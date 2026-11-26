package questions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
)

// deliveryFixture is a question fixture whose answers are typed into a fake herdr agent.
type deliveryFixture struct {
	*fixture
	herdr     *fake.Fake
	pane      string
	deliverer *Deliverer
}

func newDeliveryFixture(t *testing.T, status herdr.AgentStatus) *deliveryFixture {
	t.Helper()
	f := newFixture(t)
	client := fake.New()
	_, _, pane := client.AddWorkspace("work", "/tmp/work")
	if err := client.AddAgent(pane, "builder", "claude", status, "session-1"); err != nil {
		t.Fatal(err)
	}
	d := &deliveryFixture{fixture: f, herdr: client, pane: pane}
	d.deliverer = NewDeliverer(DelivererOptions{Store: f.store, Prompt: HerdrPrompter(client), Now: f.clock.Now, Interval: time.Millisecond})
	return d
}

// askAndAnswer asks from the fixture's pane and answers yes with a note.
func (d *deliveryFixture) askAndAnswer(t *testing.T, title string) Question {
	t.Helper()
	question, err := d.service.Ask(context.Background(), confirm(title), Caller{PaneID: d.pane})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := d.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"yes"`), Note: "go\nahead"})
	if err != nil {
		t.Fatal(err)
	}
	return answered
}

func (d *deliveryFixture) prompts() []string {
	var out []string
	for _, prompt := range d.herdr.Prompts() {
		out = append(out, prompt.Text)
	}
	return out
}

func TestDeliveryTypesIntoAnIdleAgent(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusIdle)
	question := d.askAndAnswer(t, "Ship the migration tonight?")
	d.deliverer.DeliverPending(context.Background())
	want := "[crowsnest answer " + question.ID + "] Ship the migration tonight? → Yes (note: go ahead)"
	if got := d.prompts(); len(got) != 1 || got[0] != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
	stored, _ := d.service.Get(context.Background(), question.ID)
	if stored.DeliveredAt == nil || stored.DeliveryError != "" {
		t.Fatalf("delivery not recorded: %+v", stored)
	}
	d.deliverer.DeliverPending(context.Background())
	if len(d.prompts()) != 1 {
		t.Fatal("a delivered answer was typed again")
	}
}

func TestDeliveryTypesIntoAWorkingAgent(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusWorking)
	d.askAndAnswer(t, "Ship?")
	d.deliverer.DeliverPending(context.Background())
	if len(d.prompts()) != 1 {
		t.Fatalf("a working agent should get the answer queued; typed %v", d.prompts())
	}
}

func TestDeliveryHoldsWhileBlockedThenDelivers(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusBlocked)
	question := d.askAndAnswer(t, "Ship?")
	d.deliverer.DeliverPending(context.Background())
	if len(d.prompts()) != 0 {
		t.Fatal("typed into an agent at a dialog")
	}
	stored, _ := d.service.Get(context.Background(), question.ID)
	if stored.DeliveredAt != nil || stored.DeliveryError != "" {
		t.Fatalf("a held delivery must stay pending: %+v", stored)
	}
	if err := d.herdr.SetStatus(d.pane, herdr.StatusIdle); err != nil {
		t.Fatal(err)
	}
	d.clock.Advance(time.Minute) // past the hold's backoff
	d.deliverer.DeliverPending(context.Background())
	if len(d.prompts()) != 1 {
		t.Fatalf("not delivered after the dialog closed: %v", d.prompts())
	}
}

func TestDeliveryHoldsWhenTheAgentIsMissing(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusIdle)
	question, _ := d.service.Ask(context.Background(), confirm("Gone?"), Caller{PaneID: "w9:p9"})
	_, _ = d.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	d.deliverer.DeliverPending(context.Background())
	stored, _ := d.service.Get(context.Background(), question.ID)
	if stored.DeliveredAt != nil || stored.DeliveryError != "" {
		t.Fatalf("a missing agent is a hold, not a failure: %+v", stored)
	}
}

func TestDeliveryIsAtMostOnceAfterACrash(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusIdle)
	question := d.askAndAnswer(t, "Ship?")
	numeric, _ := ParseID(question.ID)
	// A previous run set the write-ahead mark and died before recording the result.
	if ok, err := d.store.ClaimDelivery(context.Background(), numeric, d.clock.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.deliverer.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if len(d.prompts()) != 0 {
		t.Fatal("an interrupted delivery was retried")
	}
	stored, _ := d.service.Get(context.Background(), question.ID)
	if !strings.Contains(stored.DeliveryError, "interrupted") {
		t.Fatalf("interrupted delivery not recorded: %+v", stored)
	}
}

func TestDeliveryUnknownOutcomeIsNotRetried(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	calls := 0
	deliverer := NewDeliverer(DelivererOptions{Store: f.store, Now: f.clock.Now, Prompt: func(context.Context, string, string) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return errors.New("connection reset mid-send")
	}})
	question := f.ask(t, confirm("Ship?"))
	_, _ = f.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"no"`)})
	deliverer.DeliverPending(context.Background())
	f.clock.Advance(time.Hour)
	deliverer.DeliverPending(context.Background())
	if calls != 1 {
		t.Fatalf("prompted %d times; an attempt that may have typed must not be retried", calls)
	}
	stored, _ := f.service.Get(context.Background(), question.ID)
	if !strings.Contains(stored.DeliveryError, "connection reset") {
		t.Fatalf("failure not recorded: %+v", stored)
	}
}

// TestUncertainSendIsRecordedNotDelivered is R1b-C05, through the real adapter: herdr took the
// line and its answer was lost. That is neither delivered nor retryable.
func TestUncertainSendIsRecordedNotDelivered(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusIdle)
	d.deliverer = NewDeliverer(DelivererOptions{Store: d.store, Prompt: HerdrPrompter(lostAnswerClient{d.herdr}), Now: d.clock.Now, Interval: time.Millisecond})
	question := d.askAndAnswer(t, "Ship?")
	d.deliverer.DeliverPending(context.Background())
	stored, _ := d.service.Get(context.Background(), question.ID)
	if stored.DeliveredAt != nil || !strings.Contains(stored.DeliveryError, "uncertain") {
		t.Fatalf("an uncertain send was recorded as %+v", stored)
	}
	d.clock.Advance(time.Hour)
	d.deliverer.DeliverPending(context.Background())
	if len(d.prompts()) != 1 {
		t.Fatalf("an uncertain send was retried: %v", d.prompts())
	}
}

// lostAnswerClient types the prompt and then loses herdr's answer, as the socket client reports
// a deadline passing after the request was written.
type lostAnswerClient struct{ *fake.Fake }

func (c lostAnswerClient) AgentPrompt(ctx context.Context, target, text string, wait *herdr.PromptWait) (herdr.Agent, error) {
	_, _ = c.Fake.AgentPrompt(ctx, target, text, wait)
	return herdr.Agent{}, fmt.Errorf("%w: reading response: %w", herdr.ErrNoResponse, context.DeadlineExceeded)
}

// TestHeldDeliveryIsReleasedDuringShutdown is R1b-C08: shutdown cancels an attempt that typed
// nothing. Its claim must still be released, or the next start abandons a retryable answer.
func TestHeldDeliveryIsReleasedDuringShutdown(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	deliverer := NewDeliverer(DelivererOptions{Store: f.store, Now: f.clock.Now, Prompt: func(context.Context, string, string) error {
		cancel() // shutdown arrives while the agent's screen is being read
		return fmt.Errorf("%w: reading the screen: %w", ErrHold, context.Canceled)
	}})
	question := f.ask(t, confirm("Ship?"))
	_, _ = f.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	deliverer.DeliverPending(ctx)
	numeric, _ := ParseID(question.ID)
	record, err := f.store.GetQuestion(context.Background(), numeric)
	if err != nil {
		t.Fatal(err)
	}
	if !record.DeliveryClaimedAt.IsZero() {
		t.Fatal("a claim for an attempt that typed nothing survived shutdown; the next start would abandon it")
	}
}

func TestPollDeliveryIsNotTyped(t *testing.T) {
	d := newDeliveryFixture(t, herdr.StatusIdle)
	input := confirm("Blocking")
	input.Wait = true
	question, _ := d.service.Ask(context.Background(), input, Caller{PaneID: d.pane})
	_, _ = d.service.Answer(context.Background(), question.ID, AnswerInput{Value: json.RawMessage(`"yes"`)})
	d.deliverer.DeliverPending(context.Background())
	if len(d.prompts()) != 0 {
		t.Fatalf("typed an answer the asker read from the long-poll: %v", d.prompts())
	}
}

func TestAnswerLine(t *testing.T) {
	options := []Option{{Key: "a", Label: "Alpha"}, {Key: "b", Label: "Beta\x1b[31m"}}
	cases := []struct {
		name string
		q    Question
		want string
	}{
		{"single with note", Question{ID: "q_1", Title: "Pick\tone", Kind: KindChoice, Options: options, Status: StatusAnswered, AnswerValue: "a", AnswerNote: "line1\r\nline2"},
			"[crowsnest answer q_1] Pick one → Alpha (note: line1 line2)"},
		{"multi", Question{ID: "q_2", Title: "Pick", Kind: KindChoice, Multi: true, Options: options, Status: StatusAnswered, AnswerValue: []any{"a", "b"}},
			"[crowsnest answer q_2] Pick → Alpha, Beta"},
		{"manual", Question{ID: "q_3", Title: "Say", Kind: KindManual, Status: StatusAnswered, AnswerNote: "Do X"}, "[crowsnest answer q_3] Say → Do X"},
		{"ack", Question{ID: "q_4", Title: "FYI", Kind: KindAck, Status: StatusAnswered, AnswerValue: "ack"}, "[crowsnest answer q_4] FYI → acknowledged"},
		{"expired", Question{ID: "q_5", Title: "Ship?", Kind: KindConfirm, Status: StatusExpired}, "[crowsnest answer q_5] Ship? → expired with no answer (expiry is not consent)"},
		{"cancelled", Question{ID: "q_6", Title: "Ship?", Kind: KindConfirm, Status: StatusCancelled}, "[crowsnest answer q_6] Ship? → cancelled by the human"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnswerLine(tc.q); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
	long := AnswerLine(Question{ID: "q_9", Title: "t", Kind: KindManual, Status: StatusAnswered, AnswerNote: strings.Repeat("x", 2000)})
	if n := len([]rune(long)); n != MaxLineRunes || !strings.HasSuffix(long, "…") || strings.ContainsAny(long, "\n\r") {
		t.Fatalf("long line: %d runes", n)
	}
}

func TestURLNotifierPostsOnlyBlockingTitlesRedacted(t *testing.T) {
	received := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		if r.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		received <- string(body)
	}))
	defer server.Close()
	notify := NewURLNotifier(server.URL, server.Client(), nil)
	notify(Question{ID: "q_1", Title: "Normal one", Urgency: UrgencyNormal})
	notify(Question{ID: "q_2", Title: "Deploy with token=ghp_abcdefghijklmnopqrstuvwxyz0123", Urgency: UrgencyBlocking, BodyMD: "SECRET BODY"})
	select {
	case line := <-received:
		if !strings.HasPrefix(line, "crowsnest: q_2 needs you: Deploy with") || strings.Contains(line, "ghp_") || strings.Contains(line, "SECRET") {
			t.Fatalf("notice %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notice posted for a blocking question")
	}
	select {
	case line := <-received:
		t.Fatalf("unexpected second notice %q", line)
	case <-time.After(100 * time.Millisecond):
	}
}
