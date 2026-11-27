package jobs

import (
	"context"
	"testing"
	"time"
)

// A job whose agent waits on a tool-call approval (DESIGN.md 8.7) is needs_you/approval-tool: the
// Stop hook lets the turn end, the backstop leaves it alone, the idle check does not mark it
// stuck, and it goes back to running when nothing is pending (also when the callback was lost).
func TestApprovalWaitIsNeedsYouAndUnsupervised(t *testing.T) {
	pending := true
	r := newRig(t, func(opts *Options) {
		opts.ApprovalPending = func(context.Context, string) bool { return pending }
	})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.svc.SetApprovalWaiting(ctx, job.ID, true, "Bash: touch x"); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.State != StateNeedsYou || d.Cause != CauseApprovalTool || d.CauseDetail != "Bash: touch x" || d.CauseText == "" {
		t.Fatalf("waiting: %s/%s %q", d.State, d.Cause, d.CauseDetail)
	}
	reply, err := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{SessionID: job.SessionID})
	if err != nil || reply.Stdout != "" {
		t.Fatalf("a stop while waiting on an approval must not be refused: %q %v", reply.Stdout, err)
	}
	if list, _ := r.svc.BackstopJobs(ctx); len(list) != 0 {
		t.Fatalf("the backstop must leave a job waiting on an approval alone: %+v", list)
	}
	r.clock.Advance(time.Hour)
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.Stuck || d.Nudges != 0 || d.State != StateNeedsYou {
		t.Fatalf("an hour of waiting is not stuck, nudged or idle: %+v", d.Job)
	}
	// The approval settled but the callback was lost: the tick brings the job back.
	pending = false
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateRunning || d.Cause != "" {
		t.Fatalf("nothing pending: %s/%s", d.State, d.Cause)
	}
}

func TestApprovalWaitLeavesOtherDispositionsAlone(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Block", Dir: t.TempDir()})
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "waiting on CI", State: string(StateBlocked), Reason: "CI is down", RecheckIn: "30m"}); err != nil {
		t.Fatal(err)
	}
	_ = r.svc.SetApprovalWaiting(ctx, job.ID, true, "Bash: ls")
	_ = r.svc.SetApprovalWaiting(ctx, job.ID, false, "")
	if d := r.get(job.ID); d.State != StateBlocked {
		t.Fatalf("a blocked job stays blocked: %s", d.State)
	}
}

// The approvals' change callbacks can run out of order: a supersession lands between an
// approval's insert and its "requested" callback, so "requested" runs last. The job must follow
// what is actually pending, not the order the callbacks ran in.
func TestApprovalWaitFollowsTheStoreNotTheCallbackOrder(t *testing.T) {
	pending := false
	r := newRig(t, func(opts *Options) {
		opts.ApprovalPending = func(context.Context, string) bool { return pending }
	})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_ = r.svc.SetApprovalWaiting(ctx, job.ID, false, "")        // the supersession's callback, first
	_ = r.svc.SetApprovalWaiting(ctx, job.ID, true, "Bash: ls") // the late "requested" callback
	if d := r.get(job.ID); d.State != StateRunning || d.Cause != "" {
		t.Fatalf("nothing is pending, yet the job is %s/%s", d.State, d.Cause)
	}
	pending = true
	_ = r.svc.SetApprovalWaiting(ctx, job.ID, false, "Bash: ls") // a stale "settled" hint
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseApprovalTool {
		t.Fatalf("an approval is pending, yet the job is %s/%s", d.State, d.Cause)
	}
}
