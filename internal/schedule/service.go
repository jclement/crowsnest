// The runtime half of scheduled work: reads the task directories and the app schedules on every
// check (a new or edited file is picked up at the next one, nothing to restart), keeps each
// task's stamp and the on/off switches in one kv blob, hands the poker the poke-type tasks due
// for each standing agent (the orchestrator, or a project's lead), stamps them when the poke
// carrying them is delivered, creates the jobs of `as: job` tasks, and records every run in the
// schedule_runs history.
//
// A file or app schedule that cannot be used is reported once in the journal (by what it is and
// the error, kept across restarts) and otherwise skipped: one bad task never stops the others.

package schedule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// kvState is the kv key of the stamps, switches and reported problems.
	kvState = "schedule.state"
	// DefaultTickInterval is how often job tasks are checked (poke tasks ride the poker's checks).
	DefaultTickInterval = 30 * time.Second
	// maxReported bounds the remembered problem reports.
	maxReported = 200
	// Origins of a task file.
	OriginDir  = "dir"  // <orchestrator dir>/tasks
	OriginUser = "user" // ~/.config/crowsnest/tasks
)

// KV is the small persistent store (store.Store).
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
}

// JobRequest is a job to create for an `as: job` task.
type JobRequest struct {
	Brief, Dir, Role string
	Engine           string // "" the jobs service's default
	ProjectID        int64  // 0 unfiled
}

// Jobs is what the service needs of the jobs service (cmd adapts *jobs.Service).
type Jobs interface {
	// CreateJob records and launches a job, returning its id.
	CreateJob(ctx context.Context, request JobRequest) (string, error)
	// JobState returns a job's state and whether that state is final.
	JobState(ctx context.Context, id string) (state string, final bool, err error)
}

// Source is one directory of task files.
type Source struct {
	Dir    string
	Origin string // OriginDir or OriginUser
}

// Options configure a Service. KV and Sources are required.
type Options struct {
	KV      KV
	Sources []Source // read in order; a name already taken by an earlier source is a problem
	// Store keeps app schedules and the run history. Nil: task files only, and no history.
	Store Store
	// StateDir is where poke-type app schedules are materialised (<StateDir>/schedules).
	StateDir string
	// Projects lists the live projects, slug → id, for `project:` and app schedules' targets.
	// Nil: there are no projects, and a task naming one is a problem.
	Projects func(ctx context.Context) map[string]int64
	Jobs     Jobs // nil: as: job tasks are reported as unavailable
	Journal  *journal.Journal
	Hub      *hub.Hub // nil: no `schedule` events
	Logger   *slog.Logger
	Location *time.Location // nil: time.Local
	// Picture lists the fleet and job picture, one line each, for every: change. Nil: never.
	Picture func(ctx context.Context) []string
	// Kick asks the poker to check now (a Run now of a poke task). Nil: the next check.
	Kick func()
	Now  func() time.Time
	// Reviews records the human's approval of task files that start jobs or reach leads
	// (review.go, R5-S3). Nil: every such task is held.
	Reviews Reviews
	// Paused reports whether the human paused crowsnest's automation (DESIGN.md 6.13): no
	// `as: job` task is started meanwhile (Run now still works). Nil: never.
	Paused func() bool
}

// state is the kv blob.
type state struct {
	Stamps   map[string]Stamp `json:"stamps"`
	Disabled []string         `json:"disabled,omitempty"`
	Reported map[string]int64 `json:"reported,omitempty"` // problem key → when it was journalled
}

// Problem is a task file or app schedule that could not be used (or, shadowed, will not run).
type Problem struct {
	Path  string `json:"path"`           // the task file; "" for an app schedule
	Name  string `json:"name,omitempty"` // the app schedule
	Error string `json:"error"`
}

// subject names what the problem is about, for the journal.
func (p Problem) subject() string {
	if p.Path != "" {
		return p.Path
	}
	return "app schedule " + p.Name
}

// Service runs scheduled tasks. Build it with New.
type Service struct {
	opts Options

	mu       sync.Mutex
	loaded   bool
	state    state
	pictures map[string]string // task → picture fingerprint offered with its due reason
}

// New returns a service; nothing is read until the first call.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	return &Service{opts: opts, state: state{Stamps: map[string]Stamp{}}, pictures: map[string]string{}}
}

// ---- Reading the tasks -----------------------------------------------------------------------

// Load reads every source and the app schedules: the tasks that can run, and the ones that
// could not be used (a shadowed app schedule among them). A missing directory is not a problem
// (most people have no tasks).
func (s *Service) Load(ctx context.Context) ([]Task, []Problem) {
	all := s.load(ctx)
	return all.tasks, all.problems
}

// loaded is one read of every task.
type loaded struct {
	tasks    []Task // runnable: task files, then app schedules
	shadowed []Task // app schedules a task file of the same name hides
	problems []Problem
}

func (s *Service) load(ctx context.Context) loaded {
	bySlug, byID := s.projectMaps(ctx)
	tasks, problems := s.loadFiles(bySlug)
	apps, shadowed, appProblems := s.loadApp(ctx, s.fileNames(), byID)
	return loaded{tasks: append(tasks, apps...), shadowed: shadowed, problems: append(problems, appProblems...)}
}

// loadFiles reads the task directories, resolving each `project:` slug through bySlug.
func (s *Service) loadFiles(bySlug map[string]int64) ([]Task, []Problem) {
	var tasks []Task
	var problems []Problem
	seen := map[string]string{}
	for _, source := range s.opts.Sources {
		paths, err := filepath.Glob(filepath.Join(source.Dir, "*.md"))
		if err != nil {
			continue
		}
		sort.Strings(paths)
		for _, path := range paths {
			task, err := readTask(path, source.Origin)
			if err == nil {
				if first, taken := seen[task.Name]; taken {
					err = fmt.Errorf("a task named %s is already defined in %s, which wins", task.Name, first)
				}
			}
			if err == nil && task.Project != "" {
				if task.ProjectID = bySlug[task.Project]; task.ProjectID == 0 {
					err = fmt.Errorf("project: %s is not a live project here", task.Project)
				}
			}
			if err != nil {
				problems = append(problems, Problem{Path: path, Error: err.Error()})
				continue
			}
			seen[task.Name] = path
			tasks = append(tasks, task)
		}
	}
	return tasks, problems
}

func readTask(path, origin string) (Task, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Task{}, fmt.Errorf("reading the task file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Task{}, errors.New("not a regular file")
	}
	if info.Size() > maxTaskBytes {
		return Task{}, fmt.Errorf("the file is %d bytes; a task is at most %d", info.Size(), maxTaskBytes)
	}
	body, err := os.ReadFile(path) //nolint:gosec // the user's own task directories
	if err != nil {
		return Task{}, fmt.Errorf("reading the task file: %w", err)
	}
	return Parse(path, origin, body)
}

// reportProblems journals each new problem once (an edited file with a new error is new).
func (s *Service) reportProblems(ctx context.Context, problems []Problem) {
	s.mu.Lock()
	s.loadLocked(ctx)
	var fresh []Problem
	for _, problem := range problems {
		key := fingerprint([]string{problem.subject(), problem.Error})
		if _, done := s.state.Reported[key]; done {
			continue
		}
		if s.state.Reported == nil {
			s.state.Reported = map[string]int64{}
		}
		s.state.Reported[key] = s.opts.Now().Unix()
		fresh = append(fresh, problem)
	}
	if len(fresh) > 0 {
		s.pruneReportedLocked()
		s.saveLocked(ctx)
	}
	s.mu.Unlock()
	for _, problem := range fresh {
		s.opts.Journal.Add(ctx, journal.KindAlert, "Scheduled task "+problem.subject()+" is skipped: "+problem.Error,
			map[string]any{"path": problem.Path, "name": problem.Name, "error": problem.Error})
	}
}

func (s *Service) pruneReportedLocked() {
	for len(s.state.Reported) > maxReported {
		oldest, at := "", int64(0)
		for key, when := range s.state.Reported {
			if oldest == "" || when < at {
				oldest, at = key, when
			}
		}
		delete(s.state.Reported, oldest)
	}
}

// ---- Poke tasks ------------------------------------------------------------------------------

// DueTask is a poke-type task that is due, as the poker needs it.
type DueTask struct {
	Name      string
	Text      string // the reason line, naming the file
	RideAlong bool   // every: poke: only with a poke that has something else due
}

// DueTasks lists the poke-type tasks due at now for the orchestrator: DueTasksFor(ctx, now, 0).
func (s *Service) DueTasks(ctx context.Context, now time.Time) []DueTask {
	return s.DueTasksFor(ctx, now, 0)
}

// DueTasksFor lists the poke-type tasks due at now for one standing agent: projectID 0 is the
// orchestrator, any other the lead of that project. Nothing is stamped until Delivered.
func (s *Service) DueTasksFor(ctx context.Context, now time.Time, projectID int64) []DueTask {
	all, problems := s.Load(ctx)
	s.reportProblems(ctx, problems)
	tasks := slices.DeleteFunc(all, func(t Task) bool { return t.As != RunAsPoke || t.ProjectID != projectID || !s.mayRun(ctx, t) })
	picture := ""
	if s.opts.Picture != nil && slices.ContainsFunc(tasks, func(t Task) bool { return t.Every.Kind == CadenceChange }) {
		picture = fingerprint(s.opts.Picture(ctx))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked(ctx)
	var due []DueTask
	for _, task := range tasks {
		verdict := Check(task, s.inputLocked(task, now, picture))
		if verdict == NotDue {
			continue
		}
		s.pictures[task.Name] = picture
		due = append(due, DueTask{Name: task.Name, Text: s.reasonText(task), RideAlong: verdict == RideAlong})
	}
	return due
}

// reasonText is the poke line: the standing agent reads the file and does what it says. Called
// with s.mu held.
func (s *Service) reasonText(task Task) string {
	last := "never run"
	if stamp := s.state.Stamps[task.Name]; !stamp.LastRun.IsZero() {
		last = "last run " + stamp.LastRun.In(s.opts.Location).Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("scheduled task %s is due (%s, %s): read %s and do what it says", task.Name, task.Schedule(), last, task.Path)
}

// outcomeDelivered is the stamp and history line of a poke-type run.
const outcomeDelivered = "carried by a poke"

// Delivered stamps the tasks a delivered poke carried, whichever standing agent it went to:
// they are not due again until their next time, whatever the agent made of them. Each is a run
// in the history.
func (s *Service) Delivered(ctx context.Context, at time.Time, names []string) {
	if len(names) == 0 {
		return
	}
	s.mu.Lock()
	s.loadLocked(ctx)
	for _, name := range names {
		stamp := s.state.Stamps[name]
		stamp.LastRun, stamp.RunNow, stamp.JobID = at, false, ""
		stamp.LastOutcome = outcomeDelivered
		if picture, ok := s.pictures[name]; ok && picture != "" {
			stamp.Picture = picture
		}
		s.state.Stamps[name] = stamp
	}
	s.saveLocked(ctx)
	s.mu.Unlock()
	for _, name := range names {
		s.recordRun(ctx, name, at, outcomeDelivered, "")
	}
	s.publish()
}

// recordRun adds one line to a task's run history (kept to maxRunsKept). A failure is logged:
// the history is for the human, and must never stop a run.
func (s *Service) recordRun(ctx context.Context, name string, at time.Time, outcome, jobID string) {
	if s.opts.Store == nil {
		return
	}
	run := store.ScheduleRun{Name: name, At: at, Outcome: outcome, JobID: jobID}
	if err := s.opts.Store.AddScheduleRun(context.WithoutCancel(ctx), run, maxRunsKept); err != nil {
		s.opts.Logger.Warn("recording a scheduled run", "task", name, "err", err)
	}
}

// ---- Job tasks -------------------------------------------------------------------------------

// Run checks the job tasks every DefaultTickInterval until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(DefaultTickInterval)
	defer ticker.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick creates a job for each `as: job` task that is due, unless the job of its previous run is
// still going (a slow job never stacks up copies of itself).
func (s *Service) Tick(ctx context.Context) {
	tasks, problems := s.Load(ctx)
	s.reportProblems(ctx, problems)
	if s.opts.Paused != nil && s.opts.Paused() {
		return // due tasks stay due and run at the first tick after the resume
	}
	now := s.opts.Now()
	for _, task := range tasks {
		if task.As != RunAsJob {
			continue
		}
		s.mu.Lock()
		s.loadLocked(ctx)
		verdict := Check(task, s.inputLocked(task, now, ""))
		previous := s.state.Stamps[task.Name].JobID
		s.mu.Unlock()
		if verdict == NotDue {
			continue
		}
		if !s.mayRun(ctx, task) {
			s.notRun(ctx, task.Name, now, outcomeNeedsReview, "", nil)
			continue
		}
		s.runJob(ctx, task, now, previous)
	}
}

func (s *Service) runJob(ctx context.Context, task Task, now time.Time, previous string) {
	if s.opts.Jobs == nil {
		s.notRun(ctx, task.Name, now, "not run: jobs are unavailable in this server", "", func(st *Stamp) { st.RunNow = false })
		return
	}
	if previous != "" {
		if jobState, final, err := s.opts.Jobs.JobState(ctx, previous); err == nil && !final {
			s.notRun(ctx, task.Name, now, fmt.Sprintf("waiting: the previous run, %s, is still %s", previous, jobState), previous, nil)
			return
		}
	}
	request := JobRequest{Brief: jobBrief(task), Dir: task.Dir, Role: task.Role, Engine: task.Engine, ProjectID: task.ProjectID}
	id, err := s.opts.Jobs.CreateJob(ctx, request)
	if err != nil {
		s.opts.Logger.Warn("creating a scheduled job", "task", task.Name, "err", err)
		outcome := "could not create the job: " + oneLine(err.Error())
		s.stamp(ctx, task.Name, func(st *Stamp) { st.LastRun, st.RunNow, st.LastOutcome = now, false, outcome })
		s.recordRun(ctx, task.Name, now, outcome, "")
		s.opts.Journal.Add(ctx, journal.KindAlert, "Scheduled task "+task.Name+" could not create its job: "+oneLine(err.Error()), map[string]any{"task": task.Name})
		return
	}
	outcome := "job " + id + " created"
	s.stamp(ctx, task.Name, func(st *Stamp) { st.LastRun, st.RunNow, st.JobID, st.LastOutcome = now, false, id, outcome })
	s.recordRun(ctx, task.Name, now, outcome, id)
	s.opts.Journal.Add(ctx, journal.KindTask, "Scheduled task "+task.Name+" started job "+id, map[string]any{"task": task.Name, "job_id": id})
}

// notRun stamps an outcome that is not a run (waiting for the previous job, jobs unavailable)
// and records it in the history only when it differs from the last outcome: the task stays due
// and is looked at every tick, and twenty identical "waiting" lines would push out every run.
func (s *Service) notRun(ctx context.Context, name string, now time.Time, outcome, jobID string, change func(*Stamp)) {
	repeated := false
	s.stamp(ctx, name, func(st *Stamp) {
		repeated = st.LastOutcome == outcome
		st.LastOutcome = outcome
		if change != nil {
			change(st)
		}
	})
	if !repeated {
		s.recordRun(ctx, name, now, outcome, jobID)
	}
}

// jobBrief is the brief of a job task: where it came from, then the body.
func jobBrief(task Task) string {
	from := "from " + task.Path
	if task.Origin == OriginApp {
		from = "made in the app"
	}
	return fmt.Sprintf("%s\n\n(Scheduled task %s %s, %s.)\n\n%s", task.Title, task.Name, from, task.Schedule(), task.Body)
}

// ---- The switches and Run now ----------------------------------------------------------------

// ErrNoTask is returned for a name no task file or app schedule has.
var ErrNoTask = errors.New("no such scheduled task")

// RunNow makes a task due at once: a poke task goes with the next poke the gate allows, a job
// task starts its job now.
func (s *Service) RunNow(ctx context.Context, name string) error {
	task, err := s.find(ctx, name)
	if err != nil {
		return err
	}
	if !s.mayRun(ctx, task) {
		return ErrNeedsReview // Run now is not an approval: the human approves the content itself
	}
	s.stamp(ctx, name, func(st *Stamp) { st.RunNow = true })
	if task.As == RunAsJob {
		s.mu.Lock()
		previous := s.state.Stamps[name].JobID
		s.mu.Unlock()
		s.runJob(ctx, task, s.opts.Now(), previous)
		return nil
	}
	if s.opts.Kick != nil {
		s.opts.Kick()
	}
	return nil
}

// SetEnabled switches a task (file or app) on or off. A file stays the source of what it says;
// the switch is crowsnest's for both kinds.
func (s *Service) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if _, err := s.find(ctx, name); err != nil {
		return err
	}
	s.mu.Lock()
	s.loadLocked(ctx)
	s.state.Disabled = slices.DeleteFunc(s.state.Disabled, func(n string) bool { return n == name })
	if !enabled {
		s.state.Disabled = append(s.state.Disabled, name)
		sort.Strings(s.state.Disabled)
	}
	s.saveLocked(ctx)
	s.mu.Unlock()
	s.publish()
	return nil
}

// forget drops a deleted task's stamp and switch, so a new task of that name starts fresh.
func (s *Service) forget(ctx context.Context, name string) {
	s.mu.Lock()
	s.loadLocked(ctx)
	delete(s.state.Stamps, name)
	s.state.Disabled = slices.DeleteFunc(s.state.Disabled, func(n string) bool { return n == name })
	s.saveLocked(ctx)
	s.mu.Unlock()
}

// find returns the runnable task called name (a shadowed app schedule is not one).
func (s *Service) find(ctx context.Context, name string) (Task, error) {
	tasks, _ := s.Load(ctx)
	for _, task := range tasks {
		if task.Name == name {
			return task, nil
		}
	}
	return Task{}, ErrNoTask
}

// ---- The view --------------------------------------------------------------------------------

// TaskView is one row of GET /schedule.
type TaskView struct {
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Path        string     `json:"path"`
	Origin      string     `json:"origin"`   // app, dir or user
	Editable    bool       `json:"editable"` // an app schedule: the app may edit and delete it
	Every       string     `json:"every"`
	After       string     `json:"after,omitempty"`
	Schedule    string     `json:"schedule"` // in words
	As          RunAs      `json:"as"`
	Target      Target     `json:"target"` // orchestrator, lead or job
	ProjectID   int64      `json:"project_id"`
	Project     string     `json:"project,omitempty"` // the project's slug
	Dir         string     `json:"dir,omitempty"`
	Role        string     `json:"role,omitempty"`
	Engine      string     `json:"engine,omitempty"`
	BodyMD      string     `json:"body_md,omitempty"` // app schedules, and task files that need review
	Shadowed    bool       `json:"shadowed"`          // an app schedule a task file of the same name hides: never run
	Enabled     bool       `json:"enabled"`
	LastRunAt   *time.Time `json:"last_run_at"`
	NextDueAt   *time.Time `json:"next_due_at"` // nil: with pokes, on change, disabled or shadowed
	LastOutcome string     `json:"last_outcome,omitempty"`
	JobID       string     `json:"job_id,omitempty"`
	JobState    string     `json:"job_state,omitempty"`
	RunNow      bool       `json:"run_now"` // pressed, not yet carried out
	Runs        []RunView  `json:"runs"`    // newest first, at most 20
	// Review is "" (not reviewed), "needed" (new or changed: it does not run) or "approved"
	// (review.go). Hash is the content the view shows, which Approve must name.
	Review string `json:"review,omitempty"`
	Hash   string `json:"hash,omitempty"`
	// Source is the whole file as the hash covers it, for a task that needs review (R6-S7): the
	// card shows this raw text, so nothing Markdown would hide (an HTML comment, raw HTML, the
	// frontmatter) is approved unseen.
	Source string `json:"source,omitempty"`
}

// RunView is one line of a task's run history.
type RunView struct {
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
	JobID   string    `json:"job_id,omitempty"`
}

// View is GET /schedule.
type View struct {
	Tasks    []TaskView `json:"tasks"`
	Problems []Problem  `json:"problems"`
	Dirs     []string   `json:"dirs"` // where task files are read from, in order
}

// List is the Schedule view: every task with its cadence, stamps, next time and run history,
// then the shadowed app schedules.
func (s *Service) List(ctx context.Context) View {
	all := s.load(ctx)
	now := s.opts.Now()
	view := View{Tasks: []TaskView{}, Problems: all.problems, Dirs: []string{}}
	if view.Problems == nil {
		view.Problems = []Problem{}
	}
	for _, source := range s.opts.Sources {
		view.Dirs = append(view.Dirs, source.Dir)
	}
	s.mu.Lock()
	s.loadLocked(ctx)
	for _, task := range all.tasks {
		view.Tasks = append(view.Tasks, s.viewLocked(task, now))
	}
	s.mu.Unlock()
	for i, task := range all.tasks {
		if NeedsReview(task) {
			// The human approves what they read here: the body and the hash of these bytes.
			view.Tasks[i].Review, view.Tasks[i].Hash, view.Tasks[i].BodyMD = s.reviewState(ctx, task), task.Hash, task.Body
			view.Tasks[i].Source = task.Raw
		}
	}
	for _, task := range all.shadowed {
		view.Tasks = append(view.Tasks, shadowedView(task))
	}
	for i := range view.Tasks {
		row := &view.Tasks[i]
		if row.JobID != "" && s.opts.Jobs != nil {
			if jobState, _, err := s.opts.Jobs.JobState(ctx, row.JobID); err == nil {
				row.JobState = jobState
			}
		}
		if !row.Shadowed {
			row.Runs = s.runs(ctx, row.Name)
		}
	}
	return view
}

// ListProject is List with only the tasks of one project (0: the orchestrator's and unfiled
// jobs). Problems and directories are not filtered.
func (s *Service) ListProject(ctx context.Context, projectID int64) View {
	view := s.List(ctx)
	view.Tasks = slices.DeleteFunc(view.Tasks, func(row TaskView) bool { return row.ProjectID != projectID })
	return view
}

func (s *Service) viewLocked(task Task, now time.Time) TaskView {
	in := s.inputLocked(task, now, "")
	row := baseView(task)
	row.Enabled, row.LastOutcome, row.JobID, row.RunNow = in.Enabled, in.Stamp.LastOutcome, in.Stamp.JobID, in.Stamp.RunNow
	if !in.Stamp.LastRun.IsZero() {
		last := in.Stamp.LastRun
		row.LastRunAt = &last
	}
	if next := NextDue(task, in); !next.IsZero() {
		row.NextDueAt = &next
	}
	return row
}

// shadowedView is a shadowed app schedule's row: what it says, but none of the stamps, which
// belong to the task file of the same name.
func shadowedView(task Task) TaskView {
	row := baseView(task)
	row.Shadowed = true
	row.LastOutcome = "not run: a task file of the same name wins"
	return row
}

// baseView is the part of a row that comes from the task alone.
func baseView(task Task) TaskView {
	row := TaskView{
		Name: task.Name, Title: task.Title, Path: task.Path, Origin: task.Origin, Editable: task.Origin == OriginApp,
		Every: task.Every.Text, Schedule: task.Schedule(), As: task.As, Target: task.Target(),
		ProjectID: task.ProjectID, Project: task.Project, Dir: task.Dir, Role: task.Role, Engine: task.Engine,
		Runs: []RunView{},
	}
	if task.Origin == OriginApp {
		row.BodyMD = task.Body
	}
	if task.After != nil {
		row.After = task.After.Text
	}
	return row
}

// runs is a task's run history for the view, newest first ([] without a Store or on error).
func (s *Service) runs(ctx context.Context, name string) []RunView {
	out := []RunView{}
	if s.opts.Store == nil {
		return out
	}
	runs, err := s.opts.Store.ListScheduleRuns(ctx, name, maxRunsKept)
	if err != nil {
		s.opts.Logger.Warn("reading a scheduled task's runs", "task", name, "err", err)
		return out
	}
	for _, run := range runs {
		out = append(out, RunView{At: run.At, Outcome: run.Outcome, JobID: run.JobID})
	}
	return out
}

// ---- Bookkeeping -----------------------------------------------------------------------------

func (s *Service) inputLocked(task Task, now time.Time, picture string) DueInput {
	return DueInput{
		Now: now, Location: s.opts.Location, Stamp: s.state.Stamps[task.Name],
		Enabled: !slices.Contains(s.state.Disabled, task.Name), Picture: picture,
	}
}

// stamp changes one task's stamp and stores it.
func (s *Service) stamp(ctx context.Context, name string, change func(*Stamp)) {
	s.mu.Lock()
	s.loadLocked(ctx)
	stamp := s.state.Stamps[name]
	change(&stamp)
	s.state.Stamps[name] = stamp
	s.saveLocked(ctx)
	s.mu.Unlock()
	s.publish()
}

// loadLocked reads the kv blob once. An unreadable blob starts empty: every task is then due
// once, which is the cost of losing the stamps, not a silent stop.
func (s *Service) loadLocked(ctx context.Context) {
	if s.loaded {
		return
	}
	s.loaded = true
	raw, err := s.opts.KV.GetKV(ctx, kvState)
	if err != nil {
		return
	}
	var loaded state
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		s.opts.Logger.Warn("ignoring unreadable schedule stamps", "err", err)
		return
	}
	if loaded.Stamps == nil {
		loaded.Stamps = map[string]Stamp{}
	}
	s.state = loaded
}

func (s *Service) saveLocked(ctx context.Context) {
	body, err := json.Marshal(s.state)
	if err != nil {
		return
	}
	if err := s.opts.KV.SetKV(context.WithoutCancel(ctx), kvState, string(body)); err != nil {
		s.opts.Logger.Warn("storing schedule stamps", "err", err)
	}
}

func (s *Service) publish() {
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "schedule", Data: map[string]any{"at": s.opts.Now()}})
	}
}

// fingerprint is a short stable hash of some lines (order-independent).
func fingerprint(lines []string) string {
	sorted := slices.Clone(lines)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:8])
}

func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 200 {
		return string(runes[:199]) + "…"
	}
	return text
}
