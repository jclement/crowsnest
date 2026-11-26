// The runtime half of scheduled work: reads the task directories on every check (a new or edited
// file is picked up at the next one, nothing to restart), keeps each task's stamp and the on/off
// switches in one kv blob, hands the poker the poke-type tasks that are due, stamps them when
// the poke carrying them is delivered, and creates the jobs of `as: job` tasks.
//
// A file that cannot be read or parsed is reported once in the journal (by path and error, kept
// across restarts) and otherwise skipped: one bad file never stops the others.

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
	KV       KV
	Sources  []Source // read in order; a name already taken by an earlier source is a problem
	Jobs     Jobs     // nil: as: job tasks are reported as unavailable
	Journal  *journal.Journal
	Hub      *hub.Hub // nil: no `schedule` events
	Logger   *slog.Logger
	Location *time.Location // nil: time.Local
	// Picture lists the fleet and job picture, one line each, for every: change. Nil: never.
	Picture func(ctx context.Context) []string
	// Kick asks the poker to check now (a Run now of a poke task). Nil: the next check.
	Kick func()
	Now  func() time.Time
}

// state is the kv blob.
type state struct {
	Stamps   map[string]Stamp `json:"stamps"`
	Disabled []string         `json:"disabled,omitempty"`
	Reported map[string]int64 `json:"reported,omitempty"` // problem key → when it was journalled
}

// Problem is a task file that could not be used.
type Problem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
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

// ---- Reading the task files ----------------------------------------------------------------

// Load reads every source: the tasks, and the files that could not be used. A missing
// directory is not a problem (most people have no tasks).
func (s *Service) Load() ([]Task, []Problem) {
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
		key := fingerprint([]string{problem.Path, problem.Error})
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
		s.opts.Journal.Add(ctx, journal.KindAlert, "Scheduled task "+problem.Path+" is skipped: "+problem.Error,
			map[string]any{"path": problem.Path, "error": problem.Error})
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

// DueTasks lists the poke-type tasks due at now. Nothing is stamped until Delivered.
func (s *Service) DueTasks(ctx context.Context, now time.Time) []DueTask {
	tasks, problems := s.Load()
	s.reportProblems(ctx, problems)
	picture := ""
	if s.opts.Picture != nil && slices.ContainsFunc(tasks, func(t Task) bool { return t.Every.Kind == CadenceChange }) {
		picture = fingerprint(s.opts.Picture(ctx))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked(ctx)
	var due []DueTask
	for _, task := range tasks {
		if task.As != RunAsPoke {
			continue
		}
		verdict := Check(task, s.inputLocked(task, now, picture))
		if verdict == NotDue {
			continue
		}
		s.pictures[task.Name] = picture
		due = append(due, DueTask{Name: task.Name, Text: s.reasonText(task), RideAlong: verdict == RideAlong})
	}
	return due
}

// reasonText is the poke line: the orchestrator reads the file and does what it says. Called
// with s.mu held.
func (s *Service) reasonText(task Task) string {
	last := "never run"
	if stamp := s.state.Stamps[task.Name]; !stamp.LastRun.IsZero() {
		last = "last run " + stamp.LastRun.In(s.opts.Location).Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("scheduled task %s is due (%s, %s): read %s and do what it says", task.Name, task.Schedule(), last, task.Path)
}

// Delivered stamps the tasks a delivered poke carried: they are not due again until their next
// time, whatever the orchestrator made of them.
func (s *Service) Delivered(ctx context.Context, at time.Time, names []string) {
	if len(names) == 0 {
		return
	}
	s.mu.Lock()
	s.loadLocked(ctx)
	for _, name := range names {
		stamp := s.state.Stamps[name]
		stamp.LastRun, stamp.RunNow, stamp.JobID = at, false, ""
		stamp.LastOutcome = "carried by a poke"
		if picture, ok := s.pictures[name]; ok && picture != "" {
			stamp.Picture = picture
		}
		s.state.Stamps[name] = stamp
	}
	s.saveLocked(ctx)
	s.mu.Unlock()
	s.publish()
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
	tasks, problems := s.Load()
	s.reportProblems(ctx, problems)
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
		s.runJob(ctx, task, now, previous)
	}
}

func (s *Service) runJob(ctx context.Context, task Task, now time.Time, previous string) {
	if s.opts.Jobs == nil {
		s.stamp(ctx, task.Name, func(st *Stamp) { st.RunNow, st.LastOutcome = false, "not run: jobs are unavailable in this server" })
		return
	}
	if previous != "" {
		if jobState, final, err := s.opts.Jobs.JobState(ctx, previous); err == nil && !final {
			s.stamp(ctx, task.Name, func(st *Stamp) {
				st.LastOutcome = fmt.Sprintf("waiting: the previous run, %s, is still %s", previous, jobState)
			})
			return
		}
	}
	id, err := s.opts.Jobs.CreateJob(ctx, JobRequest{Brief: jobBrief(task), Dir: task.Dir, Role: task.Role})
	if err != nil {
		s.opts.Logger.Warn("creating a scheduled job", "task", task.Name, "err", err)
		s.stamp(ctx, task.Name, func(st *Stamp) {
			st.LastRun, st.RunNow, st.LastOutcome = now, false, "could not create the job: "+oneLine(err.Error())
		})
		s.opts.Journal.Add(ctx, journal.KindAlert, "Scheduled task "+task.Name+" could not create its job: "+oneLine(err.Error()), map[string]any{"task": task.Name})
		return
	}
	s.stamp(ctx, task.Name, func(st *Stamp) {
		st.LastRun, st.RunNow, st.JobID, st.LastOutcome = now, false, id, "job "+id+" created"
	})
	s.opts.Journal.Add(ctx, journal.KindTask, "Scheduled task "+task.Name+" started job "+id, map[string]any{"task": task.Name, "job_id": id})
}

// jobBrief is the brief of a job task: where it came from, then the file's body.
func jobBrief(task Task) string {
	return fmt.Sprintf("%s\n\n(Scheduled task %s from %s, %s.)\n\n%s", task.Title, task.Name, task.Path, task.Schedule(), task.Body)
}

// ---- The switches and Run now ----------------------------------------------------------------

// ErrNoTask is returned for a name no task file has.
var ErrNoTask = errors.New("no such scheduled task")

// RunNow makes a task due at once: a poke task goes with the next poke the gate allows, a job
// task starts its job now.
func (s *Service) RunNow(ctx context.Context, name string) error {
	task, err := s.find(name)
	if err != nil {
		return err
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

// SetEnabled switches a task on or off. The file stays the source of what it says; only the
// switch is crowsnest's.
func (s *Service) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if _, err := s.find(name); err != nil {
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

func (s *Service) find(name string) (Task, error) {
	tasks, _ := s.Load()
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
	Origin      string     `json:"origin"`
	Every       string     `json:"every"`
	After       string     `json:"after,omitempty"`
	Schedule    string     `json:"schedule"` // in words
	As          RunAs      `json:"as"`
	Dir         string     `json:"dir,omitempty"`
	Role        string     `json:"role,omitempty"`
	Enabled     bool       `json:"enabled"`
	LastRunAt   *time.Time `json:"last_run_at"`
	NextDueAt   *time.Time `json:"next_due_at"` // nil: with pokes, on change, or disabled
	LastOutcome string     `json:"last_outcome,omitempty"`
	JobID       string     `json:"job_id,omitempty"`
	JobState    string     `json:"job_state,omitempty"`
	RunNow      bool       `json:"run_now"` // pressed, not yet carried out
}

// View is GET /schedule.
type View struct {
	Tasks    []TaskView `json:"tasks"`
	Problems []Problem  `json:"problems"`
	Dirs     []string   `json:"dirs"` // where task files are read from, in order
}

// List is the Schedule view: every task with its cadence, stamps and next time.
func (s *Service) List(ctx context.Context) View {
	tasks, problems := s.Load()
	now := s.opts.Now()
	view := View{Tasks: []TaskView{}, Problems: problems, Dirs: []string{}}
	if view.Problems == nil {
		view.Problems = []Problem{}
	}
	for _, source := range s.opts.Sources {
		view.Dirs = append(view.Dirs, source.Dir)
	}
	s.mu.Lock()
	s.loadLocked(ctx)
	for _, task := range tasks {
		view.Tasks = append(view.Tasks, s.viewLocked(task, now))
	}
	s.mu.Unlock()
	for i := range view.Tasks {
		if id := view.Tasks[i].JobID; id != "" && s.opts.Jobs != nil {
			if jobState, _, err := s.opts.Jobs.JobState(ctx, id); err == nil {
				view.Tasks[i].JobState = jobState
			}
		}
	}
	return view
}

func (s *Service) viewLocked(task Task, now time.Time) TaskView {
	in := s.inputLocked(task, now, "")
	row := TaskView{
		Name: task.Name, Title: task.Title, Path: task.Path, Origin: task.Origin, Every: task.Every.Text,
		Schedule: task.Schedule(), As: task.As, Dir: task.Dir, Role: task.Role, Enabled: in.Enabled,
		LastOutcome: in.Stamp.LastOutcome, JobID: in.Stamp.JobID, RunNow: in.Stamp.RunNow,
	}
	if task.After != nil {
		row.After = task.After.Text
	}
	if !in.Stamp.LastRun.IsZero() {
		last := in.Stamp.LastRun
		row.LastRunAt = &last
	}
	if next := NextDue(task, in); !next.IsZero() {
		row.NextDueAt = &next
	}
	return row
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
