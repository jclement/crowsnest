// Tests for sending review comments from the diff to a job (comments.go): the human's message path
// for a free job, the pending feedback held by a hold or the pause and typed once it ends, a fix
// job for a finished job, and the refusals.

package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/store"
)

const reviewBody = "Review comments on your changes: 2 comments from the diff view."

func TestReviewCommentsToARunningJobAreAMessage(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.How != CommentTyped && delivery.How != CommentQueued {
		t.Fatalf("delivery = %+v", delivery)
	}
	if n := promptsContaining(r, "[crowsnest message "+job.ID+"] "+reviewBody); n != 1 {
		t.Fatalf("typed %d times, want once through the message path", n)
	}
	if !strings.Contains(timelineText(r.get(job.ID)), "You sent 2 review comments from the diff") {
		t.Fatalf("timeline lacks the send: %s", timelineText(r.get(job.ID)))
	}
}

func TestReviewCommentsWaitForAHold(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.BudgetState = BudgetExceeded })
	r.placeHold(job.ID, HoldBudget, "")
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.How != CommentWaiting || !strings.Contains(delivery.Why, "budget") {
		t.Fatalf("delivery = %+v", delivery)
	}
	if n := promptsContaining(r, reviewBody); n != 0 {
		t.Fatalf("typed into a job its budget holds (%d)", n)
	}
	r.svc.Tick(ctx)
	if n := promptsContaining(r, reviewBody); n != 0 {
		t.Fatalf("the tick typed into a held job (%d)", n)
	}
	big := 100.0
	if _, err := r.svc.SetJobBudget(ctx, job.ID, BudgetInput{USD: &big}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "[crowsnest review comments "+job.ID+"] "+reviewBody); n != 1 {
		t.Fatalf("after the raise the comments were typed %d times, want 1", n)
	}
	if rec := r.record(job.ID); hasFeedback(rec) {
		t.Fatalf("still pending: %q", rec.FeedbackJSON)
	}
}

func TestReviewCommentsWaitForThePause(t *testing.T) {
	pause := &fakePause{}
	r := newRig(t, func(o *Options) { o.Pause = pause })
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	pause.Set(true)
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil || delivery.How != CommentWaiting {
		t.Fatalf("delivery = %+v %v", delivery, err)
	}
	r.svc.Tick(ctx)
	if n := promptsContaining(r, reviewBody); n != 0 {
		t.Fatalf("typed while paused (%d)", n)
	}
	pause.Set(false)
	r.svc.Tick(ctx)
	if n := promptsContaining(r, reviewBody); n != 1 {
		t.Fatalf("after resume typed %d times, want 1", n)
	}
}

func TestReviewCommentsLiftAFinalReviewsObjection(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	r.placeHold(job.ID, HoldReview, "")
	if d := r.get(job.ID); d.State != StateNeedsYou {
		t.Fatalf("setup: %s", d.State)
	}
	if _, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateRunning {
		t.Fatalf("after the comments: %s (%s)", d.State, d.Cause)
	}
	if n := promptsContaining(r, reviewBody); n != 1 {
		t.Fatalf("typed %d times", n)
	}
}

func TestReviewCommentsOnAFinishedJobStartAFixJob(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
	ctx := context.Background()
	repo := landRepo(t)
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateDone {
		t.Fatalf("setup: %s", d.State)
	}
	plan, err := r.svc.PlanReviewComments(ctx, job.ID)
	if err != nil || plan.Route != CommentFixJob {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	delivery, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2})
	if err != nil || delivery.How != CommentFixJob || delivery.FixJob == "" {
		t.Fatalf("delivery = %+v %v", delivery, err)
	}
	fix := r.get(delivery.FixJob)
	if fix.FixOf != job.ID || fix.WorkDir != job.WorkDir || !strings.Contains(fix.BriefMD, reviewBody) {
		t.Fatalf("fix job = %+v", fix.Job)
	}
	if d := r.get(job.ID); d.State != StateDone {
		t.Fatalf("the finished job changed state: %s", d.State)
	}
	if _, err := r.svc.SendReviewComments(ctx, job.ID, CommentSend{Body: reviewBody, Count: 2}); err == nil || !strings.Contains(err.Error(), "is making the changes") {
		t.Fatalf("a second fix job while one runs: %v", err)
	}
}

func TestReviewCommentsRefusals(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	inReview := r.create(CreateInput{Brief: "Checked", Dir: t.TempDir()})
	r.edit(inReview.ID, func(rec *store.JobRecord) { rec.State = string(StateInReview) })
	cancelled := r.create(CreateInput{Brief: "Gone", Dir: t.TempDir()})
	if _, err := r.svc.Cancel(ctx, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	doneNoWorktree := r.create(CreateInput{Brief: "Dir job", Dir: t.TempDir()})
	r.edit(doneNoWorktree.ID, func(rec *store.JobRecord) { rec.State = string(StateDone) })
	for id, want := range map[string]string{inReview.ID: "checking or reviewing", cancelled.ID: "cancelled", doneNoWorktree.ID: "no worktree"} {
		_, err := r.svc.SendReviewComments(ctx, id, CommentSend{Body: reviewBody, Count: 1})
		if err == nil || !IsValidation(err) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", id, err, want)
		}
	}
	if _, err := r.svc.SendReviewComments(ctx, "j_999", CommentSend{Body: reviewBody, Count: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
	if n := promptsContaining(r, reviewBody); n != 0 {
		t.Fatalf("a refused send typed something (%d)", n)
	}
}

func timelineText(detail Detail) string {
	var out strings.Builder
	for _, event := range detail.Timeline {
		out.WriteString(event.Summary + "\n")
	}
	return out.String()
}

// TestAFixJobLeavesAnUnverifiedJobUnverified: with no check to run, the fix job's end does not
// start a check that cannot run (it showed "Check failed" in the real-Claude journey).
func TestAFixJobLeavesAnUnverifiedJobUnverified(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
	ctx := context.Background()
	repo := landRepo(t)
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	r.edit(job.ID, func(rec *store.JobRecord) { rec.State, rec.AcceptState = string(StateDone), AcceptUnverified })
	fix, err := r.svc.AskChanges(ctx, job.ID, reviewBody, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.edit(fix.ID, func(rec *store.JobRecord) { rec.State = string(StateDone) })
	r.svc.fixSettled(ctx, r.get(fix.ID).Job)
	if d := r.get(job.ID); d.AcceptState != AcceptUnverified || d.State != StateDone {
		t.Fatalf("after the fix: %s, %s (%s)", d.State, d.AcceptState, d.AcceptDetail)
	}
	if checks, _ := r.db.ListChecks(ctx, mustParse(job.ID), 10); len(checks) != 0 {
		t.Fatalf("a check ran: %+v", checks)
	}
}
