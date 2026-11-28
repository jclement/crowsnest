package attention

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) GetKV(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, ok := k.m[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (k *memKV) SetKV(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

// world is a mutable set of sources and a clock the tests move.
type world struct {
	mu        sync.Mutex
	now       time.Time
	approvals []approvals.Approval
	questions []questions.Question
	jobs      []jobs.Job
}

func (w *world) clock() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.now }

func (w *world) advance(d time.Duration) { w.mu.Lock(); w.now = w.now.Add(d); w.mu.Unlock() }

func newTestService(w *world, events *hub.Hub) *Service {
	return New(Options{
		Approvals: func(context.Context) ([]approvals.Approval, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.approvals, nil
		},
		Questions: func(context.Context) ([]questions.Question, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.questions, nil
		},
		Jobs:     func(context.Context) ([]jobs.Job, error) { w.mu.Lock(); defer w.mu.Unlock(); return w.jobs, nil },
		Projects: func(context.Context) ([]projects.Project, error) { return []projects.Project{crow, site}, nil },
		KV:       &memKV{m: map[string]string{}},
		Hub:      events,
		Now:      w.clock,
	})
}

// A change in a source reaches subscribers as one `attention` event with the whole snapshot,
// and an unchanged rebuild publishes nothing.
func TestServicePublishesOnChange(t *testing.T) {
	w := &world{now: testNow}
	events := hub.New()
	sub := events.Subscribe(16)
	defer sub.Close()
	service := newTestService(w, events)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)

	first := next(t, sub)
	if first.Counts.Total != 0 || first.Revision != 1 {
		t.Fatalf("first snapshot %+v", first)
	}
	w.mu.Lock()
	w.approvals = []approvals.Approval{approval("ap_1", "w1:p1", "", risk.Low, 0)}
	w.mu.Unlock()
	events.Publish(hub.Event{Type: "approval", Data: nil})
	second := next(t, sub)
	if second.Counts.Total != 1 || second.Counts.Approval != 1 || second.Revision != 2 || second.Items[0].ID != "approval:ap_1" {
		t.Fatalf("second snapshot %+v", second)
	}
	events.Publish(hub.Event{Type: "fleet", Data: nil}) // nothing changed: no event
	select {
	case event := <-sub.Events():
		if event.Type == EventType {
			t.Fatalf("an unchanged queue was published again: %+v", event.Data)
		}
	case <-time.After(3 * debounce):
	}
}

func next(t *testing.T, sub *hub.Subscription) Snapshot {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-sub.Events():
			if snapshot, ok := event.Data.(Snapshot); ok && event.Type == EventType {
				return snapshot
			}
		case <-deadline:
			t.Fatal("no attention event")
		}
	}
}

// A snooze is the server's: every caller sees it, it leaves the count, and it runs out.
func TestServiceSnoozeIsSharedAndExpires(t *testing.T) {
	w := &world{now: testNow, questions: []questions.Question{question("q_1", "w1:p1", "", questions.UrgencyNormal, time.Minute)}}
	service := newTestService(w, nil)
	ctx := context.Background()
	snapshot, err := service.Snooze(ctx, "question:q_1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Counts.Total != 0 || snapshot.Counts.Snoozed != 1 || snapshot.Items[0].SnoozedUntil == nil {
		t.Fatalf("after snooze %+v", snapshot.Counts)
	}
	if again := service.Snapshot(ctx); again.Counts.Total != 0 { // "another device" reads the same
		t.Fatalf("snooze not kept: %+v", again.Counts)
	}
	w.advance(SnoozeFor + time.Second)
	if later := service.Snapshot(ctx); later.Counts.Total != 1 || later.Items[0].SnoozedUntil != nil {
		t.Fatalf("snooze did not run out: %+v", later.Counts)
	}
	if _, err := service.Snooze(ctx, "question:q_404", time.Time{}); !errors.Is(err, ErrUnknownItem) {
		t.Fatalf("snoozing an unknown item: %v", err)
	}
}

// Only a failed job can be dismissed.
func TestServiceDismiss(t *testing.T) {
	failed := job("j_2", 2, "", jobs.StateFailed, false, "")
	failed.SettledAt = agoPtr(time.Hour)
	w := &world{now: testNow, jobs: []jobs.Job{failed}, questions: []questions.Question{question("q_1", "w1:p1", "", questions.UrgencyNormal, time.Minute)}}
	service := newTestService(w, nil)
	ctx := context.Background()
	if _, err := service.Dismiss(ctx, "question:q_1"); !errors.Is(err, ErrNotDismissable) {
		t.Fatalf("dismissing a question: %v", err)
	}
	snapshot, err := service.Dismiss(ctx, "job:j_2")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Counts.Total != 1 || snapshot.Items[0].ID != "question:q_1" {
		t.Fatalf("after dismiss %+v", ids(snapshot.Items))
	}
}

// Each browser has its own "since": a return after AwayAfter opens a window from when it last
// was active; a short gap does not; mark-seen empties it; another browser is unaffected.
func TestSincePerBrowser(t *testing.T) {
	w := &world{now: testNow}
	service := newTestService(w, nil)
	ctx := context.Background()
	phone, desk := "phone-abcdef12", "desk-abcdef12"
	settled := func(id string, state jobs.State, at time.Time) jobs.Job {
		j := job(id, 1, "", state, false, "")
		j.SettledAt = &at
		return j
	}
	if err := service.Beat(ctx, phone); err != nil {
		t.Fatal(err)
	}
	if err := service.Beat(ctx, desk); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSeen(ctx, phone); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSeen(ctx, desk); err != nil {
		t.Fatal(err)
	}
	// The phone leaves for an hour; the desk keeps beating.
	w.advance(30 * time.Minute)
	doneAt := w.clock()
	w.advance(30 * time.Minute)
	_ = service.Beat(ctx, desk)
	src := SinceSources{Jobs: []jobs.Job{settled("j_1", jobs.StateDone, doneAt), settled("j_2", jobs.StateFailed, doneAt.Add(time.Minute))},
		Projects: map[int64]ProjectRef{1: {ID: 1, Slug: "crowsnest", Name: "Crowsnest"}}}
	_ = service.Beat(ctx, phone) // back after an hour: a new window from when it left
	got, err := service.SinceFor(ctx, phone, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 || got.Events[0].JobID != "j_2" || got.Counts["done"] != 1 || got.Events[0].Project == nil {
		t.Fatalf("phone since %+v", got)
	}
	desktop, _ := service.SinceFor(ctx, desk, src) // the desk saw them happen (marked seen, then beating)
	if len(desktop.Events) != 2 {
		// The desk's window is still from its mark: events after it are listed until marked.
		t.Fatalf("desk since %+v", desktop)
	}
	_ = service.MarkSeen(ctx, desk)
	if cleared, _ := service.SinceFor(ctx, desk, src); len(cleared.Events) != 0 {
		t.Fatalf("mark seen did not clear the desk: %+v", cleared)
	}
	if still, _ := service.SinceFor(ctx, phone, src); len(still.Events) != 2 {
		t.Fatalf("the desk's mark cleared the phone: %+v", still)
	}
	w.advance(time.Minute)
	_ = service.Beat(ctx, phone) // a short gap is not leaving
	if same, _ := service.SinceFor(ctx, phone, src); len(same.Events) != 2 {
		t.Fatalf("a short gap reset the window: %+v", same)
	}
	if err := service.Beat(ctx, "bad id!"); !errors.Is(err, ErrBadBrowser) {
		t.Fatalf("bad id: %v", err)
	}
}

// The Now rows: waiting is the queue's own count, owners follow the lead's state, and running,
// done today and spend are per project.
func TestRows(t *testing.T) {
	doneToday, doneYesterday := ago(time.Hour), ago(30*time.Hour)
	running := job("j_1", 1, "w1:p1", jobs.StateRunning, false, "")
	running.DoingNow = "Writing tests"
	finished := job("j_2", 1, "", jobs.StateDone, false, "")
	finished.SettledAt = &doneToday
	old := job("j_3", 1, "", jobs.StateDone, false, "")
	old.SettledAt = &doneYesterday
	stuck := job("j_4", 2, "w2:p1", jobs.StateRunning, true, jobs.CauseLimit)
	// R10 Opus B4: a job waiting for a slot is queued, not running.
	queued := job("j_5", 1, "", jobs.StateQueued, false, "")
	rows := Rows(OverviewSources{
		Projects: []projects.Project{site, crow},
		Jobs:     []jobs.Job{running, finished, old, stuck, queued},
		Standing: []StandingAgent{{Name: "lead-crowsnest", State: "idle", ProjectID: 1}, {Name: "orchestrator", State: "working"}},
		Spend:    map[string]float64{"j_1": 1.5, "lead:crowsnest": 0.5, "orchestrator": 2},
		Waiting:  map[string]int{"2": 1},
		Now:      testNow, Location: time.UTC,
	})
	if len(rows) != 4 || rows[0].Project.Slug != "crowsnest" || rows[1].Project.Slug != "website" || rows[2].Kind != "unfiled" || rows[3].Kind != "orchestrator" {
		t.Fatalf("rows %+v", rows)
	}
	crowRow, siteRow := rows[0], rows[1]
	if crowRow.Owner != OwnerLead || siteRow.Owner != OwnerNoLead || siteRow.Lead.State != "off" {
		t.Errorf("owners %q / %q", crowRow.Owner, siteRow.Owner)
	}
	if crowRow.Counts.Running != 1 || crowRow.Counts.Queued != 1 || crowRow.Counts.DoneToday != 1 || crowRow.Counts.Waiting != 0 || *crowRow.SpendTodayUSD != 2 {
		t.Errorf("crowsnest row %+v spend %v", crowRow.Counts, *crowRow.SpendTodayUSD)
	}
	if crowRow.Doing != "j_1 Job j_1: Writing tests" {
		t.Errorf("doing %q", crowRow.Doing)
	}
	if siteRow.Counts.Waiting != 1 || siteRow.Counts.Running != 0 || siteRow.Doing != "Nothing moving: waiting on you" {
		t.Errorf("website row %+v %q", siteRow.Counts, siteRow.Doing)
	}
	if rows[3].SpendTodayUSD == nil || *rows[3].SpendTodayUSD != 2 {
		t.Errorf("orchestrator spend %+v", rows[3].SpendTodayUSD)
	}
}
