// Tests for what is pushed: the notices built from each event, cleaning, the kind switches, the
// rate limits, the dialog hold and the per-job dedupe.

package push

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
)

type recordingSender struct {
	mu       sync.Mutex
	disabled map[Kind]bool
	notices  []Notice
}

func (r *recordingSender) Enabled(_ context.Context, kind Kind) bool { return !r.disabled[kind] }

func (r *recordingSender) Deliver(_ context.Context, notice Notice, _ string) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, notice.Clean())
	return Result{Sent: 1}, nil
}

func (r *recordingSender) got() []Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notice(nil), r.notices...)
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newTestWatcher() (*Watcher, *recordingSender, *fakeClock) {
	sender := &recordingSender{disabled: map[Kind]bool{}}
	clock := &fakeClock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
	return NewWatcher(WatcherOptions{Sender: sender, Now: clock.Now, Synchronous: true}), sender, clock
}

func TestQuestionNotices(t *testing.T) {
	watcher, sender, _ := newTestWatcher()
	watcher.QuestionAsked(questions.Question{ID: "q_1", Title: "Ship it?", Urgency: questions.UrgencyBlocking, SourcePane: "w1:p1"})
	watcher.QuestionAsked(questions.Question{ID: "q_2", Title: "Which colour?", Urgency: "normal", SourcePane: "w1:p2"})
	watcher.QuestionAsked(questions.Question{ID: "q_3", Title: "FYI", Urgency: "low", SourcePane: "w1:p3"})
	got := sender.got()
	if len(got) != 2 {
		t.Fatalf("low questions are not pushed; got %+v", got)
	}
	if got[0].Urgency != UrgencyHigh || got[0].URL != "/inbox?q=q_1" || got[0].Tag != "q_1" || got[0].Body != "Ship it?" {
		t.Fatalf("blocking: %+v", got[0])
	}
	if got[1].Urgency != UrgencyNormal || got[1].Title != "An agent asks" {
		t.Fatalf("normal: %+v", got[1])
	}
}

func TestNoticeCleaningRedactsCapsAndKeepsLinksInTheApp(t *testing.T) {
	notice := Notice{
		Title: "line one\nline two \x1b[31mred", Body: "token cnt_" + strings.Repeat("a1", 20) + " " + strings.Repeat("x", 400),
		URL: "https://evil.example/phish", Tag: "q_1",
	}.Clean()
	if strings.ContainsAny(notice.Title, "\n\x1b") {
		t.Fatalf("title not cleaned: %q", notice.Title)
	}
	if strings.Contains(notice.Body, strings.Repeat("a1", 20)) {
		t.Fatalf("token not redacted: %q", notice.Body)
	}
	if len([]rune(notice.Body)) > maxBodyRunes {
		t.Fatalf("body not capped: %d runes", len([]rune(notice.Body)))
	}
	for _, bad := range []string{"https://evil.example/", "//evil.example/", "/\\evil", "javascript:alert(1)"} {
		if got := (Notice{URL: bad}).Clean().URL; got != "/" {
			t.Errorf("URL %q became %q, want /", bad, got)
		}
	}
}

func TestJobNotices(t *testing.T) {
	cases := []struct {
		state  string
		cause  string
		stuck  bool
		kind   Kind
		reason string
	}{
		{"failed", "", false, KindJob, "failed"},
		{"done", "", false, KindDone, "done"},
		{"running", "limit", true, KindJob, "stuck:limit"},
		{"running", "unresponsive", true, KindJob, "stuck:unresponsive"},
		{"needs_you", "auth", false, KindJob, "needs:auth"},
		{"running", "exited", true, "", ""},       // restarting by itself
		{"needs_you", "questions", false, "", ""}, // the question is pushed instead
		{"needs_you", "dialog", false, "", ""},    // the dialog is pushed instead
		{"running", "", false, "", ""},
		{"cancelled", "", false, "", ""}, // Jeff did that himself
	}
	for _, c := range cases {
		notice, reason, ok := JobNotice("j_4", "Fix the build", c.state, c.cause, c.stuck)
		if ok != (c.kind != "") || notice.Kind != c.kind || reason != c.reason {
			t.Errorf("%s/%s/%v: %+v %q %v", c.state, c.cause, c.stuck, notice, reason, ok)
		}
		if ok && (notice.URL != "/jobs?job=j_4" || notice.Tag != "job:j_4") {
			t.Errorf("deep link/tag: %+v", notice)
		}
	}
}

func TestJobEventsArePushedOncePerReason(t *testing.T) {
	watcher, sender, _ := newTestWatcher()
	ctx := context.Background()
	stuck := jobs.HubEvent{ID: "j_1", State: jobs.StateRunning, Stuck: true, Cause: jobs.CauseLimit, Title: "x"}
	watcher.HandleEvent(ctx, hub.Event{Type: "job", Data: stuck})
	watcher.HandleEvent(ctx, hub.Event{Type: "job", Data: stuck}) // a timeline event, same picture
	watcher.HandleEvent(ctx, hub.Event{Type: "job", Data: jobs.HubEvent{ID: "j_1", State: jobs.StateRunning, Title: "x"}})
	watcher.HandleEvent(ctx, hub.Event{Type: "job", Data: stuck}) // stuck again after recovering
	watcher.HandleEvent(ctx, hub.Event{Type: "job", Data: jobs.HubEvent{ID: "j_1", State: jobs.StateFailed, Title: "x"}})
	if got := sender.got(); len(got) != 3 {
		t.Fatalf("want stuck, stuck again, failed; got %+v", got)
	}
}

func TestKindSwitchesAreHonoured(t *testing.T) {
	watcher, sender, _ := newTestWatcher()
	sender.disabled[KindDone] = true
	watcher.HandleEvent(context.Background(), hub.Event{Type: "job", Data: jobs.HubEvent{ID: "j_2", State: jobs.StateDone, Title: "x"}})
	if len(sender.got()) != 0 {
		t.Fatal("done is switched off")
	}
}

func TestRateLimitsPerSourceAndOverall(t *testing.T) {
	watcher, sender, clock := newTestWatcher()
	for i := range 6 {
		watcher.QuestionAsked(questions.Question{ID: "q_" + string(rune('a'+i)), Title: "t", Urgency: "blocking", SourcePane: "w1:p1"})
	}
	if got := len(sender.got()); got != perSourceBurst {
		t.Fatalf("one asker: %d pushed, want %d", got, perSourceBurst)
	}
	for i := range 20 {
		watcher.QuestionAsked(questions.Question{ID: "q_x", Title: "t", Urgency: "blocking", SourcePane: "pane-" + string(rune('a'+i))})
	}
	if got := len(sender.got()); got != overallBurst {
		t.Fatalf("many askers: %d pushed, want the overall cap %d", got, overallBurst)
	}
	clock.now = clock.now.Add(time.Minute)
	watcher.QuestionAsked(questions.Question{ID: "q_y", Title: "t", Urgency: "blocking", SourcePane: "fresh"})
	if got := len(sender.got()); got != overallBurst+1 {
		t.Fatalf("after a minute one more goes: %d", got)
	}
}

func TestDialogsAreHeldThenPushedOnceUntilCleared(t *testing.T) {
	watcher, sender, clock := newTestWatcher()
	ctx := context.Background()
	atDialog := fleet.Fleet{Agents: []fleet.AgentEntry{{Agent: fleet.Agent{Name: "job-3"}, PaneID: "w2:p1", Workspace: "crowsnest", NeedsYou: true}}}
	watcher.HandleEvent(ctx, hub.Event{Type: "fleet", Data: atDialog})
	clock.now = clock.now.Add(dialogHold - time.Second)
	watcher.CheckDialogs(ctx)
	if len(sender.got()) != 0 {
		t.Fatal("a dialog answered within the hold is not pushed")
	}
	clock.now = clock.now.Add(2 * time.Second)
	watcher.CheckDialogs(ctx)
	watcher.CheckDialogs(ctx)
	got := sender.got()
	if len(got) != 1 || got[0].URL != "/inbox?q=dialog%3Aw2%3Ap1" || got[0].Kind != KindDialog || !strings.Contains(got[0].Body, "job-3") {
		t.Fatalf("got %+v", got)
	}
	// Cleared, then a new dialog in the same pane: pushed again after the hold.
	watcher.HandleEvent(ctx, hub.Event{Type: "fleet", Data: fleet.Fleet{}})
	watcher.HandleEvent(ctx, hub.Event{Type: "fleet", Data: atDialog})
	clock.now = clock.now.Add(dialogHold)
	watcher.CheckDialogs(ctx)
	if len(sender.got()) != 2 {
		t.Fatalf("a new dialog must be pushed again: %+v", sender.got())
	}
	// A fleet read that failed (Error set) changes nothing.
	watcher.HandleEvent(ctx, hub.Event{Type: "fleet", Data: fleet.Fleet{Error: "herdr is not answering"}})
	clock.now = clock.now.Add(dialogHold)
	watcher.CheckDialogs(ctx)
	if len(sender.got()) != 2 {
		t.Fatal("an unreadable fleet must not clear or re-push dialogs")
	}
}

func TestOrchestratorNotices(t *testing.T) {
	watcher, sender, _ := newTestWatcher()
	ctx := context.Background()
	for _, state := range []orch.State{orch.StateStarting, orch.StateNeedsTerminal, orch.StateNeedsTerminal, orch.StateIdle, orch.StateFailed} {
		watcher.HandleEvent(ctx, hub.Event{Type: "orchestrator", Data: orch.Status{State: state}})
	}
	got := sender.got()
	if len(got) != 2 || got[0].Title != "The orchestrator needs the terminal" || got[1].Title != "The orchestrator stopped" {
		t.Fatalf("got %+v", got)
	}
}

// Run resubscribes when the hub closes it (a subscriber that fell behind) and keeps pushing.
func TestRunSurvivesASubscriberOverflow(t *testing.T) {
	events := hub.New()
	defer events.Close()
	sender := &recordingSender{disabled: map[Kind]bool{}}
	watcher := NewWatcher(WatcherOptions{Sender: sender, Hub: events, Synchronous: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)
	waitFor(t, func() bool { return events.Subscribers() == 1 })
	for range hub.DefaultBuffer * 3 { // overflow the queue so the hub closes the subscriber
		events.Publish(hub.Event{Type: "noise"})
	}
	waitFor(t, func() bool { return events.Subscribers() == 1 })
	events.Publish(hub.Event{Type: "job", Data: jobs.HubEvent{ID: "j_9", State: jobs.StateFailed, Title: "x"}})
	waitFor(t, func() bool { return len(sender.got()) == 1 })
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// deviceless reports no subscribed device.
type deviceless struct{ recordingSender }

func (d *deviceless) HasDevices(context.Context) bool { return false }

// A notice nobody would receive spends no rate budget: a burst before the first device
// subscribes must not hold back the first notice that device should get.
func TestWatcherSpendsNoBudgetWithoutDevices(t *testing.T) {
	quiet := &deviceless{recordingSender: recordingSender{disabled: map[Kind]bool{}}}
	watcher := NewWatcher(WatcherOptions{Sender: quiet, Synchronous: true})
	for i := 0; i < 30; i++ {
		watcher.notify(context.Background(), Notice{Kind: KindQuestion, Tag: "q:" + strconv.Itoa(i), Source: "pane"})
	}
	if got := len(quiet.got()); got != 0 {
		t.Fatalf("pushed %d notices to no device", got)
	}
	if ok, why := watcher.gate.allow("pane"); !ok {
		t.Fatalf("the budget was spent without a device: %s", why)
	}
}
