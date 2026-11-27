package digest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

type rig struct {
	db      *store.Store
	now     time.Time
	notices []string
	svc     *Service
}

func newRig(t *testing.T, orchestrator func(ctx context.Context, day, file string) error) *rig {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &rig{db: db, now: time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)}
	r.svc = New(Options{Store: db, Location: time.UTC, Now: func() time.Time { return r.now }, StateDir: t.TempDir(),
		Notice: func(_ context.Context, title, body string) (int64, error) {
			r.notices = append(r.notices, title+"\n"+body)
			return int64(len(r.notices)), nil
		},
		Orchestrator: orchestrator,
		Projects:     func(context.Context) map[int64][2]string { return map[int64][2]string{1: {"Website", "lead-website"}} },
		Spend:        func(context.Context) string { return "$1.20 est." },
	})
	return r
}

func (r *rig) job(t *testing.T, title, state string, settled time.Time, project int64, stuck bool) {
	t.Helper()
	record := store.JobRecord{Title: title, State: state, Role: "builder", Engine: "claude", CreatedAt: r.now.Add(-time.Hour), SettledAt: settled,
		ProjectID: project, Stuck: stuck, VerifiedJSON: "[]", NotVerifiedJSON: "[]"}
	if _, err := r.db.InsertJob(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

func TestFactsAndPlainDigest(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.job(t, "Ship the widget", "done", r.now.Add(-2*time.Hour), 1, false)
	r.job(t, "Yesterday's job", "done", r.now.Add(-30*time.Hour), 0, false)
	r.job(t, "Broken build", "failed", r.now.Add(-time.Hour), 0, false)
	r.job(t, "Stuck on a limit", "running", time.Time{}, 1, true)
	r.job(t, "Next thing", "queued", time.Time{}, 0, false)
	facts, err := r.svc.Facts(ctx, r.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Finished) != 1 || facts.Finished[0].Title != "Ship the widget" || len(facts.Failed) != 1 || len(facts.Stuck) != 1 || len(facts.Queued) != 1 {
		t.Fatalf("facts %+v", facts)
	}
	if len(facts.Projects) != 1 || facts.Projects[0].Finished != 1 || facts.Projects[0].Live != 1 {
		t.Fatalf("projects %+v", facts.Projects)
	}
	result, err := r.svc.Start(ctx)
	if err != nil || result.Source != "crowsnest" {
		t.Fatalf("start %+v %v", result, err)
	}
	body := r.notices[0]
	for _, want := range []string{"Digest 2026-11-26", "Ship the widget", "Broken build", "Stuck on a limit", "Website (lead lead-website): 1 finished", "$1.20 est.", "Next thing"} {
		if !strings.Contains(body, want) {
			t.Errorf("digest lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Yesterday's job") {
		t.Error("yesterday's job is in today's digest")
	}
	list, _ := r.svc.List(ctx, 0)
	if len(list) != 1 || list[0].Source != "crowsnest" || list[0].QuestionID != 1 {
		t.Fatalf("history %+v", list)
	}
}

func TestOrchestratorWritesItOrCrowsnestFallsBack(t *testing.T) {
	var asked string
	r := newRig(t, func(_ context.Context, _ string, file string) error { asked = file; return nil })
	ctx := context.Background()
	if result, _ := r.svc.Start(ctx); result.Source != "orchestrator" || asked == "" {
		t.Fatalf("not handed to the orchestrator: %+v %q", result, asked)
	}
	if err := r.svc.Submit(ctx, "2026-11-26", "Today: the widget shipped."); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Submit(ctx, "2026-11-26", "again"); err == nil {
		t.Fatal("a second submission must be refused")
	}
	if len(r.notices) != 1 || !strings.Contains(r.notices[0], "the widget shipped") {
		t.Fatalf("notices %v", r.notices)
	}
	// Asked again and never delivered: crowsnest's own after the deadline.
	r.now = r.now.Add(24 * time.Hour)
	_, _ = r.svc.Start(ctx)
	r.now = r.now.Add(DeliverWithin)
	r.svc.Check(ctx)
	if len(r.notices) != 2 || !strings.Contains(r.notices[1], "did not deliver a digest in time") {
		t.Fatalf("fallback %v", r.notices)
	}
	// The orchestrator off: crowsnest at once.
	off := newRig(t, func(context.Context, string, string) error { return errors.New("off") })
	if result, _ := off.svc.Start(ctx); result.Source != "crowsnest" || len(off.notices) != 1 {
		t.Fatalf("orchestrator off: %+v", result)
	}
}

func TestScheduleOncePerDayAndPaused(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	paused := true
	r.svc.opts.Paused = func() bool { return paused }
	r.svc.Check(ctx)
	if len(r.notices) != 0 {
		t.Fatal("ran while off")
	}
	if _, err := r.svc.SaveSettings(ctx, Settings{Enabled: true, At: "25:00"}); err == nil {
		t.Fatal("a bad time must be refused")
	}
	if _, err := r.svc.SaveSettings(ctx, Settings{Enabled: true, At: "17:30"}); err != nil {
		t.Fatal(err)
	}
	r.svc.Check(ctx)
	if len(r.notices) != 0 {
		t.Fatal("ran while paused")
	}
	paused = false
	r.now = time.Date(2026, 11, 26, 17, 0, 0, 0, time.UTC)
	r.svc.Check(ctx)
	if len(r.notices) != 0 {
		t.Fatal("ran before its time")
	}
	r.now = time.Date(2026, 11, 26, 17, 31, 0, 0, time.UTC)
	r.svc.Check(ctx)
	r.svc.Check(ctx)
	if len(r.notices) != 1 {
		t.Fatalf("ran %d times", len(r.notices))
	}
}
