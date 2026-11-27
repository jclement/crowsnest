// Tests for the review gate's recovery (R5-C05), its human decision (R5-C07) and the pause
// (R5-C08).

package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func reviewerOfRound(t *testing.T, r *rig, jobID string, round int) string {
	t.Helper()
	reviewers, _ := r.db.ListReviewJobs(context.Background(), mustParse(jobID))
	for i := len(reviewers) - 1; i >= 0; i-- {
		if reviewers[i].ReviewRound == round {
			return FormatID(reviewers[i].ID)
		}
	}
	return ""
}

var approve = DoneInput{ResultMD: "VERDICT: approve\nLooks right.", Verified: []string{}, NotVerified: []string{}}
var changes = DoneInput{ResultMD: "VERDICT: changes\n- widget.go:3 no error check", Verified: []string{}, NotVerified: []string{}}

// TestReviewMissingReviewerIsStarted: crowsnest stopped after recording the round but before
// starting its reviewer. The tick starts one, after a grace for the one Done is starting.
func TestReviewMissingReviewerIsStarted(t *testing.T) {
	r := newLandRig(t, LandingOptions{ReviewDefault: ReviewDefaultSame}, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	if _, err := r.svc.mutate(ctx, job.ID, func(rec *store.JobRecord, _ *change) error {
		rec.State, rec.ReviewRound, rec.LastProgressAt = string(StateInReview), 1, r.clock.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if reviewerOfRound(t, r, job.ID, 1) != "" {
		t.Fatal("a reviewer was started inside the grace")
	}
	r.clock.Advance(2 * reviewAskGrace)
	r.svc.Tick(ctx)
	reviewer := reviewerOfRound(t, r, job.ID, 1)
	if reviewer == "" {
		t.Fatal("the reconciler did not start the missing reviewer")
	}
	r.svc.Tick(ctx)
	if reviewers, _ := r.db.ListReviewJobs(ctx, mustParse(job.ID)); len(reviewers) != 1 {
		t.Fatalf("a second reviewer for the same round: %d", len(reviewers))
	}
	if _, err := r.svc.Done(ctx, reviewer, approve); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateDone {
		t.Fatalf("the started reviewer's approval did not finish the job: %s", d.State)
	}
}

// TestReviewSettledVerdictIsReplayed: the reviewer settled but its verdict was never applied
// (crowsnest stopped in between). The tick applies it, once.
func TestReviewSettledVerdictIsReplayed(t *testing.T) {
	r := newLandRig(t, LandingOptions{ReviewDefault: ReviewDefaultSame}, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	reviewer := reviewerOfRound(t, r, job.ID, 1)
	if _, err := r.svc.mutate(ctx, reviewer, func(rec *store.JobRecord, _ *change) error {
		rec.State, rec.ResultMD = string(StateDone), approve.ResultMD // no afterSettled: "the crash"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateInReview {
		t.Fatalf("the parent moved without the callback: %s", d.State)
	}
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateDone || d.ReviewVerdict != VerdictApprove {
		t.Fatalf("the settled verdict was not applied: %s %s", d.State, d.ReviewVerdict)
	}
}

// TestFailedFinalReviewNeedsTheHuman is R5-C07: after the last round objects, the builder's
// done is refused; the human's message sends it back, and only then is done accepted.
func TestFailedFinalReviewNeedsTheHuman(t *testing.T) {
	r := newLandRig(t, LandingOptions{ReviewDefault: ReviewDefaultSame}, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	for round := 1; round <= maxReviewRounds; round++ {
		if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
			t.Fatal(err)
		}
		if _, err := r.svc.Done(ctx, reviewerOfRound(t, r, job.ID, round), changes); err != nil {
			t.Fatal(err)
		}
	}
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseReview {
		t.Fatalf("after the last objecting round: %s %s", d.State, d.Cause)
	}
	if _, err := r.svc.Done(ctx, job.ID, done); err == nil || !strings.Contains(err.Error(), "human's decision") {
		t.Fatalf("an agent's done cleared the failed review: %v", err)
	}
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseReview {
		t.Fatalf("the refused done changed the job: %s %s", d.State, d.Cause)
	}
	if _, err := r.svc.Message(ctx, job.ID, MessageInput{Text: "Fix the error check, then finish."}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateRunning {
		t.Fatalf("the human's message did not send it back: %s %s", d.State, d.Cause)
	}
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatalf("done after the human sent it back: %v", err)
	}
}

// TestReviewFindingsWaitForResume is R5-C08: a reviewer that asks for changes while paused does
// not start a builder turn; the findings go at the first tick after the resume.
func TestReviewFindingsWaitForResume(t *testing.T) {
	pause := &fakePause{}
	r := newLandRig(t, LandingOptions{ReviewDefault: ReviewDefaultSame}, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	reviewer := reviewerOfRound(t, r, job.ID, 1)
	pause.Set(true)
	before := len(r.fake.Prompts())
	if _, err := r.svc.Done(ctx, reviewer, changes); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateInReview {
		t.Fatalf("paused, the builder was sent back: %s", d.State)
	}
	if len(r.fake.Prompts()) != before {
		t.Fatalf("paused, something was typed: %+v", r.fake.Prompts()[before:])
	}
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateInReview {
		t.Fatalf("paused, the tick sent it back: %s", d.State)
	}
	pause.Set(false)
	r.clock.Advance(time.Second)
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateRunning || d.ReviewVerdict != VerdictChanges {
		t.Fatalf("after the resume: %s %s", d.State, d.ReviewVerdict)
	}
	prompts := r.fake.Prompts()
	if last := prompts[len(prompts)-1]; !strings.HasPrefix(last.Text, "[crowsnest review "+reviewer+"]") {
		t.Fatalf("the findings were not typed after the resume: %+v", last)
	}
}
