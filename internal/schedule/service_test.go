package schedule

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
)

type fakeJobs struct {
	mu      sync.Mutex
	created []JobRequest
	states  map[string]string
}

func (f *fakeJobs) CreateJob(_ context.Context, request JobRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, request)
	id := "j_" + string(rune('0'+len(f.created)))
	f.states[id] = "running"
	return id, nil
}

func (f *fakeJobs) JobState(_ context.Context, id string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.states[id]
	return state, state == "done" || state == "failed" || state == "cancelled", nil
}

type harness struct {
	dir, userDir string
	db           *store.Store
	journal      *journal.Journal
	jobs         *fakeJobs
	now          time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{dir: t.TempDir(), userDir: t.TempDir(), db: db, jobs: &fakeJobs{states: map[string]string{}},
		now: time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)}
	h.journal = journal.New(db, nil, nil, func() time.Time { return h.now })
	return h
}

// service is a fresh Service over the harness's database: a new one is a crowsnest restart.
func (h *harness) service() *Service {
	return New(Options{
		KV: h.db, Jobs: h.jobs, Journal: h.journal, Location: time.UTC, Now: func() time.Time { return h.now },
		Sources: []Source{{Dir: h.dir, Origin: OriginDir}, {Dir: h.userDir, Origin: OriginUser}},
		Picture: func(context.Context) []string { return []string{"agent a idle"} },
	})
}

func (h *harness) write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(due []DueTask) []string {
	var out []string
	for _, task := range due {
		out = append(out, task.Name)
	}
	return out
}

func TestDueDeliveredAndRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "check.md", "---\nevery: 15m\n---\n# Anything stuck?\n")
	h.write(t, h.userDir, "tidy.md", "---\nevery: poke\n---\nTidy.\n")
	service := h.service()
	due := service.DueTasks(ctx, h.now)
	if len(due) != 2 || due[0].Name != "check" || due[0].RideAlong || !due[1].RideAlong {
		t.Fatalf("due = %+v", due)
	}
	if !strings.Contains(due[0].Text, filepath.Join(h.dir, "check.md")) || !strings.Contains(due[0].Text, "never run") {
		t.Fatalf("reason text does not name the file: %q", due[0].Text)
	}
	// Not delivered (the gate held the poke): still due at the next check.
	if got := names(service.DueTasks(ctx, h.now.Add(time.Minute))); len(got) != 2 {
		t.Fatalf("held task dropped: %v", got)
	}
	service.Delivered(ctx, h.now, []string{"check"})
	h.now = h.now.Add(10 * time.Minute)
	restarted := h.service() // the stamps live in kv
	if got := names(restarted.DueTasks(ctx, h.now)); len(got) != 1 || got[0] != "tidy" {
		t.Fatalf("after delivery and restart, due = %v, want only the ride-along", got)
	}
	h.now = h.now.Add(5 * time.Minute)
	if got := names(restarted.DueTasks(ctx, h.now)); len(got) != 2 {
		t.Fatalf("15 minutes later check is due again, got %v", got)
	}
	view := restarted.List(ctx)
	if len(view.Tasks) != 2 || view.Tasks[0].LastRunAt == nil || view.Tasks[0].LastOutcome != "carried by a poke" {
		t.Fatalf("view = %+v", view.Tasks)
	}
}

func TestDisabledAndRunNow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "check.md", "---\nevery: 1d\nafter: 23:00\n---\nCheck.\n")
	service := h.service()
	if due := service.DueTasks(ctx, h.now); len(due) != 0 {
		t.Fatalf("before after:, due = %+v", due)
	}
	kicked := false
	service.opts.Kick = func() { kicked = true }
	if err := service.SetEnabled(ctx, "check", false); err != nil {
		t.Fatal(err)
	}
	if err := service.RunNow(ctx, "check"); err != nil || !kicked {
		t.Fatalf("run now: %v kicked=%v", err, kicked)
	}
	if due := service.DueTasks(ctx, h.now); len(due) != 1 {
		t.Fatalf("run now of a disabled task is due once, got %+v", due)
	}
	service.Delivered(ctx, h.now, []string{"check"})
	h.now = h.now.Add(30 * time.Hour)
	if due := h.service().DueTasks(ctx, h.now); len(due) != 0 {
		t.Fatalf("a disabled task stays off after a restart, got %+v", due)
	}
	if err := service.RunNow(ctx, "nope"); err != ErrNoTask {
		t.Fatalf("unknown task: %v", err)
	}
}

func TestBadFilesReportedOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "bad.md", "---\nevery: fortnightly\n---\n")
	h.write(t, h.dir, "good.md", "---\nevery: 15m\n---\nok\n")
	h.write(t, h.userDir, "good.md", "---\nevery: 15m\n---\nshadowed\n")
	h.write(t, h.dir, "Not A Task.md", "---\nevery: 15m\n---\n")
	for range 3 {
		if got := names(h.service().DueTasks(ctx, h.now)); len(got) != 1 || got[0] != "good" {
			t.Fatalf("due = %v", got)
		}
	}
	page, err := h.journal.List(ctx, journal.Query{Kinds: []journal.Kind{journal.KindAlert}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("want 3 problems journalled once each across restarts, got %d: %+v", len(page.Entries), page.Entries)
	}
	view := h.service().List(ctx)
	if len(view.Problems) != 3 || len(view.Tasks) != 1 {
		t.Fatalf("view problems %+v tasks %+v", view.Problems, view.Tasks)
	}
}

func TestJobTasks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "build.md", "---\nevery: 1h\nas: job\ndir: /tmp/repo\nrole: builder\n---\n# Nightly build\nRun it.\n")
	service := h.service()
	if due := service.DueTasks(ctx, h.now); len(due) != 0 {
		t.Fatalf("job tasks are not poke reasons: %+v", due)
	}
	service.Tick(ctx)
	if len(h.jobs.created) != 1 || h.jobs.created[0].Dir != "/tmp/repo" || h.jobs.created[0].Role != "builder" ||
		!strings.Contains(h.jobs.created[0].Brief, "Run it.") || !strings.HasPrefix(h.jobs.created[0].Brief, "Nightly build") {
		t.Fatalf("created = %+v", h.jobs.created)
	}
	service.Tick(ctx) // not due again yet
	h.now = h.now.Add(2 * time.Hour)
	service.Tick(ctx) // due, but j_1 is still running: no second copy
	if len(h.jobs.created) != 1 {
		t.Fatalf("a running job must not be stacked, created %d", len(h.jobs.created))
	}
	if view := service.List(ctx); !strings.Contains(view.Tasks[0].LastOutcome, "still running") || view.Tasks[0].JobState != "running" {
		t.Fatalf("view = %+v", view.Tasks[0])
	}
	h.jobs.states["j_1"] = "done"
	h.service().Tick(ctx) // restart: the job id survives in kv
	if len(h.jobs.created) != 2 {
		t.Fatalf("after the first job finished, a second run starts; created %d", len(h.jobs.created))
	}
	page, _ := h.journal.List(ctx, journal.Query{Kinds: []journal.Kind{journal.KindTask}})
	if len(page.Entries) != 2 {
		t.Fatalf("job starts journalled: %+v", page.Entries)
	}
}

func TestChangeTask(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.write(t, h.dir, "unblock.md", "---\nevery: change\n---\nLook.\n")
	picture := []string{"agent a idle"}
	service := h.service()
	service.opts.Picture = func(context.Context) []string { return picture }
	if got := names(service.DueTasks(ctx, h.now)); len(got) != 1 {
		t.Fatalf("first sight is a change: %v", got)
	}
	service.Delivered(ctx, h.now, []string{"unblock"})
	h.now = h.now.Add(10 * time.Minute)
	if got := service.DueTasks(ctx, h.now); len(got) != 0 {
		t.Fatalf("same picture: %v", got)
	}
	picture = []string{"agent a blocked"}
	if got := names(service.DueTasks(ctx, h.now)); len(got) != 1 {
		t.Fatalf("changed picture: %v", got)
	}
}
