// Regression tests for the R3 review's supervisor findings (reviews/R3-codex.md). Each sets up
// the reviewer's failure scenario and asserts the fixed behaviour; REPORT.md says how each was
// seen to fail with the old code.

package jobs

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// ---- R3-C01: an interrupted restart leaves no unowned "exited" -------------------------------

func TestInterruptedRestartLeavesNoUnownedExit(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	// A restart claimed the job (stuck/exited), started the replacement, and crowsnest died before
	// the restart finished: the replacement is up and idle, its brief long sent.
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.Stuck, rec.Cause, rec.CauseDetail = true, string(CauseExited), "Restarting the agent"
		rec.LifecycleOp, rec.LifecycleOwner, rec.LifecycleAt = opStart, "a-crowsnest-that-died", r.clock.Now()
	})
	r.clock.Advance(31 * time.Second) // past exitedGrace
	r.svc.Tick(ctx)
	d := r.get(job.ID)
	if d.Stuck || d.Cause == CauseExited {
		t.Fatalf("a live agent is still marked exited with nobody restarting it: %+v", d.Job)
	}
	list, err := r.svc.BackstopJobs(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("the backstop does not watch the job again: %+v %v", list, err)
	}
	// Left idle, it is flagged and Jeff is told: never a silent stop.
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	r.clock.Advance(idleAfter + time.Minute)
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.Cause != CauseIdle || !anyContains(r.Notices(), "idle") {
		t.Fatalf("an idle replacement was never reported: %+v %q", d.Job, r.Notices())
	}
}

func TestExitIsNotTakenBackWhileTheProcessIsLeaving(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	// SessionEnd just ran; the process has not left its pane yet.
	if _, err := r.svc.HandleHook(ctx, HookSessionEnd, job.ID, "", HookInput{Reason: "other"}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.Cause != CauseExited {
		t.Fatalf("an exit was cleared within the grace: %+v", d.Job)
	}
}

func anyContains(list []string, part string) bool {
	for _, item := range list {
		if strings.Contains(item, part) {
			return true
		}
	}
	return false
}

// ---- R3-C04: delivering a brief never erases a newer disposition ------------------------------

func TestBriefDeliveryKeepsWhatTheAgentDeclaredMeanwhile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		act   func(r *rig, jobID string)
		state State
		cause Cause
	}{
		{"blocked", func(r *rig, id string) {
			if _, err := r.svc.Update(context.Background(), id, UpdateInput{Progress: "waiting on the database", State: string(StateBlocked), Reason: "the database is down"}); err != nil {
				r.t.Error(err)
			}
		}, StateBlocked, ""},
		{"auth failure", func(r *rig, id string) {
			if _, err := r.svc.HandleHook(context.Background(), HookStopFailure, id, "", HookInput{Error: "authentication_failed"}); err != nil {
				r.t.Error(err)
			}
		}, StateNeedsYou, CauseAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, nil)
			// While crowsnest waits for the brief's turn to start, the agent (or its hook) speaks.
			r.fake.OnPrompt(func(_ *fake.Fake, _, text string) {
				if id, ok := strings.CutPrefix(text, "[crowsnest job "); ok {
					r.fake.OnPrompt(nil)
					tc.act(r, strings.Fields(id)[0][:len(strings.Fields(id)[0])-1])
				}
			})
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			d := r.get(job.ID)
			if d.State != tc.state || d.Cause != tc.cause || !d.briefSent {
				t.Fatalf("the brief's completion overwrote the agent's own word: %s/%s, want %s/%s (brief sent %v)", d.State, d.Cause, tc.state, tc.cause, d.briefSent)
			}
		})
	}
}

func TestDispatchKeepsWhatTheOrchestratorDeclaredMeanwhile(t *testing.T) {
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
	r.fake.OnPrompt(func(_ *fake.Fake, paneID, _ string) {
		if paneID == pane {
			r.fake.OnPrompt(nil)
			if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "need a decision", State: string(StateBlocked), Reason: "waiting on the vendor"}); err != nil {
				t.Error(err)
			}
		}
	})
	r.mu.Lock()
	r.orch = OrchestratorInfo{PaneID: pane, Ready: true}
	r.mu.Unlock()
	r.svc.DispatchOrchestrator(ctx)
	if d := r.get(job.ID); d.State != StateBlocked || !d.briefSent {
		t.Fatalf("the dispatch's completion overwrote the orchestrator's blocked: %+v", d.Job)
	}
}

// ---- R3-C06: a nudge or retry interrupted by a crash stays counted ----------------------------

// dieAfterTyping makes the process "crash" right after herdr accepts a prompt starting with
// prefix: the claim now belongs to a dead process, so this one cannot record the outcome, and the
// next pass finds a stale claim.
func dieAfterTyping(r *rig, prefix string) {
	r.fake.OnPrompt(func(_ *fake.Fake, _, text string) {
		if !strings.HasPrefix(text, prefix) {
			return
		}
		record, err := r.db.GetJob(context.Background(), 1)
		if err != nil {
			r.t.Error(err)
			return
		}
		record.LifecycleOwner, record.LifecycleGen = "a-crowsnest-that-died", record.LifecycleGen+100
		if err := r.db.UpdateJob(context.Background(), record); err != nil {
			r.t.Error(err)
		}
	})
}

func promptsStarting(r *rig, prefix string) int {
	count := 0
	for _, prompt := range r.fake.Prompts() {
		if strings.HasPrefix(prompt.Text, prefix) {
			count++
		}
	}
	return count
}

func TestNudgesInterruptedByCrashesStillRunOut(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Engine: EngineCodex, Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	dieAfterTyping(r, "[crowsnest nudge]")
	for range 2 * MaxNudges {
		r.clock.Advance(time.Minute)
		_, _ = r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID))
	}
	if typed := promptsStarting(r, "[crowsnest nudge]"); typed > MaxNudges {
		t.Fatalf("%d nudges typed across crashes, the budget is %d", typed, MaxNudges)
	}
	if d := r.get(job.ID); !d.Stuck || d.Cause != CauseUnresponsive || !anyContains(r.Notices(), "stuck") {
		t.Fatalf("the budget never ran out, so Jeff was never told: %+v %q", d.Job, r.Notices())
	}
}

func TestRetriesInterruptedByCrashesKeepTheBackoffAndTheCap(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "server_error"})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	dieAfterTyping(r, continueText)
	for range 8 {
		r.clock.Advance(16 * time.Minute) // past every API-error back-off
		_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
		r.svc.Tick(ctx)
	}
	if typed := promptsStarting(r, continueText); typed > 3 {
		t.Fatalf("%d retries typed across crashes; API errors are retried 3 times", typed)
	}
	if !anyContains(r.Notices(), "API errors") {
		t.Fatalf("the retry cap was never reached, so Jeff was never told: %q", r.Notices())
	}
}

// ---- R2-C08 residual: a cancel during the send's own checks wins ------------------------------

func TestCancelDuringTheSendsChecksTypesNothingAndInterruptsLast(t *testing.T) {
	r, hooks := newSeamRig(t)
	ctx := context.Background()
	job := r.create(CreateInput{Engine: EngineCodex, Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	nudges := promptsStarting(r, "[crowsnest nudge]")
	// The nudge's claim held; orch.Prompt is reading the screen when the human cancels.
	hooks.set(&hooks.onPaneRead, func() {
		if _, err := r.svc.Cancel(ctx, job.ID); err != nil {
			t.Error(err)
		}
	})
	r.clock.Advance(time.Minute)
	_, _ = r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID))
	if typed := promptsStarting(r, "[crowsnest nudge]") - nudges; typed != 0 {
		t.Fatalf("a nudge was typed into a cancelled job (%d)", typed)
	}
	keys := r.fake.SentKeys()
	if len(keys) == 0 || keys[len(keys)-1].Keys[0] != "esc" {
		t.Fatalf("the cancel's Esc was not sent: %+v", keys)
	}
	if d := r.get(job.ID); d.State != StateCancelled {
		t.Fatalf("state %s", d.State)
	}
}

// ---- R3-C05: a stop let through for crowsnest's own typing is looked at again -----------------

func TestStopDuringTypingIsRevisitedWhenTheAgentStaysIdle(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.fake.SetScreen(job.PaneID, fleet.StripANSI(emptyInputScreen), emptyInputScreen); err != nil {
		t.Fatal(err)
	}
	poker := backstopPoker(r)
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	poker.Check(ctx, false)
	// The human's nudge is being typed when the agent's turn ends: the stop is allowed, since the
	// nudge starts the next turn. But that turn ends at once, with nothing declared.
	var hookReply string
	r.fake.OnPrompt(func(_ *fake.Fake, _, text string) {
		if strings.HasPrefix(text, "[crowsnest nudge]") {
			r.fake.OnPrompt(nil)
			hookReply = r.stop(job.ID)
		}
	})
	if _, err := r.svc.Nudge(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if hookReply != "" {
		t.Fatalf("a stop while crowsnest types is allowed: %q", hookReply)
	}
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	poker.Check(ctx, false)
	r.clock.Advance(orch.BackstopIdle + time.Second)
	poker.Check(ctx, false)
	if typed := nudgesTyped(r, job.PaneID); typed != 2 {
		t.Fatalf("the agent sat idle after a stop let through for typing and was never nudged: %d nudges typed (want the human's and the backstop's)", typed)
	}
}

// ---- R3-C03: an allow with a note starts nothing ----------------------------------------------

func TestAllowWithANoteDoesNotLaunch(t *testing.T) {
	ctx := context.Background()
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead"})
	other := t.TempDir()
	job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: other, Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.questions.Answer(ctx, job.CauseDetail, questions.AnswerInput{Value: json.RawMessage(`"allow"`), Note: "Not until tomorrow"}); err != nil {
		t.Fatal(err)
	}
	starts := len(d.fake.Starts())
	d.svc.ReconcileApprovals(ctx)
	got := d.get(job.ID)
	if got.State != StateCancelled || len(d.fake.Starts()) != starts {
		t.Fatalf("a qualified allow launched the job: %+v", got.Job)
	}
	if slices.Contains(d.svc.RememberedDirs(ctx), other) {
		t.Fatal("a qualified allow widened the remembered directories")
	}
}

// ---- R3-C02: held job, question and link survive a crash between them --------------------------

func TestOrphanedApprovalIsCancelledByTheTick(t *testing.T) {
	ctx := context.Background()
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead"})
	// crowsnest died after recording the held job and before asking.
	d.svc.opts.AskHuman = func(context.Context, questions.AskInput, questions.Caller) (questions.Question, error) {
		panic("crash")
	}
	var held Job
	func() {
		defer func() { _ = recover() }()
		held, _ = d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: t.TempDir(), Role: "tester"}, caller)
	}()
	list, err := d.svc.List(ctx, []State{StateQueued})
	if err != nil || len(list) != 1 || held.ID != "" {
		t.Fatalf("setup: %+v %v", list, err)
	}
	d.clock.Advance(2 * time.Minute)
	d.svc.Tick(ctx)
	if got := d.get(list[0].ID); got.State != StateCancelled {
		t.Fatalf("an orphaned held job sits forever, taking a live slot: %+v", got.Job)
	}
}

func TestApprovalWithItsLinkLostIsFoundAndApplied(t *testing.T) {
	ctx := context.Background()
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead"})
	job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: t.TempDir(), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	questionID := job.CauseDetail
	// crowsnest died after asking, before the job learnt its question's id.
	d.edit(job.ID, func(rec *store.JobRecord) { rec.CauseDetail = "Asked the human to allow it: elsewhere" })
	if _, err := d.questions.Answer(ctx, questionID, questions.AnswerInput{Value: json.RawMessage(`"allow"`)}); err != nil {
		t.Fatal(err)
	}
	d.svc.Tick(ctx)
	if got := d.get(job.ID); got.State != StateRunning {
		t.Fatalf("an allowed job whose link was lost never started: %+v", got.Job)
	}
}

func TestApprovalAnsweredBeforeItsLinkIsStoredStillApplies(t *testing.T) {
	ctx := context.Background()
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead"})
	// The human answers before askApproval stores the question id on the job.
	d.svc.opts.AskHuman = func(ctx context.Context, input questions.AskInput, caller questions.Caller) (questions.Question, error) {
		q, err := d.questions.Ask(ctx, input, caller)
		if err == nil {
			_, err = d.questions.Answer(ctx, q.ID, questions.AnswerInput{Value: json.RawMessage(`"allow"`)})
		}
		return q, err
	}
	job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: t.TempDir(), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.get(job.ID); got.State != StateRunning {
		t.Fatalf("an answer that beat the link was never applied: %+v", got.Job)
	}
}
