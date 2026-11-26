// Schedules made in the app (DESIGN.md 7.8): rows in the schedules table that become the same
// Task a file does, so the due rules, stamps, switches, run history and the poker treat both
// alike. This file validates them (reusing task.go's rules through Task.apply, so the grammar
// has one definition), creates, edits and deletes them, and materialises a poke-type schedule's
// body as a file the reason line can name.
//
// The name space is shared with task files and a file wins: an app schedule whose name a file
// has is shadowed (listed, flagged, never run), and creating one with a file's name is refused.

package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jclement/crowsnest/internal/store"
)

// OriginApp marks a task made in the app (the other origins are the task directories').
const OriginApp = "app"

// maxRunsKept is how many runs of each task the history keeps (and GET /schedule shows).
const maxRunsKept = 20

// Store is what the service needs of the database for app schedules and run history
// (store.Store).
type Store interface {
	ListSchedules(ctx context.Context) ([]store.ScheduleRecord, error)
	GetScheduleByName(ctx context.Context, name string) (store.ScheduleRecord, error)
	InsertSchedule(ctx context.Context, record store.ScheduleRecord) (int64, error)
	UpdateSchedule(ctx context.Context, record store.ScheduleRecord) error
	DeleteSchedule(ctx context.Context, id int64) error
	AddScheduleRun(ctx context.Context, run store.ScheduleRun, keep int) error
	ListScheduleRuns(ctx context.Context, name string, limit int) ([]store.ScheduleRun, error)
}

// ScheduleInput is an app schedule as POST /schedule and PUT /schedule/{name} take it. Every
// and After use the task-file grammar; the UI's words map onto it (every N minutes → "Nm",
// daily at 17:00 → every "1d" after "17:00", on poke → "poke", on change → "change").
type ScheduleInput struct {
	Name      string `json:"name"`
	Every     string `json:"every"`
	After     string `json:"after"`
	As        string `json:"as"`         // poke (the default) or job
	ProjectID int64  `json:"project_id"` // poke: 0 the orchestrator, else that project's lead; job: the job's project
	BodyMD    string `json:"body_md"`    // the instruction (poke) or the brief (job)
	Dir       string `json:"dir"`        // job only: absolute, or "" for none
	Role      string `json:"role"`       // job only
	Engine    string `json:"engine"`     // job only: claude or codex ("" the default)
}

// ValidationError is an app schedule the service refuses; Message says what to fix.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// Errors of the app-schedule calls, besides ValidationError and ErrNoTask.
var (
	// ErrNameTaken: a task file or another app schedule already has the name.
	ErrNameTaken = errors.New("a scheduled task already has that name")
	// ErrReadOnly: the name is a task file's, which only its file changes.
	ErrReadOnly = errors.New("the task is a file; edit the file")
	// ErrNoAppSchedules: the service was built without a Store.
	ErrNoAppSchedules = errors.New("schedules made in the app are not available in this server")
)

// ---- Create, edit, delete --------------------------------------------------------------------

// Create validates and stores a new app schedule. It is ErrNameTaken when a task file or app
// schedule has the name, a *ValidationError when the input is wrong.
func (s *Service) Create(ctx context.Context, input ScheduleInput) error {
	if s.opts.Store == nil {
		return ErrNoAppSchedules
	}
	record, err := s.validate(ctx, input)
	if err != nil {
		return err
	}
	if s.fileNames()[record.Name] != "" {
		return ErrNameTaken
	}
	record.CreatedAt, record.UpdatedAt = s.opts.Now(), s.opts.Now()
	if _, err := s.opts.Store.InsertSchedule(ctx, record); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ErrNameTaken
		}
		return fmt.Errorf("creating schedule %s: %w", record.Name, err)
	}
	s.publish()
	return nil
}

// Update replaces every field of the app schedule name (a schedule is not renamed: its name is
// the key of its stamps and history). A task file's name is ErrReadOnly; an unknown one ErrNoTask.
func (s *Service) Update(ctx context.Context, name string, input ScheduleInput) error {
	if s.fileNames()[name] != "" {
		return ErrReadOnly
	}
	if s.opts.Store == nil {
		return ErrNoTask
	}
	existing, err := s.opts.Store.GetScheduleByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoTask
	}
	if err != nil {
		return fmt.Errorf("reading schedule %s: %w", name, err)
	}
	if input.Name != "" && input.Name != name {
		return invalid("a schedule cannot be renamed; delete it and make a new one")
	}
	input.Name = name
	record, err := s.validate(ctx, input)
	if err != nil {
		return err
	}
	record.ID, record.CreatedAt, record.UpdatedAt = existing.ID, existing.CreatedAt, s.opts.Now()
	if err := s.opts.Store.UpdateSchedule(ctx, record); err != nil {
		return fmt.Errorf("updating schedule %s: %w", name, err)
	}
	if record.RunAs != string(RunAsPoke) {
		s.unmaterialise(name) // a job's brief travels in the job, not in a file
	}
	s.publish()
	return nil
}

// Delete removes the app schedule name, its materialised file, its stamp and its switch (a new
// schedule of the same name starts fresh); its run history stays. A task file's name is
// ErrReadOnly; an unknown one ErrNoTask.
func (s *Service) Delete(ctx context.Context, name string) error {
	if s.fileNames()[name] != "" {
		return ErrReadOnly
	}
	if s.opts.Store == nil {
		return ErrNoTask
	}
	existing, err := s.opts.Store.GetScheduleByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoTask
	}
	if err != nil {
		return fmt.Errorf("reading schedule %s: %w", name, err)
	}
	if err := s.opts.Store.DeleteSchedule(ctx, existing.ID); err != nil {
		return fmt.Errorf("deleting schedule %s: %w", name, err)
	}
	s.unmaterialise(name)
	s.forget(ctx, name)
	s.publish()
	return nil
}

// validate turns input into a record, or a *ValidationError saying what to fix.
func (s *Service) validate(ctx context.Context, input ScheduleInput) (store.ScheduleRecord, error) {
	record := store.ScheduleRecord{
		Name: strings.TrimSpace(input.Name), Every: strings.ToLower(strings.TrimSpace(input.Every)),
		After: strings.TrimSpace(input.After), RunAs: strings.TrimSpace(input.As), ProjectID: input.ProjectID,
		BodyMD: strings.TrimSpace(input.BodyMD), Dir: strings.TrimSpace(input.Dir),
		Role: strings.TrimSpace(input.Role), Engine: strings.TrimSpace(input.Engine),
	}
	if !namePattern.MatchString(record.Name) {
		return record, invalid("a name is lower-case letters, digits, '-' and '_' (at most 64), starting with a letter or digit")
	}
	if record.RunAs == "" {
		record.RunAs = string(RunAsPoke)
	}
	if record.Dir != "" {
		record.Dir = filepath.Clean(record.Dir)
	}
	if _, err := taskFromRecord(record); err != nil {
		return record, &ValidationError{Message: err.Error()}
	}
	if record.ProjectID < 0 {
		return record, invalid("project_id %d is not a project", record.ProjectID)
	}
	if record.ProjectID != 0 {
		if _, byID := s.projectMaps(ctx); byID[record.ProjectID] == "" {
			return record, invalid("project %d does not exist or is archived", record.ProjectID)
		}
	}
	return record, nil
}

// taskFromRecord builds the Task of an app schedule with the task-file rules (pure): the same
// frontmatter checks a file gets, then the fields only the app has. Errors say what to fix.
func taskFromRecord(record store.ScheduleRecord) (Task, error) {
	body := strings.TrimSpace(record.BodyMD)
	if body == "" {
		return Task{}, errors.New("the schedule needs a body: what to do when it is due")
	}
	if len(body) > maxTaskBytes {
		return Task{}, fmt.Errorf("the body is %d bytes; a schedule is at most %d", len(body), maxTaskBytes)
	}
	task := Task{
		Name: record.Name, Origin: OriginApp, As: RunAsPoke, Body: body, Title: titleOf(body, record.Name),
		ProjectID: record.ProjectID, Engine: record.Engine,
	}
	front := map[string]string{"every": record.Every}
	for key, value := range map[string]string{"after": record.After, "as": record.RunAs, "dir": record.Dir, "role": record.Role} {
		if value != "" {
			front[key] = value
		}
	}
	if err := task.apply(front); err != nil {
		return Task{}, err
	}
	switch task.Engine {
	case "", "claude", "codex":
	default:
		return Task{}, fmt.Errorf("engine: %q is not claude or codex", task.Engine)
	}
	if task.Engine != "" && task.As != RunAsJob {
		return Task{}, errors.New("engine: applies only to a schedule run as a job")
	}
	if task.Dir != "" && !filepath.IsAbs(task.Dir) {
		return Task{}, fmt.Errorf("dir: %q is not an absolute path", task.Dir)
	}
	return task, nil
}

// ---- Loading ---------------------------------------------------------------------------------

// loadApp reads the app schedules into tasks (runnable) and shadowed (a file of the same name
// wins), with the problems. fileNames are the names the task directories hold.
func (s *Service) loadApp(ctx context.Context, fileNames map[string]string, byID map[int64]string) (tasks, shadowed []Task, problems []Problem) {
	if s.opts.Store == nil {
		return nil, nil, nil
	}
	records, err := s.opts.Store.ListSchedules(ctx)
	if err != nil {
		return nil, nil, []Problem{{Name: "*", Error: "reading the app schedules: " + err.Error()}}
	}
	for _, record := range records {
		task, err := taskFromRecord(record)
		if err == nil && task.ProjectID != 0 {
			if task.Project = byID[task.ProjectID]; task.Project == "" {
				err = fmt.Errorf("project %d does not exist or is archived; edit the schedule's target", task.ProjectID)
			}
		}
		if err != nil {
			problems = append(problems, Problem{Name: record.Name, Error: err.Error()})
			continue
		}
		if path := fileNames[task.Name]; path != "" {
			shadowed = append(shadowed, task)
			problems = append(problems, Problem{Name: task.Name,
				Error: "the task file " + path + " has the same name and wins; this schedule does not run while it exists"})
			continue
		}
		if task.As == RunAsPoke {
			if task.Path, err = s.materialise(task); err != nil {
				problems = append(problems, Problem{Name: task.Name, Error: err.Error()})
				continue
			}
		}
		tasks = append(tasks, task)
	}
	return tasks, shadowed, problems
}

// fileNames maps every task-file name in the sources to its first path, parseable or not: a
// broken file still owns its name, so fixing it never starts a second task of that name.
func (s *Service) fileNames() map[string]string {
	names := map[string]string{}
	for _, source := range s.opts.Sources {
		paths, err := filepath.Glob(filepath.Join(source.Dir, "*.md"))
		if err != nil {
			continue
		}
		for _, path := range paths {
			if name, err := NameOf(path); err == nil && names[name] == "" {
				names[name] = path
			}
		}
	}
	return names
}

// projectMaps are the live projects by slug and by id (both empty without Options.Projects).
func (s *Service) projectMaps(ctx context.Context) (map[string]int64, map[int64]string) {
	bySlug, byID := map[string]int64{}, map[int64]string{}
	if s.opts.Projects == nil {
		return bySlug, byID
	}
	for slug, id := range s.opts.Projects(ctx) {
		bySlug[slug], byID[id] = id, slug
	}
	return bySlug, byID
}

// ---- The materialised file -------------------------------------------------------------------

// materialisedDir holds app poke schedules' bodies, under the state directory.
const materialisedDir = "schedules"

// materialise writes a poke-type app schedule's body to <StateDir>/schedules/<name>.md (0600),
// rewriting it only when it changed, and returns its path: the poke's reason line tells the
// standing agent to read it, exactly as it does for a task file.
func (s *Service) materialise(task Task) (string, error) {
	if s.opts.StateDir == "" {
		return "", errors.New("this server has no state directory to write the schedule's file to")
	}
	dir := filepath.Join(s.opts.StateDir, materialisedDir)
	path := filepath.Join(dir, task.Name+".md") // the name is namePattern's, so it stays in dir
	content := []byte(task.Body + "\n")
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, content) { //nolint:gosec // our own file
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("making %s: %w", dir, err)
	}
	// Written beside and renamed over, so an agent reading it never sees half a body.
	temp, err := os.CreateTemp(dir, "."+task.Name+"-*.md")
	if err != nil {
		return "", fmt.Errorf("writing the schedule's file: %w", err)
	}
	_, writeErr := temp.Write(content)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(temp.Name())
		return "", fmt.Errorf("writing the schedule's file: %w", err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		_ = os.Remove(temp.Name())
		return "", fmt.Errorf("writing the schedule's file: %w", err)
	}
	return path, nil
}

// unmaterialise removes an app schedule's file, if there is one.
func (s *Service) unmaterialise(name string) {
	if s.opts.StateDir != "" {
		_ = os.Remove(filepath.Join(s.opts.StateDir, materialisedDir, name+".md"))
	}
}
