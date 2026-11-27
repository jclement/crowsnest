package questions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

// fakeClock is a settable clock for expiry and grace tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	service *Service
	store   *store.Store
	clock   *fakeClock
	hub     *hub.Hub
	settled chan Question
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{store: db, clock: &fakeClock{now: time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)}, hub: hub.New(), settled: make(chan Question, 16)}
	f.service = New(Options{Store: db, Hub: f.hub, Now: f.clock.Now, OnSettled: func(q Question) { f.settled <- q }})
	return f
}

var asker = Caller{PaneID: "w1:p1", JobID: "j_3"}

func confirm(title string) AskInput {
	return AskInput{Title: title, Kind: KindConfirm, Recommend: "yes"}
}

func (f *fixture) ask(t *testing.T, input AskInput) Question {
	t.Helper()
	question, err := f.service.Ask(context.Background(), input, asker)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	return question
}

func TestValidation(t *testing.T) {
	big := strings.Repeat("x", MaxFileSideBytes+1)
	cases := []struct {
		name    string
		input   AskInput
		wantErr string
	}{
		{"confirm with defaults", AskInput{Title: "Ship?", Kind: KindConfirm, Recommend: "yes"}, ""},
		{"confirm needs recommend", AskInput{Title: "Ship?", Kind: KindConfirm}, "recommend is required"},
		{"choice needs recommend", AskInput{Title: "Pick", Kind: KindChoice, Options: opts("a", "b")}, "recommend is required"},
		{"recommend must be a key", AskInput{Title: "Pick", Kind: KindChoice, Options: opts("a", "b"), Recommend: "c"}, "not one of the option keys"},
		{"choice needs two options", AskInput{Title: "Pick", Kind: KindChoice, Options: opts("a"), Recommend: "a"}, "at least two"},
		{"duplicate keys", AskInput{Title: "Pick", Kind: KindChoice, Options: opts("a", "a"), Recommend: "a"}, "unique"},
		{"bad key", AskInput{Title: "Pick", Kind: KindChoice, Options: []Option{{Key: "a b", Label: "x"}, {Key: "c", Label: "y"}}, Recommend: "c"}, "key"},
		{"label required", AskInput{Title: "Pick", Kind: KindChoice, Options: []Option{{Key: "a"}, {Key: "b", Label: "B"}}, Recommend: "b"}, "needs a label"},
		{"title required", AskInput{Kind: KindAck}, "title is required"},
		{"title one line", AskInput{Title: "a\nb", Kind: KindAck}, "one line"},
		{"title too long", AskInput{Title: strings.Repeat("t", MaxTitleRunes+1), Kind: KindAck}, "longer than"},
		{"body too long", AskInput{Title: "t", Kind: KindAck, BodyMD: strings.Repeat("b", MaxBodyBytes+1)}, "body_md"},
		{"ack takes no options", AskInput{Title: "t", Kind: KindAck, Options: opts("a", "b")}, "takes no options"},
		{"multi only for choice", AskInput{Title: "t", Kind: KindConfirm, Multi: true, Recommend: "yes"}, "multi"},
		{"recommend only for confirm/choice", AskInput{Title: "t", Kind: KindManual, Recommend: "x"}, "recommend is only"},
		{"unknown kind", AskInput{Title: "t", Kind: "poll"}, "kind must be"},
		{"bad urgency", AskInput{Title: "t", Kind: KindAck, Urgency: "now"}, "urgency"},
		{"expiry too long", AskInput{Title: "t", Kind: KindAck, ExpiresInHours: 169}, "expires_in_hours"},
		{"expiry too short (R1b N2)", AskInput{Title: "t", Kind: KindAck, ExpiresInHours: 1e-6}, "five minutes"},
		{"bidi override in title (R1b N3)", AskInput{Title: "Ship \u202eoN", Kind: KindAck}, "bidirectional"},
		{"bidi in a label (R1b N3)", AskInput{Title: "t", Kind: KindChoice, Recommend: "a", Options: []Option{{Key: "a", Label: "\u2067A"}, {Key: "b", Label: "B"}}}, "bidirectional"},
		{"too many files", AskInput{Title: "t", Kind: KindAck, Files: make([]File, MaxFiles+1)}, "at most 20 files"},
		{"file too big", AskInput{Title: "t", Kind: KindAck, Files: []File{{Filename: "a", Final: big}}}, "256 KB"},
		{"file needs a name", AskInput{Title: "t", Kind: KindAck, Files: []File{{Original: "a"}}}, "filename"},
		{"multi choice ok", AskInput{Title: "t", Kind: KindChoice, Multi: true, Options: opts("a", "b", "c"), Recommend: "a"}, ""},
		{"no kind with options is a choice", AskInput{Title: "t", Options: opts("a", "b"), Recommend: "a"}, ""},
		{"no kind no options is an ack", AskInput{Title: "t"}, ""},
		{"too many options", AskInput{Title: "t", Kind: KindChoice, Options: opts("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"), Recommend: "a"}, "at most 12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalise(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !IsValidation(err) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want a validation error containing %q", err, tc.wantErr)
			}
		})
	}
}

func opts(keys ...string) []Option {
	out := make([]Option, 0, len(keys))
	for _, key := range keys {
		out = append(out, Option{Key: key, Label: strings.ToUpper(key)})
	}
	return out
}

func TestAskStoresDefaultsAndCaller(t *testing.T) {
	f := newFixture(t)
	question := f.ask(t, confirm("Ship the migration tonight?"))
	if question.ID != "q_1" || question.Status != StatusOpen || question.SourcePane != "w1:p1" || question.JobID != "j_3" {
		t.Fatalf("question %+v", question)
	}
	if len(question.Options) != 2 || question.Options[0].Key != "yes" || question.Urgency != UrgencyNormal {
		t.Fatalf("defaults not applied: %+v", question)
	}
	if want := f.clock.Now().Add(24 * time.Hour); !question.ExpiresAt.Equal(want) {
		t.Fatalf("expires %v, want %v", question.ExpiresAt, want)
	}
}

func TestAnswerSingleMultiAckManualNotice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	single := f.ask(t, confirm("Ship?"))
	multi := f.ask(t, AskInput{Title: "Which?", Kind: KindChoice, Multi: true, Options: opts("a", "b", "c"), Recommend: "a"})
	ack := f.ask(t, AskInput{Title: "FYI", Kind: KindAck})
	manual := f.ask(t, AskInput{Title: "Say what?", Kind: KindManual})
	notice := f.ask(t, AskInput{Title: "Done", Kind: KindNotice})

	cases := []struct {
		name    string
		id      string
		answer  AnswerInput
		wantErr string
		want    any
	}{
		{"single bad key", single.ID, AnswerInput{Value: json.RawMessage(`"maybe"`)}, "not an option", nil},
		{"single array refused", single.ID, AnswerInput{Value: json.RawMessage(`["yes"]`)}, "pick an option", nil},
		{"single ok", single.ID, AnswerInput{Value: json.RawMessage(`"yes"`), Note: "  after 10pm "}, "", "yes"},
		{"single twice", single.ID, AnswerInput{Value: json.RawMessage(`"no"`)}, "no longer open", nil},
		{"multi empty", multi.ID, AnswerInput{Value: json.RawMessage(`[]`)}, "at least one", nil},
		{"multi repeated", multi.ID, AnswerInput{Value: json.RawMessage(`["a","a"]`)}, "repeated", nil},
		{"multi ok", multi.ID, AnswerInput{Value: json.RawMessage(`["a","c"]`)}, "", []any{"a", "c"}},
		{"ack", ack.ID, AnswerInput{}, "", "ack"},
		{"manual needs a note", manual.ID, AnswerInput{}, "written answer", nil},
		{"manual ok", manual.ID, AnswerInput{Note: "Make it so"}, "", nil},
		{"notice dismissed", notice.ID, AnswerInput{}, "", "dismissed"},
		{"unknown id", "q_99", AnswerInput{}, "no such question", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.service.Answer(ctx, tc.id, tc.answer)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.Status != StatusAnswered {
				t.Fatalf("answer: %+v %v", got, err)
			}
			wantJSON, _ := json.Marshal(tc.want)
			gotJSON, _ := json.Marshal(got.AnswerValue)
			if string(wantJSON) != string(gotJSON) {
				t.Fatalf("value %s, want %s", gotJSON, wantJSON)
			}
		})
	}
	answered, _ := f.service.Get(ctx, single.ID)
	if answered.AnswerNote != "after 10pm" || answered.SettledAt == nil {
		t.Fatalf("note or settled_at not stored: %+v", answered)
	}
}

func TestExpirySweepWithFakeClock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	short := f.ask(t, AskInput{Title: "Soon", Kind: KindAck, ExpiresInHours: 1})
	long := f.ask(t, AskInput{Title: "Later", Kind: KindAck, ExpiresInHours: 48})

	if n, err := f.service.SweepExpired(ctx); err != nil || n != 0 {
		t.Fatalf("nothing should expire yet: %d %v", n, err)
	}
	f.clock.Advance(61 * time.Minute)
	if n, err := f.service.SweepExpired(ctx); err != nil || n != 1 {
		t.Fatalf("one should expire: %d %v", n, err)
	}
	got, _ := f.service.Get(ctx, short.ID)
	if got.Status != StatusExpired || got.AnswerValue != nil {
		t.Fatalf("expired question %+v: expiry must not be an answer", got)
	}
	if _, err := f.service.Answer(ctx, short.ID, AnswerInput{}); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("answering an expired question: %v", err)
	}
	if got, _ := f.service.Get(ctx, long.ID); got.Status != StatusOpen {
		t.Fatalf("long question %s", got.Status)
	}
}

func TestLongPollWakesOnAnswer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	question := f.ask(t, confirm("Ship?"))
	result := make(chan Question, 1)
	go func() {
		got, err := f.service.Wait(ctx, question.ID, 30*time.Second, asker)
		if err != nil {
			t.Errorf("wait: %v", err)
		}
		result <- got
	}()
	waitForWaiter(t, f.service, question.ID)
	answered, err := f.service.Answer(ctx, question.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.Status != StatusAnswered || got.AnswerValue != "yes" {
			t.Fatalf("poll returned %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the long-poll did not wake on the answer")
	}
	if answered.Delivery != DeliveryPoll {
		t.Fatalf("delivery %q: an asker on the long-poll must not also get the answer typed", answered.Delivery)
	}
}

func TestLongPollTimesOutOpenAndHonoursCancel(t *testing.T) {
	f := newFixture(t)
	question := f.ask(t, confirm("Ship?"))
	got, err := f.service.Wait(context.Background(), question.ID, 50*time.Millisecond, asker)
	if err != nil || got.Status != StatusOpen {
		t.Fatalf("timed-out poll: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.service.Wait(ctx, question.ID, 30*time.Second, asker)
		done <- err
	}()
	waitForWaiter(t, f.service, question.ID)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled poll returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the long-poll ignored its context")
	}
}

func TestDeliveryDecision(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	typed := f.ask(t, confirm("Nobody waits"))
	got, _ := f.service.Answer(ctx, typed.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if got.Delivery != DeliveryType {
		t.Fatalf("no waiter: delivery %q, want type", got.Delivery)
	}

	blocking := confirm("Asker blocks")
	blocking.Wait = true
	waited := f.ask(t, blocking)
	got, _ = f.service.Answer(ctx, waited.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if got.Delivery != DeliveryPoll {
		t.Fatalf("wait=true asker within grace: delivery %q, want poll", got.Delivery)
	}

	gone := blocking
	gone.Title = "Asker blocked but went away"
	abandoned := f.ask(t, gone)
	f.clock.Advance(pollGrace + time.Second)
	got, _ = f.service.Answer(ctx, abandoned.ID, AnswerInput{Value: json.RawMessage(`"no"`)})
	if got.Delivery != DeliveryType {
		t.Fatalf("wait=true asker that stopped polling: delivery %q, want type", got.Delivery)
	}

	byAsker := f.ask(t, confirm("Withdrawn"))
	got, _ = f.service.Cancel(ctx, byAsker.ID, true)
	if got.Status != StatusCancelled || got.Delivery != DeliveryNone {
		t.Fatalf("cancelled by asker: %+v", got)
	}
	byHuman := f.ask(t, confirm("Cancelled in the inbox"))
	got, _ = f.service.Cancel(ctx, byHuman.ID, false)
	if got.Delivery != DeliveryType {
		t.Fatalf("cancelled by the human: delivery %q, want type", got.Delivery)
	}

	notice := f.ask(t, AskInput{Title: "FYI", Kind: KindNotice})
	got, _ = f.service.Answer(ctx, notice.ID, AnswerInput{})
	if got.Delivery != DeliveryNone {
		t.Fatalf("notice: delivery %q, want none", got.Delivery)
	}
	noPane, _ := f.service.Ask(ctx, confirm("From a script"), Caller{})
	got, _ = f.service.Answer(ctx, noPane.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if got.Delivery != DeliveryNone {
		t.Fatalf("no pane: delivery %q, want none", got.Delivery)
	}
}

func TestListSummaryAndEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.hub.Subscribe(16)
	defer sub.Close()
	f.ask(t, AskInput{Title: "Diff", Kind: KindAck, Urgency: UrgencyBlocking, Files: []File{{Filename: "a.go", Original: "a", Final: "b"}}})
	second := f.ask(t, confirm("Second"))
	if _, err := f.service.Answer(ctx, second.ID, AnswerInput{Value: json.RawMessage(`"no"`)}); err != nil {
		t.Fatal(err)
	}
	open, err := f.service.List(ctx, "open")
	if err != nil || len(open) != 1 || open[0].Files != nil || open[0].FileCount != 1 {
		t.Fatalf("open list %+v %v (files must be left out of lists)", open, err)
	}
	all, _ := f.service.List(ctx, "all")
	if len(all) != 2 || all[0].Status != StatusOpen {
		t.Fatalf("all list must put open first: %+v", all)
	}
	if _, err := f.service.List(ctx, "bogus"); !IsValidation(err) {
		t.Fatalf("bad status filter: %v", err)
	}
	summary, _ := f.service.Summary(ctx)
	if summary.Open != 1 || summary.Blocking != 1 {
		t.Fatalf("summary %+v", summary)
	}
	var changes []string
	for len(changes) < 3 {
		select {
		case event := <-sub.Events():
			if event.Type == "question" {
				changes = append(changes, event.Data.(Event).Change)
			}
		case <-time.After(time.Second):
			t.Fatalf("events so far %v", changes)
		}
	}
	if strings.Join(changes, ",") != "asked,asked,answered" {
		t.Fatalf("events %v", changes)
	}
}

func TestParseID(t *testing.T) {
	for text, want := range map[string]int64{"q_7": 7, "7": 7, "q_0": 0, "q_7x": 0, "x": 0, "-1": 0, "q_07": 0} {
		got, err := ParseID(text)
		if (want == 0) != (err != nil) || got != want {
			t.Errorf("ParseID(%q) = %d, %v", text, got, err)
		}
	}
}

// TestOnlyTheAskerCountsAsWaiting is part of R1b N2: another caller polling a question must not
// turn its delivery into "poll" (and so keep it from being typed into the asker's pane).
func TestOnlyTheAskerCountsAsWaiting(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	question := f.ask(t, confirm("Ship?"))
	go func() { _, _ = f.service.Wait(ctx, question.ID, 30*time.Second, Caller{PaneID: "w9:p9"}) }()
	go func() { _, _ = f.service.Wait(ctx, question.ID, 30*time.Second, Caller{}) }()
	waitForPollers(t, f.service, question.ID, 2)
	got, _ := f.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if got.Delivery != DeliveryType {
		t.Fatalf("delivery %q: a poll by someone else must not stand in for the asker", got.Delivery)
	}
}

// TestCancelledPollIsNotAReturningReader is R1b-C06: a poll cut off by its caller (a cancelled
// tool call) does not leave a grace period in which the answer would go to a poll nobody makes.
func TestCancelledPollIsNotAReturningReader(t *testing.T) {
	f := newFixture(t)
	question := f.ask(t, confirm("Ship?"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _, _ = f.service.Wait(ctx, question.ID, 30*time.Second, asker); close(done) }()
	waitForWaiter(t, f.service, question.ID)
	cancel()
	<-done
	f.clock.Advance(5 * time.Second) // well inside pollGrace
	got, _ := f.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if got.Delivery != DeliveryType {
		t.Fatalf("delivery %q after the asker's poll was cancelled; the answer would never arrive", got.Delivery)
	}
}

// TestUncollectedPollAnswerIsTyped is R1b-C06's other half: the asker was polling (so the answer
// went to the poll) but never collected it; after collectGrace it is typed instead. A collected
// one is not.
func TestUncollectedPollAnswerIsTyped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	waiting := confirm("Approve the deploy?")
	waiting.Wait = true
	lost := f.ask(t, waiting)
	kept := f.ask(t, waiting)
	lostAnswer, _ := f.service.Answer(ctx, lost.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	keptAnswer, _ := f.service.Answer(ctx, kept.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if lostAnswer.Delivery != DeliveryPoll || keptAnswer.Delivery != DeliveryPoll {
		t.Fatalf("deliveries %q %q, want poll", lostAnswer.Delivery, keptAnswer.Delivery)
	}
	f.service.Collected(ctx, keptAnswer, Caller{PaneID: "w9:p9"}) // not the asker: ignored
	f.service.Collected(ctx, keptAnswer, asker)
	if n, _ := f.service.RetypeUncollected(ctx); n != 0 {
		t.Fatalf("%d answers re-routed inside the grace period", n)
	}
	f.clock.Advance(collectGrace + time.Second)
	if n, err := f.service.RetypeUncollected(ctx); err != nil || n != 1 {
		t.Fatalf("re-routed %d (%v), want the one nobody collected", n, err)
	}
	if got, _ := f.service.Get(ctx, lost.ID); got.Delivery != DeliveryType {
		t.Fatalf("uncollected answer delivery %q, want type", got.Delivery)
	}
	if got, _ := f.service.Get(ctx, kept.ID); got.Delivery != DeliveryPoll || got.DeliveredAt == nil {
		t.Fatalf("collected answer: %+v", got)
	}
}

// TestWaiterEntriesDoNotLeak is R1b-C07: polls on missing and settled questions, and expired
// grace periods, leave nothing behind.
func TestWaiterEntriesDoNotLeak(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := range 20 {
		if _, err := f.service.Wait(ctx, FormatID(int64(1000+i)), 90*time.Second, asker); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing question: %v", err)
		}
	}
	settled := f.ask(t, confirm("Done already"))
	_, _ = f.service.Answer(ctx, settled.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	if _, err := f.service.Wait(ctx, settled.ID, 90*time.Second, asker); err != nil {
		t.Fatal(err)
	}
	open := f.ask(t, confirm("Still open"))
	if _, err := f.service.Wait(ctx, open.ID, time.Millisecond, asker); err != nil {
		t.Fatal(err)
	}
	waiting := confirm("Asked with wait")
	waiting.Wait = true
	f.ask(t, waiting)
	f.clock.Advance(pollGrace + time.Second)
	f.service.mu.Lock()
	f.service.pruneWaitersLocked()
	left := len(f.service.waiters)
	f.service.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d waiter entries left behind", left)
	}
}

// waitForPollers blocks until n long-polls on id are registered.
func waitForPollers(t *testing.T, service *Service, id string, n int) {
	t.Helper()
	numeric, _ := ParseID(id)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		state, ok := service.waiters[numeric]
		active := ok && state.active >= n
		service.mu.Unlock()
		if active {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%d long-polls not registered", n)
}

// waitForWaiter blocks until a long-poll on id is registered.
func waitForWaiter(t *testing.T, service *Service, id string) {
	t.Helper()
	numeric, _ := ParseID(id)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		state, ok := service.waiters[numeric]
		active := ok && state.active > 0
		service.mu.Unlock()
		if active {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no long-poll registered")
}

// TestCancelForJob: a cancelled job's open questions are cancelled with the reason as the note and
// nothing typed back; another job's, and the job's settled ones, are left alone.
func TestCancelForJob(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	open := f.ask(t, confirm("Ship?"))
	answered := f.ask(t, confirm("Name?"))
	if _, err := f.service.Answer(ctx, answered.ID, AnswerInput{Value: json.RawMessage(`"yes"`)}); err != nil {
		t.Fatal(err)
	}
	other, err := f.service.Ask(ctx, confirm("Other job?"), Caller{PaneID: "w1:p2", JobID: "j_4"})
	if err != nil {
		t.Fatal(err)
	}
	if n := f.service.CancelForJob(ctx, asker.JobID, "the job was cancelled"); n != 1 {
		t.Fatalf("cancelled %d, want 1", n)
	}
	got, _ := f.service.Get(ctx, open.ID)
	if got.Status != StatusCancelled || got.AnswerNote != "the job was cancelled" || got.Delivery != DeliveryNone {
		t.Fatalf("the open question: %s note %q delivery %q", got.Status, got.AnswerNote, got.Delivery)
	}
	if got, _ := f.service.Get(ctx, other.ID); got.Status != StatusOpen {
		t.Fatalf("another job's question: %s", got.Status)
	}
	if got, _ := f.service.Get(ctx, answered.ID); got.Status != StatusAnswered {
		t.Fatalf("an answered question: %s", got.Status)
	}
}
