// Tests for the R9 correctness fixes in the jobs service: requested changes hold a finished job
// from landing (R9-C1), one operation on its branch at a time (R9-C2), comments queued under a
// hold reach a blocked job (R9-C3), feedback batches are settled by id and never replayed or lost
// (R9-C4), and a deferred send keeps the human's draft protection (R9-C14).

package jobs

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/store"
)

// finishedJob makes a done job with one commit, accepted by a passing check.
func finishedJob(t *testing.T, r *rig) Detail {
	t.Helper()
	repo := landRepo(t)
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(context.Background(), job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.State != StateDone || d.AcceptState != AcceptAccepted {
		t.Fatalf("setup: %s %s", d.State, d.AcceptState)
	}
	return d
}

// settledRecorder captures what FeedbackSettled is told.
type settledRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (s *settledRecorder) hook(_ context.Context, ref string, outcome FeedbackOutcome, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, ref+"="+string(outcome))
}

func (s *settledRecorder) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.seen...)
}

// TestRequestedChangesHoldTheJobFromLanding (R9-C1): a done, accepted job sent back with comments
// does not land on its old acceptance, not while the fix waits (paused), not after the fix fails,
// and not after the fix is done until the human accepts the changes.
func TestRequestedChangesHoldTheJobFromLanding(t *testing.T) {
	pause := &fakePause{}
	r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
	r.svc.opts.Pause = pause
	ctx := context.Background()
	job := finishedJob(t, r)
	if why := r.svc.landBlocked(r.get(job.ID).Job, LandMerge, nil); why != "" {
		t.Fatalf("setup: an accepted job should be landable: %s", why)
	}
	pause.Set(true)
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil || delivery.How != CommentFixJob {
		t.Fatalf("delivery = %+v %v", delivery, err)
	}
	parent := r.get(job.ID)
	if parent.ChangesState != ChangesRequested || parent.FixJob != delivery.FixJob {
		t.Fatalf("parent after the send: changes %q fix %q", parent.ChangesState, parent.FixJob)
	}
	for _, action := range []string{LandMerge, LandPR, LandDiscard} {
		if _, err := r.svc.Land(ctx, job.ID, action, "you"); err == nil || !strings.Contains(err.Error(), delivery.FixJob) {
			t.Fatalf("%s while the fix is queued: %v", action, err)
		}
	}
	// The fix job does not finish: the request stays, and Land still refuses.
	if _, err := r.svc.Cancel(ctx, delivery.FixJob); err != nil {
		t.Fatal(err)
	}
	parent = r.get(job.ID)
	if parent.ChangesState != ChangesFixFailed || parent.FixJob != "" {
		t.Fatalf("after the fix was cancelled: changes %q fix %q", parent.ChangesState, parent.FixJob)
	}
	if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("merge after a failed fix: %v", err)
	}
	// Send again: a new fix job, which finishes.
	pause.Set(false)
	delivery, err = r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil || delivery.How != CommentFixJob {
		t.Fatalf("second send = %+v %v", delivery, err)
	}
	commitIn(t, job.WorkDir, "widget.go", "package widget\n\n// Sum is renamed.\n")
	if _, err := r.svc.Done(ctx, delivery.FixJob, done); err != nil {
		t.Fatal(err)
	}
	parent = r.get(job.ID)
	if parent.ChangesState != ChangesMade || parent.AcceptState != AcceptAccepted {
		t.Fatalf("after the fix and its re-check: changes %q accept %q", parent.ChangesState, parent.AcceptState)
	}
	if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err == nil || !strings.Contains(err.Error(), "accept the changes") {
		t.Fatalf("merge before the human accepted the changes: %v", err)
	}
	if _, err := r.svc.ResolveChanges(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err != nil || result.Job.LandState != LandedMerged {
		t.Fatalf("merge after accepting: %+v %v", result, err)
	}
}

// TestOneFixAtATime (R9-C2): sends racing for one finished job start one fix job; a fix cannot
// start while the check runs, and the check cannot start while a fix works.
func TestOneFixAtATime(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
	ctx := context.Background()
	job := finishedJob(t, r)
	var wg sync.WaitGroup
	var mu sync.Mutex
	started := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.svc.AskChanges(ctx, job.ID, reviewBody, 1); err == nil {
				mu.Lock()
				started++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	fixes, err := r.svc.opts.Store.ListFixJobs(ctx, mustParse(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 || len(fixes) != 1 {
		t.Fatalf("racing sends started %d fix jobs (%d recorded), want 1", started, len(fixes))
	}
	if _, err := r.svc.Recheck(ctx, job.ID); err == nil || !strings.Contains(err.Error(), "making the changes") {
		t.Fatalf("a recheck while the fix works: %v", err)
	}
	if _, err := r.svc.AskRebase(ctx, job.ID); err == nil {
		t.Fatal("a rebase started beside a running fix")
	}
	if _, err := r.svc.Cancel(ctx, FormatID(fixes[0].ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ResolveChanges(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	// While the check runs, no fix job starts.
	r.edit(job.ID, func(rec *store.JobRecord) { rec.AcceptState = AcceptChecking })
	if _, err := r.svc.AskChanges(ctx, job.ID, reviewBody, 1); err == nil || !strings.Contains(err.Error(), "running the check") {
		t.Fatalf("a fix job during the check: %v", err)
	}
}

// TestALiveFixIsAdoptedAfterACrash (R9-C2): a fix job inserted just before crowsnest stopped, its
// parent's reservation never saved, owns the branch after the tick.
func TestALiveFixIsAdoptedAfterACrash(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
	ctx := context.Background()
	job := finishedJob(t, r)
	fix, err := r.svc.AskChanges(ctx, job.ID, reviewBody, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.edit(job.ID, func(rec *store.JobRecord) { rec.BranchOp, rec.ChangesState = "", "" })
	r.svc.Tick(ctx)
	parent := r.get(job.ID)
	if parent.FixJob != fix.ID || parent.ChangesState != ChangesRequested {
		t.Fatalf("after the tick: fix %q changes %q", parent.FixJob, parent.ChangesState)
	}
	// A land a dead process left is dropped.
	if _, err := r.svc.Cancel(ctx, fix.ID); err != nil {
		t.Fatal(err)
	}
	r.edit(job.ID, func(rec *store.JobRecord) { rec.BranchOp, rec.BranchOpOwner = branchLand, "a-dead-process" })
	r.svc.Tick(ctx)
	if rec := r.record(job.ID); rec.BranchOp != "" {
		t.Fatalf("a dead land reservation stayed: %q", rec.BranchOp)
	}
}

// TestPausedCommentsReachABlockedJob (R9-C3): comments sent to a blocked job while paused are
// typed once the pause ends, without another wake.
func TestPausedCommentsReachABlockedJob(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.State, rec.WakeKind = string(StateBlocked), "human" })
	pause.Set(true)
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil || delivery.How != CommentWaiting {
		t.Fatalf("delivery = %+v %v", delivery, err)
	}
	r.svc.Tick(ctx)
	if n := promptsContaining(r, reviewBody); n != 0 {
		t.Fatalf("typed while paused (%d)", n)
	}
	if d := r.get(job.ID); len(d.Feedback) != 1 || d.Feedback[0].Kind != FeedbackComments {
		t.Fatalf("the waiting comments are not on the job: %+v", d.Feedback)
	}
	pause.Set(false)
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "[crowsnest review comments "+job.ID+"] "+reviewBody); n != 1 {
		t.Fatalf("after the resume the comments were typed %d times, want 1", n)
	}
	if d := r.get(job.ID); len(d.Feedback) != 0 || d.State != StateBlocked {
		t.Fatalf("after delivery: feedback %+v, state %s", d.Feedback, d.State)
	}
}

// TestFeedbackBatchesSettleByID (R9-C4): comments appended while a batch is being typed are their
// own batch: the first is not typed again with them, and a crash during the first settles only it.
func TestFeedbackBatchesSettleByID(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	recorder := &settledRecorder{}
	r.svc.SetFeedbackSettled(recorder.hook)
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	pause.Set(true)
	if _, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: "FIRST-BATCH", Count: 1, Ref: "send:a"}); err != nil {
		t.Fatal(err)
	}
	pause.Set(false)
	// A delivery claims the first batch and is typing it ...
	var batch feedbackBatch
	cl, err := r.svc.claimStep(ctx, job.ID, opFeedback, func(rec *store.JobRecord, _ *change) error {
		batch, _ = nextFeedback(*rec)
		markFeedback(rec, batch.ID, feedbackSending)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// ... when another send queues behind it (the job's feedback is busy).
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: "SECOND-BATCH", Count: 1, Ref: "send:b"})
	if err != nil || delivery.How != CommentWaiting {
		t.Fatalf("second send = %+v %v", delivery, err)
	}
	r.svc.settleFeedback(ctx, cl, batch, typedYes, nil)
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "FIRST-BATCH"); n != 0 {
		t.Fatalf("the first batch was typed again (%d) after it settled", n)
	}
	if n := promptsContaining(r, "SECOND-BATCH"); n != 1 {
		t.Fatalf("the second batch was typed %d times, want 1", n)
	}
	if got := strings.Join(recorder.all(), " "); got != "send:a=delivered send:b=delivered" {
		t.Fatalf("settled = %s", got)
	}

	// A crowsnest that died typing THIRD leaves FOURTH queued; recovery settles THIRD alone.
	pause.Set(true)
	for _, send := range []CommentSend{{Body: "THIRD-BATCH", Count: 1, Ref: "send:c"}, {Body: "FOURTH-BATCH", Count: 1, Ref: "send:d"}} {
		if _, err := r.svc.SendReviewComments(ctx, job.ID, send); err != nil {
			t.Fatal(err)
		}
	}
	r.edit(job.ID, func(rec *store.JobRecord) {
		third, _ := nextFeedback(*rec)
		markFeedback(rec, third.ID, feedbackSending)
		rec.LifecycleOp, rec.LifecycleOwner, rec.LifecycleAt = opFeedback, "a-dead-process", r.clock.Now().Add(-time.Minute)
	})
	pause.Set(false)
	r.svc.Tick(ctx)
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "THIRD-BATCH"); n != 0 {
		t.Fatalf("a batch that may have arrived was typed again (%d)", n)
	}
	if n := promptsContaining(r, "FOURTH-BATCH"); n != 1 {
		t.Fatalf("the batch behind it was typed %d times, want 1 (lost on recovery?)", n)
	}
	if got := strings.Join(recorder.all(), " "); !strings.Contains(got, "send:c=uncertain") || !strings.Contains(got, "send:d=delivered") {
		t.Fatalf("settled = %s", got)
	}
}

// TestCommentsLeftWhenTheJobFinishesGoBack (R9-C4): comments queued on a job that finishes before
// they could be typed go back to the human, never silently dropped.
func TestCommentsLeftWhenTheJobFinishesGoBack(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	recorder := &settledRecorder{}
	r.svc.SetFeedbackSettled(recorder.hook)
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	pause.Set(true)
	if _, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 1, Ref: "send:z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(recorder.all(), " "); got != "send:z=returned" {
		t.Fatalf("settled = %q", got)
	}
}

// TestDeferredCommentsKeepDraftProtection (R9-C14): comments sent without "Send anyway" while
// held are not typed over a draft in the input box when the hold ends; Send anyway lets them.
func TestDeferredCommentsKeepDraftProtection(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	pause.Set(true)
	if _, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2}); err != nil {
		t.Fatal(err)
	}
	draft, err := os.ReadFile("../fleet/testdata/screens/claude_idle_draft.ansi")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.fake.SetScreen(job.PaneID, fleet.StripANSI(string(draft)), string(draft)); err != nil {
		t.Fatal(err)
	}
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	pause.Set(false)
	r.svc.Tick(ctx)
	if n := promptsContaining(r, reviewBody); n != 0 {
		t.Fatalf("deferred comments were typed over the human's draft (%d)", n)
	}
	if !strings.Contains(timelineText(r.get(job.ID)), "left a draft") {
		t.Fatalf("the timeline does not say why they wait: %s", timelineText(r.get(job.ID)))
	}
	forced, err := r.svc.ForceWaitingComments(ctx, job.ID)
	if err != nil || forced != 2 {
		t.Fatalf("send anyway = %d %v", forced, err)
	}
	if n := promptsContaining(r, reviewBody); n != 1 {
		t.Fatalf("after Send anyway the comments were typed %d times, want 1", n)
	}
}

// TestBudgetContinuationsPassTheGate (R7-C04 residual): raising a held job's budget, or letting it
// finish its step, does not type "carry on" through the pause or another hold.
func TestBudgetContinuationsPassTheGate(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.BudgetState = BudgetExceeded })
	r.placeHold(job.ID, HoldBudget, "")
	pause.Set(true)
	big := 100.0
	if _, err := r.svc.SetJobBudget(ctx, job.ID, BudgetInput{USD: &big}); err != nil {
		t.Fatal(err)
	}
	if n := promptsContaining(r, "[crowsnest budget]"); n != 0 {
		t.Fatalf("the raise typed into a paused job (%d)", n)
	}
	r.placeHold(job.ID, HoldBudget, "")
	if _, err := r.svc.BudgetFinishStep(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if n := promptsContaining(r, "[crowsnest budget]"); n != 0 {
		t.Fatalf("finish the step typed into a paused job (%d)", n)
	}
	pause.Set(false)
	r.placeHold(job.ID, HoldBudget, "")
	if _, err := r.svc.SetJobBudget(ctx, job.ID, BudgetInput{USD: &big}); err != nil {
		t.Fatal(err)
	}
	if n := promptsContaining(r, "raised job "+job.ID+"'s budget"); n != 1 {
		t.Fatalf("unpaused, the raise was typed %d times, want 1", n)
	}
}

// TestHeldRecordSeesHoldsAndFeedback: a standing agent's pane holding a held job is busy.
func TestHeldRecordSeesHoldsAndFeedback(t *testing.T) {
	var r store.JobRecord
	if HeldRecord(r) {
		t.Fatal("an empty record is held")
	}
	setHolds(&r, []Hold{{Kind: HoldBudget, Lift: LiftHuman}})
	if !HeldRecord(r) {
		t.Fatal("a budget hold is not seen")
	}
	setHolds(&r, nil)
	queueFinding(&r, FeedbackCheck, "x", time.Now())
	if !HeldRecord(r) {
		t.Fatal("waiting feedback is not seen")
	}
}
