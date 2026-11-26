// Regression tests for the R2 review's supervisor findings (reviews/R2-codex.md, R2-C01 to C12
// and C16). Each one sets up the reviewer's failure scenario and asserts the fixed behaviour; the
// report (REPORT.md) says how each was seen to fail with the old code.

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// seams wraps the fake so a test can act at the exact moment the supervisor calls herdr: the
// interleavings the reviewer describes (a cancel or a hook landing between a read and a write).
type seams struct {
	*fake.Fake
	mu            sync.Mutex
	onAgentStart  func()
	onAgentGet    func()
	onProcessInfo func()
}

func once(mu *sync.Mutex, hook *func()) {
	mu.Lock()
	fn := *hook
	*hook = nil
	mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *seams) AgentStart(ctx context.Context, params herdr.AgentStartParams) (herdr.Agent, error) {
	once(&s.mu, &s.onAgentStart)
	return s.Fake.AgentStart(ctx, params)
}

func (s *seams) AgentGet(ctx context.Context, target string) (herdr.Agent, error) {
	once(&s.mu, &s.onAgentGet)
	return s.Fake.AgentGet(ctx, target)
}

func (s *seams) PaneProcessInfo(ctx context.Context, paneID string) (herdr.ProcessInfo, error) {
	once(&s.mu, &s.onProcessInfo)
	return s.Fake.PaneProcessInfo(ctx, paneID)
}

func (s *seams) set(hook *func(), fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*hook = fn
}

func newSeamRig(t *testing.T) (*rig, *seams) {
	var wrapped *seams
	r := newRig(t, func(o *Options) {
		wrapped = &seams{Fake: o.Herdr.(*fake.Fake)}
		o.Herdr = wrapped
	})
	return r, wrapped
}

// edit changes a job's row directly, as a crash or an older crowsnest would have left it.
func (r *rig) edit(id string, fn func(*store.JobRecord)) {
	r.t.Helper()
	ctx := context.Background()
	record, err := r.db.GetJob(ctx, mustParse(id))
	if err != nil {
		r.t.Fatal(err)
	}
	fn(&record)
	if err := r.db.UpdateJob(ctx, record); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) stop(id string) string {
	r.t.Helper()
	r.clock.Advance(time.Second)
	reply, err := r.svc.HandleHook(context.Background(), HookStop, id, "", HookInput{StopHookActive: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return reply.Stdout
}

func briefsTyped(r *rig, pane string) int {
	count := 0
	for _, prompt := range r.fake.Prompts() {
		if prompt.PaneID == pane && strings.HasPrefix(prompt.Text, "[crowsnest job ") {
			count++
		}
	}
	return count
}

// ---- R2-C01: progress means real work --------------------------------------------------------

func TestNoOpUpdatesDoNotResetTheNudgeBudget(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	notWaiting := false
	noOps := []func(){
		func() { _, _ = r.svc.Update(ctx, job.ID, UpdateInput{State: string(StateRunning)}) },
		func() { _, _ = r.svc.Update(ctx, job.ID, UpdateInput{Waiting: &notWaiting}) },
		func() {
			// The bookkeeping tool's own PostToolUse is not work either.
			_, _ = r.svc.HandleHook(ctx, HookPostToolUse, job.ID, "", HookInput{ToolName: "mcp__crowsnest__job_update", ToolInput: json.RawMessage(`{"progress":"still working"}`)})
		},
	}
	for i := range MaxNudges {
		if out := r.stop(job.ID); !strings.Contains(out, `"decision":"block"`) {
			t.Fatalf("stop %d was not nudged: %q", i+1, out)
		}
		r.clock.Advance(time.Second)
		noOps[i]()
	}
	if out := r.stop(job.ID); out != "" {
		t.Fatalf("after %d nudges answered only with no-op bookkeeping the stop must be allowed: %q", MaxNudges, out)
	}
	if d := r.get(job.ID); !d.Stuck || d.Cause != CauseUnresponsive {
		t.Fatalf("not escalated: %+v", d.Job)
	}
	// Real work does reset it: a different state, and a tool that is not crowsnest's.
	if _, err := r.svc.HandleHook(ctx, HookPostToolUse, job.ID, "", HookInput{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)}); err != nil {
		t.Fatal(err)
	}
	if out := r.stop(job.ID); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a real tool call is progress: %q", out)
	}
}

func TestBookkeepingTools(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        bool
	}{
		{"mcp__crowsnest__job_update", `{}`, true},
		{"mcp__crowsnest__ask", `{}`, true},
		{"Bash", `{"command":"crowsnest job update --progress x"}`, true},
		{"Bash", `{"command":"/usr/local/bin/crowsnest job get"}`, true},
		{"Bash", `{"command":"crowsnest job update x && go test ./..."}`, false},
		{"Bash", `{"command":"go test ./..."}`, false},
		{"Edit", `{"file_path":"main.go"}`, false},
		{"mcp__other__tool", `{}`, false},
	} {
		if got := isBookkeepingTool(tc.name, json.RawMessage(tc.input)); got != tc.want {
			t.Errorf("%s %s: %v, want %v", tc.name, tc.input, got, tc.want)
		}
	}
}

// ---- R2-C02: no absorbing launch states --------------------------------------------------------

func TestQueuedJobWithAPaneIsPickedUpAfterACrash(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	// crowsnest died after recording the pane and starting the agent, before the brief: the row
	// is queued with a pane, and the launch claim belongs to the process that died.
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.State, rec.BriefSent, rec.StartedAt = string(StateQueued), false, time.Time{}
		rec.LifecycleOp, rec.LifecycleOwner, rec.LifecycleAt = opLaunch, "a-crowsnest-that-died", r.clock.Now()
	})
	before := briefsTyped(r, job.PaneID)
	r.svc.Tick(context.Background())
	d := r.get(job.ID)
	if d.State != StateRunning || briefsTyped(r, job.PaneID) != before+1 {
		t.Fatalf("an interrupted launch was not finished: %+v briefs %d", d.Job, briefsTyped(r, job.PaneID)-before)
	}
}

func TestUnsentBriefWithADeadAgentIsRestarted(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.BriefSent = false })
	if err := r.fake.ExitAgent(job.PaneID); err != nil {
		t.Fatal(err)
	}
	starts := len(r.fake.Starts())
	r.svc.Tick(context.Background())
	if d := r.get(job.ID); len(r.fake.Starts()) != starts+1 || !d.briefSent || d.State != StateRunning {
		t.Fatalf("a dead agent with its brief unsent was left alone: starts %d %+v", len(r.fake.Starts())-starts, d.Job)
	}
}

func TestRestartsThatNeverStartRunOutAndTellJeff(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.fake.ExitAgent(job.PaneID); err != nil {
		t.Fatal(err)
	}
	for range maxRestartsPerHour + 1 {
		r.fake.FailNext("agent.start", &herdr.Error{Code: "launch_failed", Message: "no such binary"})
		r.clock.Advance(time.Minute)
		r.svc.Tick(ctx)
	}
	d := r.get(job.ID)
	if d.State != StateFailed || len(r.Notices()) == 0 {
		t.Fatalf("a restart that never starts must end in failed and a notice: %+v notices %v", d.Job, r.Notices())
	}
}

// ---- R2-C03: settled questions un-stick their job on any pass ---------------------------------

func TestSettledQuestionReleasesItsJobWithoutTheCallback(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	question := insertQuestion(t, r.db, job.ID, true)
	r.svc.QuestionChanged(ctx, job.ID, "q_1", "Ship?", "asked", true)
	if d := r.get(job.ID); d.State != StateNeedsYou {
		t.Fatalf("blocking ask: %+v", d.Job)
	}
	// The answer is recorded, then crowsnest dies before the settle callback reaches the job.
	settle(t, r.db, question)
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateRunning || d.Cause != CauseNone {
		t.Fatalf("a settled question left its job stuck: %+v", d.Job)
	}
}

// ---- R2-C04: only a real or pending delivery counts as an incoming answer ---------------------

func TestFailedAnswerDeliveryDoesNotExemptTheStop(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	id := insertQuestion(t, r.db, job.ID, false)
	r.clock.Advance(time.Minute)
	if err := r.db.SettleQuestion(ctx, id, "answered", json.RawMessage(`"ack"`), "", "type", r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.db.FinishDelivery(ctx, id, r.clock.Now(), "typing failed: pane gone"); err != nil {
		t.Fatal(err)
	}
	if out := r.stop(job.ID); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a stop after a failed delivery was let through as answer_incoming: %q", out)
	}
}

func TestAnswerGraceRunsOutAndTheBackstopLooksAgain(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.fake.SetScreen(job.PaneID, fleet.StripANSI(emptyInputScreen), emptyInputScreen); err != nil {
		t.Fatal(err)
	}
	poker := backstopPoker(r)
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	poker.Check(ctx, false)

	// An answer reaches the agent, its turn ends inside the grace: allowed, as the answer starts
	// the next turn. But Claude never picks it up and the agent sits idle.
	id := insertQuestion(t, r.db, job.ID, false)
	if err := r.db.SettleQuestion(ctx, id, "answered", json.RawMessage(`"ack"`), "", "type", r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.db.FinishDelivery(ctx, id, r.clock.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if out := r.stop(job.ID); out != "" {
		t.Fatalf("a stop right after the answer was typed is allowed: %q", out)
	}
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	poker.Check(ctx, false)
	r.clock.Advance(orch.BackstopIdle + time.Second)
	poker.Check(ctx, false) // still inside the grace: no nudge
	if typed := nudgesTyped(r, job.PaneID); typed != 0 {
		t.Fatalf("nudged inside the answer's grace: %d", typed)
	}
	r.clock.Advance(orch.BackstopIdle + time.Second)
	poker.Check(ctx, false) // the grace is over and nothing happened: the backstop nudges
	if typed := nudgesTyped(r, job.PaneID); typed != 1 {
		t.Fatalf("an idle agent past the answer's grace was never nudged: %d nudges", typed)
	}
}

func TestIdleJobTellsJeff(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	r.clock.Advance(idleAfter + time.Minute)
	r.svc.Tick(context.Background())
	if d := r.get(job.ID); d.Cause != CauseIdle || len(r.Notices()) != 1 || !strings.Contains(r.Notices()[0], "idle") {
		t.Fatalf("idle without a notice: %+v %v", d.Job, r.Notices())
	}
}

// ---- R2-C05: the orchestrator's assignment is reserved before the brief is typed --------------

func TestOrchestratorJobIsBoundBeforeItsBriefIsTyped(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_, _, pane := r.fake.AddWorkspace("orchestrator", "/tmp/orch")
	if err := r.fake.AddAgent(pane, "orchestrator", "claude", herdr.StatusIdle, "s-orch"); err != nil {
		t.Fatal(err)
	}
	first, err := r.svc.Create(ctx, CreateInput{Brief: "Plan the release"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Create(ctx, CreateInput{Brief: "Write the changelog"}, ""); err != nil {
		t.Fatal(err)
	}
	var heldDuringTyping string
	var updateErr error
	r.fake.OnPrompt(func(_ *fake.Fake, paneID, _ string) {
		if paneID != pane {
			return
		}
		r.fake.OnPrompt(nil)
		// While the brief is being typed the orchestrator already holds the job: its first
		// job_update and its hooks find it, and a second dispatcher takes nothing.
		heldDuringTyping = r.svc.jobInPane(ctx, pane)
		_, updateErr = r.svc.Update(ctx, first.ID, UpdateInput{Progress: "reading the brief"})
		r.svc.DispatchOrchestrator(ctx)
	})
	r.mu.Lock()
	r.orch = OrchestratorInfo{PaneID: pane, Ready: true}
	r.mu.Unlock()
	r.svc.DispatchOrchestrator(ctx)
	if heldDuringTyping != first.ID || updateErr != nil {
		t.Fatalf("during typing the pane held %q (want %s); job_update: %v", heldDuringTyping, first.ID, updateErr)
	}
	if typed := briefsTyped(r, pane); typed != 1 {
		t.Fatalf("%d briefs typed into the orchestrator, want 1", typed)
	}
	if d := r.get(first.ID); d.State != StateRunning || d.AgentName != OrchestratorName {
		t.Fatalf("first: %+v", d.Job)
	}
}

func TestInterruptedDispatchIsNotTypedTwice(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_, _, pane := r.fake.AddWorkspace("orchestrator", "/tmp/orch")
	if err := r.fake.AddAgent(pane, "orchestrator", "claude", herdr.StatusIdle, "s-orch"); err != nil {
		t.Fatal(err)
	}
	job, err := r.svc.Create(ctx, CreateInput{Brief: "Plan the release"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// crowsnest died while typing the brief into the orchestrator.
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.PaneID, rec.AgentName = pane, OrchestratorName
		rec.LifecycleOp, rec.LifecycleOwner, rec.LifecycleAt = opDispatch, "a-crowsnest-that-died", r.clock.Now()
	})
	r.mu.Lock()
	r.orch = OrchestratorInfo{PaneID: pane, Ready: true}
	r.mu.Unlock()
	r.svc.Tick(ctx)
	r.svc.Tick(ctx)
	d := r.get(job.ID)
	if d.State != StateRunning || briefsTyped(r, pane) != 0 || !hasEvent(d, EventLaunch, "not typed again") {
		t.Fatalf("an interrupted dispatch: %+v briefs %d", d.Job, briefsTyped(r, pane))
	}
}

// ---- R2-C07: a failed lookup is not a death ---------------------------------------------------

func TestLookupFailureIsNotProcessDeath(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	starts := len(r.fake.Starts())
	for _, method := range []string{"agent.get", "pane.process_info"} {
		r.fake.FailNext(method, herdr.ErrTimeout)
		r.clock.Advance(time.Minute)
		r.svc.Tick(ctx)
	}
	if d := r.get(job.ID); d.Restarts != 0 || len(r.fake.Starts()) != starts || d.Stuck {
		t.Fatalf("a lookup timeout was taken for an exit: %+v starts %d", d.Job, len(r.fake.Starts())-starts)
	}
}

// ---- R2-C08: a finished job is never restarted or prompted ------------------------------------

func TestCancelBetweenTheLookAndTheRestartWins(t *testing.T) {
	r, hooks := newSeamRig(t)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.fake.ExitAgent(job.PaneID); err != nil {
		t.Fatal(err)
	}
	starts := len(r.fake.Starts())
	// The tick has read the running row and is looking at the agent when the human cancels.
	hooks.set(&hooks.onAgentGet, func() {
		if _, err := r.svc.Cancel(ctx, job.ID); err != nil {
			t.Error(err)
		}
	})
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateCancelled || d.Restarts != 0 || len(r.fake.Starts()) != starts {
		t.Fatalf("a cancelled job was restarted: %+v starts %d", d.Job, len(r.fake.Starts())-starts)
	}
}

func TestCancelDuringARestartStopsTheBrief(t *testing.T) {
	r, hooks := newSeamRig(t)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.fake.ExitAgent(job.PaneID); err != nil {
		t.Fatal(err)
	}
	hooks.set(&hooks.onAgentStart, func() {
		if _, err := r.svc.Cancel(ctx, job.ID); err != nil {
			t.Error(err)
		}
	})
	briefs := briefsTyped(r, job.PaneID)
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateCancelled || briefsTyped(r, job.PaneID) != briefs {
		t.Fatalf("the brief was typed into a cancelled job: %+v", d.Job)
	}
}

func TestCancelDuringARetryTypesNothing(t *testing.T) {
	r, hooks := newSeamRig(t)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "rate_limit"})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	r.clock.Advance(nextGap(r, job.ID) + time.Second)
	prompts := len(r.fake.Prompts())
	// The tick has read the stuck row and is looking at the agent when the human cancels.
	hooks.set(&hooks.onProcessInfo, func() {
		if _, err := r.svc.Cancel(ctx, job.ID); err != nil {
			t.Error(err)
		}
	})
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateCancelled || len(r.fake.Prompts()) != prompts || d.RetryCount != 0 {
		t.Fatalf("a retry went on after the cancel: %+v prompts %+v", d.Job, r.fake.Prompts()[prompts:])
	}
}

// ---- R2-C09: the backstop respects the retry schedule -----------------------------------------

func TestBackstopLeavesARetryScheduleAlone(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "rate_limit"})
	list, err := r.svc.BackstopJobs(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("a job on its retry schedule is listed for the backstop: %+v %v", list, err)
	}
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	r.clock.Advance(time.Minute)
	if nudged, _ := r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID)); nudged {
		t.Fatal("the backstop nudged past the retry schedule")
	}
	if d := r.get(job.ID); d.Cause != CauseLimit || d.Nudges != 0 {
		t.Fatalf("the limit lost to the backstop: %+v", d.Job)
	}
}

// ---- R2-C10: hook and backstop deduplicated under the lock; a nudge counts once typed ---------

func TestBackstopNudgeYieldsToAStopHookThatRanMeanwhile(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	seen := r.backstopJob(job.ID) // the backstop's look: no Stop hook yet
	if out := r.stop(job.ID); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("hook: %q", out)
	}
	nudged, err := r.svc.NudgeIfDue(ctx, seen)
	if err != nil || nudged || nudgesTyped(r, job.PaneID) != 0 {
		t.Fatalf("the backstop nudged a stop its hook had just handled: %v %v", nudged, err)
	}
	if d := r.get(job.ID); d.Nudges != 1 {
		t.Fatalf("nudges %d, want the hook's one", d.Nudges)
	}
}

func TestBackstopNudgeNotTypedSpendsNothing(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Engine: EngineCodex, Brief: "Do it", Dir: t.TempDir()})
	draft, err := os.ReadFile("../fleet/testdata/screens/claude_idle_draft.ansi")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.fake.SetScreen(job.PaneID, fleet.StripANSI(string(draft)), string(draft)); err != nil {
		t.Fatal(err)
	}
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	for range MaxNudges + 1 {
		r.clock.Advance(time.Minute)
		if nudged, err := r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID)); nudged || err != nil {
			t.Fatalf("nudged over a draft: %v %v", nudged, err)
		}
	}
	if d := r.get(job.ID); d.Nudges != 0 || d.Stuck {
		t.Fatalf("refused sends spent the budget: %+v", d.Job)
	}
}

func TestStopHookDuringABackstopNudgeIsNotNudgedAgain(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Engine: EngineCodex, Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	var hookReply string
	r.fake.OnPrompt(func(_ *fake.Fake, _, text string) {
		if strings.HasPrefix(text, "[crowsnest nudge]") {
			r.fake.OnPrompt(nil)
			reply, _ := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{})
			hookReply = reply.Stdout
		}
	})
	r.clock.Advance(time.Minute)
	if nudged, err := r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID)); !nudged || err != nil {
		t.Fatalf("backstop: %v %v", nudged, err)
	}
	if hookReply != "" {
		t.Fatalf("the hook nudged a stop while the backstop's nudge was being typed: %q", hookReply)
	}
	if d := r.get(job.ID); d.Nudges != 1 {
		t.Fatalf("nudges %d, want 1", d.Nudges)
	}
}

// ---- R2-C11: a stale observation never clears a newer cause -----------------------------------

func TestWorkingAgainDoesNotClearANewerCause(t *testing.T) {
	r, hooks := newSeamRig(t)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "server_error"})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	// The tick has read the server-error row; while it looks at the agent, an auth failure lands.
	hooks.set(&hooks.onAgentGet, func() {
		_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "authentication_failed"})
	})
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseAuth {
		t.Fatalf("the auth failure was wiped by a stale 'working again': %+v", d.Job)
	}
}

// ---- R2-C16: the supervisor sees every live job -----------------------------------------------

func TestSupervisorSeesJobsBeyondTheBoardPage(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	oldest := r.create(CreateInput{Brief: "The oldest", Dir: t.TempDir()})
	for i := range listLimit {
		if _, err := r.db.InsertJob(ctx, store.JobRecord{Title: "filler", BriefMD: "filler", Role: "builder", Engine: EngineClaude,
			State: string(StateRunning), BriefSent: true, VerifiedJSON: "[]", NotVerifiedJSON: "[]", CreatedAt: r.clock.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := r.svc.BackstopJobs(ctx)
	if err != nil || len(list) != 1 || list[0].ID != oldest.ID {
		t.Fatalf("backstop list %+v %v", list, err)
	}
	if err := r.fake.ExitAgent(oldest.PaneID); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if d := r.get(oldest.ID); d.Restarts != 1 {
		t.Fatalf("the oldest live job was not supervised: %+v", d.Job)
	}
}
