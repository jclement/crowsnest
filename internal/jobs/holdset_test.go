package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// placeHold puts a hold on a live job as crowsnest would (through a mutation, so the state follows).
func (r *rig) placeHold(id string, kind HoldKind, from string) {
	r.t.Helper()
	if _, err := r.svc.mutate(context.Background(), id, func(rec *store.JobRecord, c *change) error {
		r.svc.placeHoldLocked(rec, c, kind, "held for the test", from)
		return nil
	}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) record(id string) store.JobRecord {
	r.t.Helper()
	record, err := r.db.GetJob(context.Background(), mustParse(id))
	if err != nil {
		r.t.Fatal(err)
	}
	return record
}

// runVerb makes the agent's call for verb on job id, as the socket, MCP or a hook would.
func runVerb(t *testing.T, r *rig, id string, verb AgentVerb) error {
	t.Helper()
	ctx := context.Background()
	switch verb {
	case VerbProgress:
		_, err := r.svc.Update(ctx, id, UpdateInput{Progress: "still at it " + time.Now().String()})
		return err
	case VerbBlocked:
		_, err := r.svc.Update(ctx, id, UpdateInput{Progress: "checking one more thing", State: "blocked", Reason: "checking one more thing", RecheckIn: "1m"})
		return err
	case VerbRunning:
		_, err := r.svc.Update(ctx, id, UpdateInput{Progress: "back", State: "running"})
		return err
	case VerbDone:
		_, err := r.svc.Done(ctx, id, DoneInput{ResultMD: "finished", Verified: []string{}, NotVerified: []string{}})
		return err
	case VerbFail:
		_, err := r.svc.Fail(ctx, id, "giving up")
		return err
	case VerbCreate:
		record := r.record(id)
		_, err := r.svc.CreateForAgent(ctx, CreateInput{Brief: "a child", Dir: record.Dir}, AgentCaller{PaneID: record.PaneID, JobID: id})
		return err
	case VerbAsk:
		r.svc.QuestionChanged(ctx, id, "q_99", "Which one?", "asked", false)
		return nil
	case VerbToolUse:
		_, err := r.svc.HandleHook(ctx, HookPostToolUse, id, "", HookInput{ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"/tmp/x"}`)})
		return err
	case VerbStop:
		r.clock.Advance(time.Second)
		_, err := r.svc.HandleHook(ctx, HookStop, id, "", HookInput{})
		return err
	}
	return fmt.Errorf("unknown verb %s", verb)
}

// TestHoldVerbTable walks every (hold kind × agent verb) cell (R6-C01): a verb the table refuses
// is refused with the hold named and changes nothing; a verb it allows goes through, and no verb
// lifts the hold or moves the job out of the state the hold implies.
func TestHoldVerbTable(t *testing.T) {
	for _, kind := range AllHoldKinds() {
		for _, verb := range AllAgentVerbs() {
			t.Run(string(kind)+"/"+string(verb), func(t *testing.T) {
				r := newRig(t, nil)
				job := r.create(CreateInput{Brief: "Held work", Dir: t.TempDir()})
				if job.State != StateRunning {
					t.Fatalf("setup: %s", job.State)
				}
				r.placeHold(job.ID, kind, "")
				before := r.record(job.ID)
				err := runVerb(t, r, job.ID, verb)
				after := r.record(job.ID)
				allowed := AgentMay(kind, verb)
				switch {
				case allowed && err != nil:
					t.Fatalf("the table allows %s under %s, but it was refused: %v", verb, kind, err)
				case !allowed && err == nil:
					t.Fatalf("the table refuses %s under %s, but it went through (state %s → %s)", verb, kind, before.State, after.State)
				case !allowed && !strings.Contains(err.Error(), string(kind)):
					t.Fatalf("the refusal does not name the hold: %v", err)
				}
				if !hasHold(after, kind) {
					t.Fatalf("%s lifted the %s hold", verb, kind)
				}
				if State(after.State).Terminal() {
					t.Fatalf("%s finished a job held by %s", verb, kind)
				}
				if after.State != before.State || (after.Cause != before.Cause && kind != HoldUsageLimit) {
					t.Fatalf("%s moved the job out of the state %s implies: %s/%s → %s/%s", verb, kind, before.State, before.Cause, after.State, after.Cause)
				}
			})
		}
	}
}

// TestBlockedThenRunningCannotClearAHumanGate is the reviewer's scenario (R6-C01): an agent
// whose final review objects, or whose budget ran out, declares blocked and comes back, then
// calls done. Every step is refused and the gate stays.
func TestBlockedThenRunningCannotClearAHumanGate(t *testing.T) {
	for _, kind := range []HoldKind{HoldReview, HoldBudget, HoldCheck} {
		t.Run(string(kind), func(t *testing.T) {
			r := newRig(t, nil)
			job := r.create(CreateInput{Brief: "Gate", Dir: t.TempDir()})
			r.placeHold(job.ID, kind, "")
			for _, verb := range []AgentVerb{VerbBlocked, VerbRunning, VerbDone} {
				if err := runVerb(t, r, job.ID, verb); err == nil {
					t.Fatalf("%s went through under %s", verb, kind)
				}
			}
			if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != causeOfHold(kind) {
				t.Fatalf("the gate moved: %s/%s", d.State, d.Cause)
			}
		})
	}
}

// TestHoldsSurviveCrowsnestWrites: a hold is applied after every mutation, not only agent ones:
// a hook or recovery path that writes running cannot clear it.
func TestHoldsSurviveCrowsnestWrites(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Budget", Dir: t.TempDir()})
	r.placeHold(job.ID, HoldBudget, "")
	if _, err := r.svc.mutate(ctx, job.ID, func(rec *store.JobRecord, _ *change) error {
		rec.State, rec.Cause, rec.CauseDetail = string(StateRunning), "", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseBudget {
		t.Fatalf("a crowsnest write cleared the hold: %s/%s", d.State, d.Cause)
	}
	// The human's raise is what lifts it.
	if _, err := r.svc.BudgetFinishStep(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateRunning || hasHold(r.record(job.ID), HoldBudget) {
		t.Fatalf("the human's action did not lift it: %s/%s", d.State, d.Cause)
	}
}

// TestParentBudgetHoldsQueuedChild (R6-C05): a child waiting for a slot when its parent's budget
// runs out is held too, does not start when a slot opens (Start anyway included), is still held
// by its own budget, and is let go when the parent's budget is raised.
func TestParentBudgetHoldsQueuedChild(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	parent := r.create(CreateInput{Brief: "Parent", Dir: t.TempDir()})
	child, err := r.svc.Create(ctx, CreateInput{Brief: "Child", Dir: t.TempDir()}, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// As though it is still waiting for a slot.
	r.edit(child.ID, func(rec *store.JobRecord) {
		rec.State, rec.PaneID, rec.AgentName, rec.BriefSent = string(StateQueued), "", "", false
		rec.Cause, rec.CauseDetail = string(CauseHeld), "waiting for a slot: 3 of 3 running"
	})
	r.svc.exceedBelow(ctx, parent.ID, parent.ID, "$2.10 of its $2.00 budget", 0)
	rec := r.record(child.ID)
	hold, ok := holdOf(rec, HoldBudget)
	if !ok || hold.From != parent.ID {
		t.Fatalf("the queued child was not held by its parent's budget: %s", rec.HoldsJSON)
	}
	r.svc.mu.Lock()
	why := r.svc.startHoldLocked(ctx, rec)
	r.svc.mu.Unlock()
	if !strings.HasPrefix(why, holdPrefix) {
		t.Fatalf("the launch would start it: %q", why)
	}
	if _, err := r.svc.StartAnyway(ctx, child.ID); err == nil {
		t.Fatal("Start anyway passed a budget hold")
	}
	// A child job of a held parent cannot be delegated either.
	r.placeHold(parent.ID, HoldBudget, "")
	if err := runVerb(t, r, parent.ID, VerbCreate); err == nil {
		t.Fatal("a parent over its budget delegated a new child")
	}
	// The human raises the parent's budget: the child's hold from it goes.
	big := 100.0
	if _, err := r.svc.SetJobBudget(ctx, parent.ID, BudgetInput{USD: &big}); err != nil {
		t.Fatal(err)
	}
	if rec := r.record(child.ID); hasHold(rec, HoldBudget) {
		t.Fatalf("raising the parent's budget left the child held: %s", rec.HoldsJSON)
	}
}

// TestBudgetHoldsBelowAreRecoveredFromTheParent is R7-C02: crowsnest saved the parent's own budget
// hold and then crashed before the walk reached the queued child and grandchild. The launch reads
// the ancestors and refuses (Start anyway too), and the next tick puts the holds back.
func TestBudgetHoldsBelowAreRecoveredFromTheParent(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	parent := r.create(CreateInput{Brief: "Parent", Dir: t.TempDir()})
	child, err := r.svc.Create(ctx, CreateInput{Brief: "Child", Dir: t.TempDir()}, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := r.svc.Create(ctx, CreateInput{Brief: "Grandchild", Dir: t.TempDir()}, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{child.ID, grandchild.ID} {
		r.edit(id, func(rec *store.JobRecord) {
			rec.State, rec.PaneID, rec.AgentName, rec.BriefSent = string(StateQueued), "", "", false
			rec.Cause, rec.CauseDetail = string(CauseHeld), "waiting for a slot"
		})
	}
	// The parent's own hold, as applyBudget saves it, and no walk after it: the crash.
	r.placeHold(parent.ID, HoldBudget, "")
	r.svc.mu.Lock()
	why := r.svc.startHoldLocked(ctx, r.record(grandchild.ID))
	r.svc.mu.Unlock()
	if !strings.HasPrefix(why, holdPrefix) {
		t.Fatalf("the grandchild would start past its grandparent's budget: %q", why)
	}
	if _, err := r.svc.StartAnyway(ctx, child.ID); err == nil {
		t.Fatal("Start anyway passed a budget hold above the job")
	}
	r.svc.Tick(ctx)
	for _, id := range []string{child.ID, grandchild.ID} {
		if hold, ok := holdOf(r.record(id), HoldBudget); !ok || hold.From != parent.ID {
			t.Fatalf("%s was not held from %s after a tick: %s", id, parent.ID, r.record(id).HoldsJSON)
		}
	}
	// The walk cut short half way: the child held, the grandchild not. A tick finishes it.
	r.edit(grandchild.ID, func(rec *store.JobRecord) { rec.HoldsJSON = "" })
	r.svc.Tick(ctx)
	if !hasHold(r.record(grandchild.ID), HoldBudget) {
		t.Fatal("a walk interrupted below an already-held child was not finished")
	}
}

// TestCheckFailureWaitsForThePause (R6-C07): a check failing while the automation is paused does
// not type anything to the builder; the finding is kept and typed after the resume.
func TestCheckFailureWaitsForThePause(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Check", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.State, rec.AcceptState = string(StateInReview), AcceptChecking
	})
	pause.Set(true)
	typedBefore := len(r.fake.Prompts())
	r.svc.checkFinished(ctx, job.ID, store.CheckRecord{Status: CheckFailed, Command: "exit 3", ExitCode: 3, Commit: "abc"})
	if len(r.fake.Prompts()) != typedBefore {
		t.Fatalf("the check's failure was typed while paused: %v", r.fake.Prompts()[typedBefore:])
	}
	if rec := r.record(job.ID); rec.PendingFeedback == "" {
		t.Fatal("the finding was not kept for later")
	}
	pause.Set(false)
	r.svc.deliverFeedback(ctx, job.ID)
	typed := r.fake.Prompts()[typedBefore:]
	if len(typed) != 1 || !strings.Contains(typed[0].Text, "[crowsnest check]") {
		t.Fatalf("after the resume the finding was not typed once: %v", typed)
	}
	if rec := r.record(job.ID); rec.PendingFeedback != "" {
		t.Fatal("a delivered finding was kept")
	}
}

// TestFinishStepEndsAtTheBackstop (R6-C06): an agent without hooks that the human let finish its
// step is held for the budget again at its next observed end of turn, not nudged on.
func TestFinishStepEndsAtTheBackstop(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Codex work", Dir: t.TempDir()})
	r.placeHold(job.ID, HoldBudget, "")
	r.edit(job.ID, func(rec *store.JobRecord) { rec.BudgetState = BudgetExceeded })
	if _, err := r.svc.BudgetFinishStep(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	typed := len(r.fake.Prompts())
	nudged, err := r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if nudged || len(r.fake.Prompts()) != typed {
		t.Fatal("the backstop nudged a job whose granted step was over")
	}
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseBudget {
		t.Fatalf("after the step: %s/%s", d.State, d.Cause)
	}
	// A second backstop look does not grant another step.
	if nudged, _ := r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID)); nudged {
		t.Fatal("nudged again")
	}
}

// TestWaitCyclesAreRefusedAndBroken (R6-C09): a job may not wait on a job that, through others,
// waits on it; a circle that exists anyway (an older crowsnest) is broken by the invariant check,
// which moves the job to wait on the human and tells them.
func TestWaitCyclesAreRefusedAndBroken(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	a := r.create(CreateInput{Brief: "A", Dir: t.TempDir()})
	b := r.create(CreateInput{Brief: "B", Dir: t.TempDir()})
	c := r.create(CreateInput{Brief: "C", Dir: t.TempDir()})
	block := func(id, on string) error {
		_, err := r.svc.Update(ctx, id, UpdateInput{Progress: "waiting", State: "blocked", Reason: "needs " + on, UntilJob: on})
		return err
	}
	if err := block(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := block(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := block(c.ID, a.ID); err == nil || !strings.Contains(err.Error(), "circle") {
		t.Fatalf("a circular wait was accepted: %v", err)
	}
	// An existing circle (written directly, as an older crowsnest could have left it).
	r.edit(c.ID, func(rec *store.JobRecord) {
		rec.State, rec.WakeKind, rec.WakeJob = string(StateBlocked), WakeJob, mustParse(a.ID)
	})
	r.svc.tickInvariants(ctx)
	broken := 0
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if r.record(id).WakeKind == WakeHuman {
			broken++
		}
	}
	if broken == 0 || len(r.Notices()) == 0 {
		t.Fatalf("the circle was not broken or the human not told (%d broken, notices %v)", broken, r.Notices())
	}
}

// TestCancelledJobCancelsItsQuestions (lead's finding from the real-Claude journeys): cancelling
// a job cancels its open questions; failing one leaves them to the human.
func TestCancelledJobCancelsItsQuestions(t *testing.T) {
	var cancelled []string
	r := newRig(t, func(o *Options) {
		o.CancelQuestions = func(_ context.Context, jobID, reason string) { cancelled = append(cancelled, jobID+": "+reason) }
	})
	ctx := context.Background()
	gone := r.create(CreateInput{Brief: "Cancel me", Dir: t.TempDir()})
	failing := r.create(CreateInput{Brief: "Fail me", Dir: t.TempDir()})
	if _, err := r.svc.Cancel(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Fail(ctx, failing.ID, "cannot"); err != nil {
		t.Fatal(err)
	}
	if len(cancelled) != 1 || !strings.HasPrefix(cancelled[0], gone.ID+": ") {
		t.Fatalf("questions cancelled for %v", cancelled)
	}
}
