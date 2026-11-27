package jobs

import (
	"context"
	"testing"

	"github.com/jclement/crowsnest/internal/store"
)

// TestFixCompletionIsReplayedAfterACrash (R6-C11): a rebase job that finished while crowsnest
// stopped before its parent's re-check is replayed by the tick into exactly one re-check, and a
// second tick (or a late callback) does not run another.
func TestFixCompletionIsReplayedAfterACrash(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
	ctx := context.Background()
	repo := landRepo(t)
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	commitIn(t, repo, "other.md", "someone else's work\n")
	r.edit(job.ID, func(rec *store.JobRecord) { rec.AcceptState = AcceptNeedsRebase })
	fix, err := r.svc.AskRebase(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, job.WorkDir, "merge", "-q", "--no-edit", "main")
	// The fix's done is recorded, but crowsnest "crashes" before the parent hears of it.
	r.edit(fix.ID, func(rec *store.JobRecord) {
		rec.State, rec.ResultMD = string(StateDone), "merged main"
	})
	if d := r.get(job.ID); d.AcceptState != AcceptNeedsRebase {
		t.Fatalf("setup: %s", d.AcceptState)
	}
	if _, err := r.svc.AskRebase(ctx, job.ID); err == nil {
		t.Fatal("another rebase was started while a finished one was not applied")
	}
	r.svc.Tick(ctx)
	d := r.get(job.ID)
	if d.AcceptState != AcceptAccepted {
		t.Fatalf("after the tick: %s (%s)", d.AcceptState, d.AcceptDetail)
	}
	checks, _ := r.db.ListChecks(ctx, mustParse(job.ID), 10)
	r.svc.Tick(ctx)
	r.svc.fixSettled(ctx, r.get(fix.ID).Job) // the lost callback arriving late
	again, _ := r.db.ListChecks(ctx, mustParse(job.ID), 10)
	if len(again) != len(checks) {
		t.Fatalf("the fix was applied twice: %d checks, then %d", len(checks), len(again))
	}
}
