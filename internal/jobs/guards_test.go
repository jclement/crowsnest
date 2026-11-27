package jobs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

// ---- The fleet's usage limit (6.17) ---------------------------------------------------------

func TestParseLimitReset(t *testing.T) {
	now := time.Date(2026, 11, 27, 13, 20, 0, 0, time.Local)
	cases := []struct {
		message string
		want    time.Time
		guessed bool
	}{
		{"5-hour limit reached ∙ resets 3pm", time.Date(2026, 11, 27, 15, 0, 0, 0, time.Local), false},
		{"Claude usage limit reached. Your limit will reset at 3:30 PM (America/Edmonton).", time.Date(2026, 11, 27, 15, 30, 0, 0, time.Local), false},
		{"limit reached, resets 9am", time.Date(2026, 11, 28, 9, 0, 0, 0, time.Local), false},
		{"rate limited: try again in 2 hours", now.Add(2 * time.Hour), false},
		{"Please try again in 45 minutes", now.Add(45 * time.Minute), false},
		{"rate_limit", now.Add(limitGuess), true},
	}
	for _, tc := range cases {
		got, guessed := ParseLimitReset(tc.message, now)
		if !got.Equal(tc.want) || guessed != tc.guessed {
			t.Errorf("%q: got %v (guessed %v), want %v (%v)", tc.message, got, guessed, tc.want, tc.guessed)
		}
	}
}

// TestFleetLimitHoldsAndResumesInOrder: one agent's limit holds every agent of the engine: new
// starts, nudges and retries; at the reset the waiting jobs resume one at a time, oldest first;
// the limit survives a restart.
func TestFleetLimitHoldsAndResumesInOrder(t *testing.T) {
	var limits *FleetLimits
	r := newRig(t, func(o *Options) {
		limits = NewFleetLimits(o.Store, nil, o.Now)
		o.Limits = limits
	})
	ctx := context.Background()
	first := r.create(CreateInput{Brief: "First", Dir: t.TempDir()})
	second := r.create(CreateInput{Brief: "Second", Dir: t.TempDir()})
	for _, job := range []Detail{first, second} {
		_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
		r.edit(job.ID, func(rec *store.JobRecord) { rec.BriefSent = true })
	}
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, first.ID, "", HookInput{Error: "rate_limit", ErrorDetails: "usage limit reached, try again in 30 minutes"})
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, second.ID, "", HookInput{Error: "rate_limit", ErrorDetails: "usage limit reached"})
	reset := r.clock.Now().Add(30 * time.Minute)
	for _, id := range []string{first.ID, second.ID} {
		if d := r.get(id); d.RetryAt == nil || !d.RetryAt.Equal(reset.UTC()) || !strings.Contains(d.CauseDetail, "usage limit resets at") {
			t.Fatalf("%s: retry %v detail %q", id, d.RetryAt, d.CauseDetail)
		}
	}
	list := r.svc.FleetLimitsNow(ctx)
	if len(list) != 1 || list[0].Engine != EngineClaude || list[0].Held != 2 || list[0].Job != first.ID {
		t.Fatalf("limits %+v", list)
	}
	// A new job of the engine waits for the reset.
	third := r.create(CreateInput{Brief: "Third", Dir: t.TempDir()})
	if d := r.get(third.ID); d.State != StateQueued || d.Cause != CauseHeld || !strings.HasPrefix(d.CauseDetail, limitHoldPrefix) {
		t.Fatalf("a new start was not held: %s %s %q", d.State, d.Cause, d.CauseDetail)
	}
	// Nothing is retried before the reset.
	prompts := len(r.fake.Prompts())
	r.clock.Advance(29 * time.Minute)
	r.svc.Tick(ctx)
	if len(r.fake.Prompts()) != prompts {
		t.Fatalf("typed before the reset: %v", r.fake.Prompts()[prompts:])
	}
	// The limit is remembered across a restart.
	if held, _ := NewFleetLimits(r.db, nil, r.clock.Now).Held(ctx, EngineClaude); !held {
		t.Fatal("a restart forgot the limit")
	}
	// After the reset: the oldest first, the next one a turn later.
	// (The held starts are launched then too, oldest first, as for any hold.)
	continues := func() []string {
		var panes []string
		for _, prompt := range r.fake.Prompts()[prompts:] {
			if prompt.Text == "continue" {
				panes = append(panes, prompt.PaneID)
			}
		}
		return panes
	}
	r.clock.Advance(2 * time.Minute)
	r.svc.Tick(ctx)
	if typed := continues(); len(typed) != 1 || typed[0] != first.PaneID {
		t.Fatalf("first resume: %+v", typed)
	}
	r.clock.Advance(resumeEvery + time.Second)
	r.svc.Tick(ctx)
	if typed := continues(); len(typed) != 2 || typed[1] != second.PaneID {
		t.Fatalf("second resume: %+v", typed)
	}
}

func TestFleetLimitAllowsStops(t *testing.T) {
	r := newRig(t, func(o *Options) { o.Limits = NewFleetLimits(o.Store, nil, o.Now) })
	ctx := context.Background()
	limited := r.create(CreateInput{Brief: "Limited", Dir: t.TempDir()})
	also := r.create(CreateInput{Brief: "Also limited", Dir: t.TempDir()})
	busy := r.create(CreateInput{Brief: "Busy", Dir: t.TempDir()})
	// R6-S3: one job's report holds only that job; a second job agreeing holds the fleet.
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, limited.ID, "", HookInput{Error: "rate_limit", ErrorDetails: "resets 11pm"})
	if held, _ := r.svc.limitHeld(ctx, EngineClaude); held {
		t.Fatal("one job's report held the whole fleet")
	}
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, also.ID, "", HookInput{Error: "rate_limit", ErrorDetails: "resets 11pm"})
	if reply := r.stop(busy.ID); reply != "" {
		t.Fatalf("a stop under the fleet's limit was nudged: %s", reply)
	}
	d := r.get(busy.ID)
	if !hasEvent(d, EventHook, "usage limit") {
		t.Fatal("the timeline does not say why the stop was allowed")
	}
	// R6-C08: that stop is registered for the reset: held by the usage limit, retried after it.
	if !hasHold(r.record(busy.ID), HoldUsageLimit) || !d.Stuck || d.Cause != CauseLimit || d.RetryAt == nil {
		t.Fatalf("a stop during the fleet's limit was not registered for the reset: %+v", d.Job)
	}
}

// TestLimitResetIsCappedAndClearable (R6-S3): a reset time past a day is capped and called a
// guess, and the human's Clear forgets the limit and lets its jobs go.
func TestLimitResetIsCappedAndClearable(t *testing.T) {
	now := time.Now()
	at, guessed := ParseLimitReset("usage limit reached, try again in 2000000 hours", now)
	if !guessed || at.Sub(now) > limitMax {
		t.Fatalf("an absurd reset was believed: %s guessed %v", at.Sub(now), guessed)
	}
	r := newRig(t, func(o *Options) { o.Limits = NewFleetLimits(o.Store, nil, o.Now) })
	ctx := context.Background()
	first := r.create(CreateInput{Brief: "First", Dir: t.TempDir()})
	second := r.create(CreateInput{Brief: "Second", Dir: t.TempDir()})
	for _, job := range []Detail{first, second} {
		_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "rate_limit", ErrorDetails: "try again in 2000000 hours"})
	}
	if held, state := r.svc.limitHeld(ctx, EngineClaude); !held || state.ResetAt.Sub(r.clock.Now()) > limitMax {
		t.Fatalf("held %v until %s", held, state.ResetAt)
	}
	released, err := r.svc.ClearLimit(ctx, EngineClaude)
	if err != nil || released != 2 {
		t.Fatalf("clear: %d %v", released, err)
	}
	if held, _ := r.svc.limitHeld(ctx, EngineClaude); held {
		t.Fatal("still held after the human cleared it")
	}
	for _, job := range []Detail{first, second} {
		if hasHold(r.record(job.ID), HoldUsageLimit) {
			t.Fatalf("%s still held", job.ID)
		}
	}
}

// ---- Budgets (6.16) -------------------------------------------------------------------------

// fakeSpend reports fixed spend per job id.
type fakeSpend map[string]spend.Summary

func (f fakeSpend) Summaries(_ context.Context, jobs []spend.JobInfo) (map[string]spend.Summary, error) {
	out := map[string]spend.Summary{}
	for _, job := range jobs {
		if summary, ok := f[job.ID]; ok {
			out[job.ID] = summary
		}
	}
	return out, nil
}
func (f fakeSpend) Turns(context.Context, string) ([]spend.Turn, error) { return nil, nil }

func dollars(value float64) spend.Summary {
	return spend.Summary{Measured: true, CostUSD: &value, TotalTokens: int64(value * 1000)}
}

func TestBudgetThresholdsChildrenAndUnmeasured(t *testing.T) {
	spent := fakeSpend{}
	r := newRig(t, func(o *Options) {
		o.Spend = spent
		o.Budgets = BudgetOptions{JobUSD: 2}
	})
	ctx := context.Background()
	parent := r.create(CreateInput{Brief: "Parent", Dir: t.TempDir()})
	child, err := r.svc.Create(ctx, CreateInput{Brief: "Child", Dir: t.TempDir()}, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want BudgetView) *BudgetView {
		t.Helper()
		record, _ := r.db.GetJob(ctx, mustParse(parent.ID))
		view := r.svc.BudgetOf(ctx, record)
		if view == nil {
			t.Fatal("no budget view")
		}
		r.svc.applyBudget(ctx, parent.ID, *view)
		return view
	}
	// 70%: nothing. 90% (children count): warned, the agent told once.
	spent[parent.ID], spent[child.ID] = dollars(1.0), dollars(0.4)
	if view := check(BudgetView{}); view.Share < 0.69 || view.Share > 0.71 || view.Used.Children != 1 {
		t.Fatalf("70%%: %+v", view)
	}
	if d := r.get(parent.ID); d.Budget == nil || d.Budget.State != BudgetOK {
		t.Fatalf("warned too early: %+v", d.Budget)
	}
	spent[child.ID] = dollars(0.8)
	check(BudgetView{})
	if d := r.get(parent.ID); d.Budget.State != BudgetWarned || !lastPromptHas(r, "[crowsnest budget]") {
		t.Fatalf("at 90%%: %+v", d.Budget)
	}
	// 100%: needs you, the agent told to stop at a safe point; nothing killed (the pane is there).
	spent[parent.ID] = dollars(1.3)
	check(BudgetView{})
	d := r.get(parent.ID)
	if d.State != StateNeedsYou || d.Cause != CauseBudget || d.PaneID == "" {
		t.Fatalf("at 100%%: %s %s", d.State, d.Cause)
	}
	if c := r.get(child.ID); c.State != StateNeedsYou || c.Cause != CauseBudget || !strings.Contains(c.CauseDetail, parent.ID) {
		t.Fatalf("the child kept going past its parent's budget: %s %s %q", c.State, c.Cause, c.CauseDetail)
	}
	if _, err := r.svc.Update(ctx, parent.ID, UpdateInput{Progress: "carry on", State: "running"}); err == nil {
		t.Fatal("declaring running past the budget was not refused")
	}
	if r.get(parent.ID).State != StateNeedsYou {
		t.Fatal("the agent lifted its own budget")
	}
	// Let it finish the step: running in grace; its next stop is back to the human.
	if _, err := r.svc.BudgetFinishStep(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if d = r.get(parent.ID); d.State != StateRunning {
		t.Fatalf("finish step: %s", d.State)
	}
	if reply := r.stop(parent.ID); reply != "" {
		t.Fatalf("the stop after the step was refused: %s", reply)
	}
	if d = r.get(parent.ID); d.State != StateNeedsYou || d.Cause != CauseBudget {
		t.Fatalf("after the step: %s %s", d.State, d.Cause)
	}
	// Raise it: back to work, told so.
	five := 5.0
	if _, err := r.svc.SetJobBudget(ctx, parent.ID, BudgetInput{USD: &five}); err != nil {
		t.Fatal(err)
	}
	if d = r.get(parent.ID); d.State != StateRunning || d.Budget.Limits.USD != 5 || d.Budget.Limits.Source["usd"] != "job" || !lastPromptHas(r, "raised") {
		t.Fatalf("after the raise: %s %+v", d.State, d.Budget)
	}
}

func TestBudgetUnmeasuredIsNotZero(t *testing.T) {
	r := newRig(t, func(o *Options) {
		o.Spend = fakeSpend{}
		o.Budgets = BudgetOptions{JobUSD: 1, JobTurns: 4}
	})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Codex work", Dir: t.TempDir(), Engine: EngineCodex})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.Turns = 3 })
	record, _ := r.db.GetJob(ctx, mustParse(job.ID))
	view := r.svc.BudgetOf(ctx, record)
	if view.Used.USD != nil || view.Used.Measured {
		t.Fatalf("unmeasured spend shown as a number: %+v", view.Used)
	}
	if view.Over != "turns" || view.Share != 0.75 {
		t.Fatalf("turns bound it: %+v", view)
	}
	if line := budgetLine(view); !strings.Contains(line, "not measured") {
		t.Fatalf("job_get line %q", line)
	}
}

func TestFleetCeilingHoldsStarts(t *testing.T) {
	r := newRig(t, func(o *Options) {
		o.Budgets = BudgetOptions{DailyUSD: 10}
		o.SpendToday = func(context.Context) (DaySpend, error) { return DaySpend{USD: 12, Measured: true}, nil }
	})
	job := r.create(CreateInput{Brief: "Later", Dir: t.TempDir()})
	if d := r.get(job.ID); d.State != StateQueued || !strings.HasPrefix(d.CauseDetail, ceilingHoldPrefix) {
		t.Fatalf("not held by the ceiling: %s %q", d.State, d.CauseDetail)
	}
	if reached, _ := r.svc.StandingCeiling(context.Background(), spend.OrchestratorSubject); reached {
		t.Fatal("no orchestrator ceiling is set")
	}
}

func lastPromptHas(r *rig, text string) bool {
	prompts := r.fake.Prompts()
	return len(prompts) > 0 && strings.Contains(prompts[len(prompts)-1].Text, text)
}

// ---- Waits (6.18) ---------------------------------------------------------------------------

func TestBlockedNeedsAWakeUpAndIsWoken(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Wait for staging", Dir: t.TempDir()})
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "x", State: "blocked", Reason: "staging is down"}); err == nil || !strings.Contains(err.Error(), "wake-up") {
		t.Fatalf("blocked with no wake-up: %v", err)
	}
	for _, bad := range []UpdateInput{{RecheckIn: "forever"}, {RecheckIn: "48h"}, {RecheckIn: "30m", UntilHuman: true}, {UntilJob: job.ID}} {
		bad.Progress, bad.State, bad.Reason = "x", "blocked", "y"
		if _, err := r.svc.Update(ctx, job.ID, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "x", State: "blocked", Reason: "staging is down", RecheckIn: "30m"}); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.WakeKind != WakeTime || d.WaitOwner != "job-1" || !strings.HasPrefix(d.Wake, "rechecks at") {
		t.Fatalf("blocked: %s %q %q", d.WakeKind, d.WaitOwner, d.Wake)
	}
	r.clock.Advance(29 * time.Minute)
	r.svc.Tick(ctx)
	if r.get(job.ID).State != StateBlocked {
		t.Fatal("woken early")
	}
	r.clock.Advance(2 * time.Minute)
	r.svc.Tick(ctx)
	d = r.get(job.ID)
	if d.State != StateRunning || d.Rechecks != 1 || !lastPromptHas(r, "[crowsnest recheck]") || !lastPromptHas(r, "staging is down") {
		t.Fatalf("after the recheck: %s %d", d.State, d.Rechecks)
	}
}

// TestDependencyWakesItsWaiterAcrossARestart: a job blocked until another settles is woken when
// it does, even if crowsnest was down at the time (a new service, the same store, a tick).
func TestDependencyWakesItsWaiterAcrossARestart(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	other := r.create(CreateInput{Brief: "Build the API", Dir: t.TempDir()})
	waiter := r.create(CreateInput{Brief: "Build the client", Dir: t.TempDir()})
	if _, err := r.svc.Update(ctx, waiter.ID, UpdateInput{Progress: "x", State: "blocked", Reason: "needs the API", UntilJob: other.ID}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(waiter.ID); d.Wake != "woken when "+other.ID+" finishes" {
		t.Fatalf("wake %q", d.Wake)
	}
	// The other job finishes while "crowsnest is down": written straight to the store.
	r.edit(other.ID, func(rec *store.JobRecord) { rec.State, rec.ResultMD = string(StateDone), "API at /v2" })
	restarted := New(r.svc.opts)
	restarted.Tick(ctx)
	d := r.get(waiter.ID)
	if d.State != StateRunning || !lastPromptHas(r, other.ID) || !lastPromptHas(r, "API at /v2") {
		t.Fatalf("after the restart: %s", d.State)
	}
	// And the immediate path: settling a dependency wakes at once.
	third := r.create(CreateInput{Brief: "Docs", Dir: t.TempDir()})
	if _, err := r.svc.Update(ctx, third.ID, UpdateInput{Progress: "x", State: "blocked", Reason: "needs the client", UntilJob: waiter.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Fail(ctx, waiter.ID, "gave up"); err != nil {
		t.Fatal(err)
	}
	if r.get(third.ID).State != StateRunning {
		t.Fatal("a settled dependency did not wake its waiter")
	}
}

// TestEveryLiveStateHasAnOwnerAndAWakeUp: the invariant over each state a job can rest in, and
// the check that lists a job breaking it.
func TestEveryLiveStateHasAnOwnerAndAWakeUp(t *testing.T) {
	rows := []store.JobRecord{
		{State: "queued", Dir: "/x"},
		{State: "queued", Cause: string(CauseHeld), CauseDetail: "waiting for a slot"},
		{State: "queued", Cause: string(CauseApproval)},
		{State: "queued"},
		{State: "running", AgentName: "job-1"},
		{State: "running", Stuck: true, Cause: string(CauseLimit), RetryAt: time.Now()},
		{State: "running", Stuck: true, Cause: string(CauseSpinning)},
		{State: "needs_you", Cause: string(CauseBudget)},
		{State: "blocked", WakeKind: WakeTime, WakeAt: time.Now()},
		{State: "blocked", WakeKind: WakeJob, WakeJob: 3},
		{State: "blocked", WakeKind: WakeHuman},
		{State: "in_review", AcceptState: AcceptChecking},
		{State: "in_review"},
	}
	for _, row := range rows {
		owner, wake := waitOf(row, false)
		if owner == "" || wake == "" {
			t.Errorf("%s/%s: owner %q wake %q", row.State, row.Cause, owner, wake)
		}
	}
	if owner, wake := waitOf(store.JobRecord{State: "running"}, true); owner != "you" || !strings.Contains(wake, "Resume") {
		t.Errorf("paused: %q %q", owner, wake)
	}
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Orphan", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.State, rec.PaneID, rec.LifecycleOp = string(StateRunning), "", "" })
	r.clock.Advance(10 * time.Minute)
	r.svc.tickInvariants(ctx)
	list, _ := r.svc.WaitViolations()
	if len(list) != 1 || list[0].JobID != job.ID || !strings.Contains(list[0].Rule, "no agent") {
		t.Fatalf("violations %+v", list)
	}
	if !hasEvent(r.get(job.ID), EventWake, "Invariant") {
		t.Fatal("not journalled on the job")
	}
	// A legacy blocked job with no wake-up is given one by the tick, not left for ever.
	legacy := r.create(CreateInput{Brief: "Old", Dir: t.TempDir()})
	r.edit(legacy.ID, func(rec *store.JobRecord) { rec.State, rec.BlockedReason = string(StateBlocked), "CI" })
	r.svc.Tick(ctx)
	if d := r.get(legacy.ID); d.WakeKind != WakeTime {
		t.Fatalf("legacy blocked: %q", d.WakeKind)
	}
}

// ---- The prose question at Stop (6.18) ------------------------------------------------------

func TestStopOnAProseQuestionSaysSo(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	reply, err := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{LastAssistant: "The tests pass. Should I push the branch?"})
	if err != nil || !strings.Contains(reply.Stdout, "You asked a question in prose") || !strings.Contains(reply.Stdout, "Call ask") {
		t.Fatalf("reply %q %v", reply.Stdout, err)
	}
	d := r.get(job.ID)
	if d.LastMessage != "The tests pass. Should I push the branch?" || d.Turns != 1 {
		t.Fatalf("last message %q turns %d", d.LastMessage, d.Turns)
	}
	reply, _ = r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{LastAssistant: "Working on it."})
	if strings.Contains(reply.Stdout, "prose") || !strings.Contains(reply.Stdout, "have not finished") {
		t.Fatalf("a plain stop: %q", reply.Stdout)
	}
}

// ---- Answer receipt (6.19) ------------------------------------------------------------------

func TestReceiptFromTheTranscript(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	now := r.clock.Now()
	ask := func(title string) int64 {
		id, err := r.db.InsertQuestion(ctx, store.QuestionRecord{Title: title, Kind: "confirm", Urgency: "normal", Options: []byte("[]"), Context: []byte("{}"),
			Files: []byte("[]"), JobID: job.ID, SourcePane: job.PaneID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.db.SettleQuestion(ctx, id, "answered", []byte(`"yes"`), "", "type", now); err != nil {
			t.Fatal(err)
		}
		return id
	}
	delivered, uncertain, pending := ask("Ship it?"), ask("Rename it?"), ask("Later?")
	_ = r.db.FinishDelivery(ctx, delivered, now, "")
	_ = r.db.FinishDelivery(ctx, uncertain, now, "delivery uncertain: crowsnest lost herdr's answer")
	list, err := r.db.DeliveredUnreceived(ctx, job.ID, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("unreceived %v %v", list, err)
	}
	// Transcript lines as Claude Code writes them (a timestamp on each). R6-C12: only an incoming
	// user message that is the answer line, recorded after the question settled, counts; the
	// agent's own text, a tool's output and a line from before the answer do not.
	after := now.Add(5 * time.Second).UTC().Format(time.RFC3339)
	before := now.Add(-time.Minute).UTC().Format(time.RFC3339)
	body := []byte(fmt.Sprintf(`{"type":"assistant","uuid":"a1","timestamp":%q,"message":{"content":[{"type":"text","text":"[crowsnest answer q_%d] I am waiting for this"}]}}`+"\n"+
		`{"type":"user","uuid":"u0","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"[crowsnest answer q_%d] from a log"}]}}`+"\n"+
		`{"type":"user","uuid":"u00","timestamp":%q,"message":{"content":"[crowsnest answer q_%d] Later? → yes"}}`+"\n",
		after, pending, after, pending, before, pending))
	r.svc.markReceived(ctx, job.ID, []int64{pending}, body)
	if receipt, _ := r.db.GetQuestionReceipt(ctx, pending); !receipt.ReceivedAt.IsZero() {
		t.Fatal("a marker in the agent's text, a tool result or an earlier line was taken as the answer's receipt")
	}
	body = []byte(fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":%q,"message":{"content":"[crowsnest answer q_%d] Ship it? → yes"}}`+"\n"+
		`{"type":"user","uuid":"u2","timestamp":%q,"message":{"content":"[crowsnest answer q_%d] Rename it? → yes"}}`+"\n", after, delivered, after, uncertain))
	r.svc.markReceived(ctx, job.ID, list, body)
	for _, id := range []int64{delivered, uncertain} {
		if receipt, _ := r.db.GetQuestionReceipt(ctx, id); receipt.ReceivedAt.IsZero() {
			t.Fatalf("q_%d not received", id)
		}
	}
	if receipt, _ := r.db.GetQuestionReceipt(ctx, pending); !receipt.ReceivedAt.IsZero() {
		t.Fatal("a pending answer was marked received")
	}
	// Received is final: a retype is refused (the store refuses a received or clean delivery).
	if err := r.db.ResetDelivery(ctx, uncertain); err == nil {
		t.Fatal("an answer seen received was put back to be typed again")
	}
	byID := map[string]string{}
	for _, q := range r.get(job.ID).Questions {
		byID[q.ID] = q.Receipt
	}
	if byID[fmt.Sprintf("q_%d", delivered)] != "received" || byID[fmt.Sprintf("q_%d", pending)] != "stored" {
		t.Fatalf("receipts %v", byID)
	}
}

// TestSetBudgetOnAJobUnderIt: a budget set on a job that was not waiting on one is just
// recorded (found by the real-Claude test: the change returned "nothing to do").
func TestSetBudgetOnAJobUnderIt(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	minute := 1
	if _, err := r.svc.SetJobBudget(context.Background(), job.ID, BudgetInput{Minutes: &minute}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.Budget == nil || d.Budget.Limits.Minutes != 1 {
		t.Fatalf("budget %+v", d.Budget)
	}
}
