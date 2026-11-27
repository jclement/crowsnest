// Tests for app schedules: CRUD and validation, their due rules and materialised files, a task
// file winning a shared name, per-target DueTasksFor, `project:` resolution and run history.

package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// appService is the harness's service with app schedules, run history and h.projects.
func (h *harness) appService() *Service {
	service := h.service()
	service.opts.Store, service.opts.StateDir = h.db, h.stateDir
	service.opts.Projects = func(context.Context) map[string]int64 { return h.projects }
	return service
}

func taskNamed(t *testing.T, view View, name string) TaskView {
	t.Helper()
	for _, row := range view.Tasks {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("no task %s in %+v", name, view.Tasks)
	return TaskView{}
}

func TestAppScheduleValidation(t *testing.T) {
	h := newHarness(t)
	h.projects = map[string]int64{"alpha": 7}
	service := h.appService()
	ctx := context.Background()
	big := strings.Repeat("x", maxTaskBytes+1)
	cases := []struct {
		name  string
		input ScheduleInput
		want  string
	}{
		{"bad name", ScheduleInput{Name: "Bad Name", Every: "1h", BodyMD: "x"}, "lower-case"},
		{"no every", ScheduleInput{Name: "a", BodyMD: "x"}, "every: is missing"},
		{"too often", ScheduleInput{Name: "a", Every: "2m", BodyMD: "x"}, "too often"},
		{"bad after", ScheduleInput{Name: "a", Every: "1d", After: "25:00", BodyMD: "x"}, "not a time"},
		{"bad run_as", ScheduleInput{Name: "a", Every: "1h", As: "robot", BodyMD: "x"}, "not poke or job"},
		{"empty body", ScheduleInput{Name: "a", Every: "1h", BodyMD: "  "}, "needs a body"},
		{"huge body", ScheduleInput{Name: "a", Every: "1h", BodyMD: big}, "at most"},
		{"dir on a poke", ScheduleInput{Name: "a", Every: "1h", Dir: "/x", BodyMD: "x"}, "only to a task with as: job"},
		{"relative dir", ScheduleInput{Name: "a", Every: "1h", As: "job", Dir: "rel/x", BodyMD: "x"}, "absolute"},
		{"bad role", ScheduleInput{Name: "a", Every: "1h", As: "job", Role: "Big Boss", BodyMD: "x"}, "not a role name"},
		{"bad engine", ScheduleInput{Name: "a", Every: "1h", As: "job", Engine: "gpt", BodyMD: "x"}, "not claude or codex"},
		{"engine on a poke", ScheduleInput{Name: "a", Every: "1h", Engine: "codex", BodyMD: "x"}, "only to a schedule run as a job"},
		{"poke cadence job", ScheduleInput{Name: "a", Every: "change", As: "job", BodyMD: "x"}, "cannot be as: job"},
		{"unknown project", ScheduleInput{Name: "a", Every: "1h", ProjectID: 9, BodyMD: "x"}, "does not exist"},
	}
	for _, tc := range cases {
		err := service.Create(ctx, tc.input)
		var invalid *ValidationError
		if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, tc.want) {
			t.Errorf("%s: error %v, want a validation error mentioning %q", tc.name, err, tc.want)
		}
	}
	if records, _ := h.db.ListSchedules(ctx); len(records) != 0 {
		t.Fatalf("an invalid schedule was stored: %+v", records)
	}
	if err := New(Options{KV: h.db}).Create(ctx, ScheduleInput{Name: "a", Every: "1h", BodyMD: "x"}); !errors.Is(err, ErrNoAppSchedules) {
		t.Fatalf("no store: %v", err)
	}
}

func TestAppScheduleCRUD(t *testing.T) {
	h := newHarness(t)
	h.projects = map[string]int64{"alpha": 7}
	ctx := context.Background()
	h.write(t, h.dir, "check.md", "---\nevery: 15m\n---\n# Anything stuck?\n")
	service := h.appService()

	if err := service.Create(ctx, ScheduleInput{Name: "digest", Every: "1d", After: "17:00", BodyMD: "# Daily digest\nSummarise."}); err != nil {
		t.Fatal(err)
	}
	row := taskNamed(t, service.List(ctx), "digest")
	materialised := filepath.Join(h.stateDir, "schedules", "digest.md")
	if row.Origin != OriginApp || !row.Editable || row.Target != TargetOrchestrator || row.Schedule != "every 1d after 17:00" ||
		row.Title != "Daily digest" || row.BodyMD != "# Daily digest\nSummarise." || row.Path != materialised || row.Shadowed {
		t.Fatalf("created row = %+v", row)
	}
	if info, err := os.Stat(materialised); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("materialised file: %v %v", info, err)
	}
	if file := taskNamed(t, service.List(ctx), "check"); file.Editable || file.BodyMD != "" || file.Origin != OriginDir {
		t.Fatalf("a file row is read-only and carries no body: %+v", file)
	}

	for _, name := range []string{"digest", "check"} { // an app schedule's and a file's name
		if err := service.Create(ctx, ScheduleInput{Name: name, Every: "1h", BodyMD: "x"}); !errors.Is(err, ErrNameTaken) {
			t.Errorf("create %s again: %v, want ErrNameTaken", name, err)
		}
	}

	edit := ScheduleInput{Every: "4h", As: "poke", ProjectID: 7, BodyMD: "Look at alpha."}
	if err := service.Update(ctx, "digest", edit); err != nil {
		t.Fatal(err)
	}
	row = taskNamed(t, service.List(ctx), "digest")
	if row.Every != "4h" || row.After != "" || row.Target != TargetLead || row.Project != "alpha" || row.ProjectID != 7 {
		t.Fatalf("edited row = %+v", row)
	}
	if body, _ := os.ReadFile(materialised); string(body) != "Look at alpha.\n" { //nolint:gosec // the test's own file
		t.Fatalf("the materialised file was not rewritten: %q", body)
	}
	if err := service.Update(ctx, "digest", ScheduleInput{Name: "other", Every: "1h", BodyMD: "x"}); err == nil {
		t.Fatal("a rename was accepted")
	}
	if err := service.Update(ctx, "check", edit); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("edit a file: %v", err)
	}
	if err := service.Update(ctx, "nope", edit); !errors.Is(err, ErrNoTask) {
		t.Fatalf("edit unknown: %v", err)
	}

	// The switch works on app schedules, and deleting one forgets it.
	if err := service.SetEnabled(ctx, "digest", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, "check"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("delete a file: %v", err)
	}
	if err := service.Delete(ctx, "digest"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(materialised); !os.IsNotExist(err) {
		t.Fatalf("the materialised file survived the delete: %v", err)
	}
	if err := service.Delete(ctx, "digest"); !errors.Is(err, ErrNoTask) {
		t.Fatalf("delete twice: %v", err)
	}
	if err := service.Create(ctx, ScheduleInput{Name: "digest", Every: "1h", BodyMD: "again"}); err != nil {
		t.Fatal(err)
	}
	if row := taskNamed(t, service.List(ctx), "digest"); !row.Enabled {
		t.Fatal("a re-made schedule inherited the deleted one's switch")
	}
}

func TestAppScheduleDue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	service := h.appService()
	// h.now is 18:00 UTC: past a daily 17:00, before a daily 23:00.
	for _, input := range []ScheduleInput{
		{Name: "digest", Every: "1d", After: "17:00", BodyMD: "Digest."},
		{Name: "late", Every: "1d", After: "23:00", BodyMD: "Late."},
		{Name: "ride", Every: "poke", BodyMD: "Ride along."},
		{Name: "build", Every: "1h", As: "job", Dir: "/tmp/repo", Role: "builder", Engine: "codex", BodyMD: "# Build\nRun it."},
	} {
		if err := service.Create(ctx, input); err != nil {
			t.Fatalf("%s: %v", input.Name, err)
		}
	}
	due := service.DueTasks(ctx, h.now)
	if got := names(due); strings.Join(got, ",") != "digest,ride" || due[0].RideAlong || !due[1].RideAlong {
		t.Fatalf("due = %+v", due)
	}
	if want := filepath.Join(h.stateDir, "schedules", "digest.md"); !strings.Contains(due[0].Text, "read "+want+" and do what it says") {
		t.Fatalf("the reason line does not name the materialised file: %q", due[0].Text)
	}
	service.Delivered(ctx, h.now, []string{"digest"})
	h.now = h.now.Add(2 * time.Hour) // 20:00 the same day: once a day
	if got := names(h.appService().DueTasks(ctx, h.now)); strings.Join(got, ",") != "ride" {
		t.Fatalf("after delivery and restart, due = %v", got)
	}
	h.now = h.now.Add(22 * time.Hour) // 18:00 the next day
	if got := names(service.DueTasks(ctx, h.now)); strings.Join(got, ",") != "digest,ride" {
		t.Fatalf("next day, due = %v", got)
	}

	service.Tick(ctx)
	if len(h.jobs.created) != 1 {
		t.Fatalf("created = %+v", h.jobs.created)
	}
	job := h.jobs.created[0]
	if job.Dir != "/tmp/repo" || job.Role != "builder" || job.Engine != "codex" || !strings.HasPrefix(job.Brief, "Build\n") ||
		!strings.Contains(job.Brief, "made in the app") || !strings.Contains(job.Brief, "Run it.") {
		t.Fatalf("job = %+v", job)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "schedules", "build.md")); !os.IsNotExist(err) {
		t.Fatal("a job schedule was materialised")
	}
}

func TestFileWinsOverAppSchedule(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "check.md", "---\nevery: 15m\n---\n# The file\n")
	// The schedule existed before the file appeared (Create would refuse the name).
	for _, record := range []store.ScheduleRecord{
		{Name: "check", Every: "15m", RunAs: "poke", BodyMD: "The app one."},
		{Name: "nightly", Every: "1h", RunAs: "job", BodyMD: "Build."},
	} {
		if _, err := h.db.InsertSchedule(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	h.write(t, h.userDir, "nightly.md", "---\nevery: 1d\nafter: 23:00\n---\nNot yet.\n")
	service := h.appService()

	due := service.DueTasks(ctx, h.now)
	if len(due) != 1 || !strings.Contains(due[0].Text, filepath.Join(h.dir, "check.md")) {
		t.Fatalf("due = %+v, want the file's check only", due)
	}
	service.Tick(ctx)
	if len(h.jobs.created) != 0 {
		t.Fatalf("a shadowed job schedule ran: %+v", h.jobs.created)
	}
	view := service.List(ctx)
	var shadowed int
	for _, row := range view.Tasks {
		if row.Shadowed {
			shadowed++
			if row.Origin != OriginApp || row.Enabled || row.NextDueAt != nil || row.LastRunAt != nil {
				t.Errorf("shadowed row carries the file's stamps: %+v", row)
			}
		}
	}
	if len(view.Tasks) != 4 || shadowed != 2 {
		t.Fatalf("want two files and two shadowed app rows, got %+v", view.Tasks)
	}
	problems := 0
	for _, problem := range view.Problems {
		if problem.Name != "" && strings.Contains(problem.Error, "wins") {
			problems++
		}
	}
	if problems != 2 {
		t.Fatalf("problems = %+v", view.Problems)
	}
	if err := service.RunNow(ctx, "nightly"); err != nil || len(h.jobs.created) != 0 {
		t.Fatalf("Run now must run the file, not the shadowed app job: %v %+v", err, h.jobs.created)
	}
}

func TestDueTasksPerTarget(t *testing.T) {
	h := newHarness(t)
	h.projects = map[string]int64{"alpha": 3, "beta": 4}
	ctx := context.Background()
	h.write(t, h.dir, "orch.md", "---\nevery: 15m\n---\nFor the orchestrator.\n")
	h.write(t, h.dir, "alpha-check.md", "---\nevery: 15m\nproject: alpha\n---\nFor alpha's lead.\n")
	h.write(t, h.dir, "ghost.md", "---\nevery: 15m\nproject: ghost\n---\nNobody.\n")
	h.write(t, h.dir, "alpha-build.md", "---\nevery: 1h\nas: job\nproject: alpha\n---\nBuild alpha.\n")
	service := h.appService()
	approveAll(t, service)
	if err := service.Create(ctx, ScheduleInput{Name: "beta-check", Every: "15m", ProjectID: 4, BodyMD: "For beta's lead."}); err != nil {
		t.Fatal(err)
	}
	for projectID, want := range map[int64]string{0: "orch", 3: "alpha-check", 4: "beta-check", 99: ""} {
		if got := strings.Join(names(service.DueTasksFor(ctx, h.now, projectID)), ","); got != want {
			t.Errorf("DueTasksFor(%d) = %q, want %q", projectID, got, want)
		}
	}
	// Delivered stamps whichever agent the poke went to.
	service.Delivered(ctx, h.now, []string{"alpha-check"})
	if got := service.DueTasksFor(ctx, h.now.Add(time.Minute), 3); len(got) != 0 {
		t.Fatalf("delivered to alpha's lead, still due: %+v", got)
	}
	service.Tick(ctx)
	if len(h.jobs.created) != 1 || h.jobs.created[0].ProjectID != 3 {
		t.Fatalf("the job task joins its project: %+v", h.jobs.created)
	}
	view := service.ListProject(ctx, 3)
	if len(view.Tasks) != 2 || view.Tasks[0].Project != "alpha" {
		t.Fatalf("project 3's tasks: %+v", view.Tasks)
	}
	if !strings.Contains(problemText(view.Problems), "project: ghost is not a live project") {
		t.Fatalf("the unknown slug is not a problem: %+v", view.Problems)
	}
	// Without projects, a task naming one is a problem, never run for the orchestrator.
	plain := h.service()
	if got := strings.Join(names(plain.DueTasks(ctx, h.now)), ","); got != "orch" {
		t.Fatalf("without projects, due = %q", got)
	}
}

func problemText(problems []Problem) string {
	var lines []string
	for _, problem := range problems {
		lines = append(lines, problem.subject()+": "+problem.Error)
	}
	return strings.Join(lines, "\n")
}

func TestRunHistory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "build.md", "---\nevery: 1h\nas: job\n---\nBuild.\n")
	h.write(t, h.dir, "check.md", "---\nevery: 15m\n---\nCheck.\n")
	service := h.appService()
	approveAll(t, service)

	service.Tick(ctx) // j_1 created
	h.now = h.now.Add(2 * time.Hour)
	service.Tick(ctx) // waiting on j_1 ...
	service.Tick(ctx) // ... still: not a second history line
	h.jobs.states["j_1"] = "done"
	service.Tick(ctx) // j_2
	runs := taskNamed(t, service.List(ctx), "build").Runs
	if len(runs) != 3 || runs[0].Outcome != "job j_2 created" || runs[0].JobID != "j_2" ||
		!strings.HasPrefix(runs[1].Outcome, "waiting: the previous run, j_1") || runs[2].JobID != "j_1" {
		t.Fatalf("runs = %+v", runs)
	}

	for i := range maxRunsKept + 5 {
		service.Delivered(ctx, h.now.Add(time.Duration(i)*time.Minute), []string{"check"})
	}
	runs = taskNamed(t, service.List(ctx), "check").Runs
	if len(runs) != maxRunsKept || runs[0].Outcome != outcomeDelivered || !runs[0].At.After(runs[1].At) {
		t.Fatalf("want the newest %d runs, newest first; got %d: %+v", maxRunsKept, len(runs), runs[:2])
	}
	if stored, _ := h.db.ListScheduleRuns(ctx, "check", 100); len(stored) != maxRunsKept {
		t.Fatalf("the history is not pruned: %d rows", len(stored))
	}

	failing := h.appService()
	failing.opts.Jobs = nil
	h.now = h.now.Add(2 * time.Hour)
	failing.Tick(ctx)
	failing.Tick(ctx)
	if runs := taskNamed(t, failing.List(ctx), "build").Runs; runs[0].Outcome != "not run: jobs are unavailable in this server" ||
		runs[1].Outcome == runs[0].Outcome {
		t.Fatalf("an unavailable run is recorded once: %+v", runs[:2])
	}
}
