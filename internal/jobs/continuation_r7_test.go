// Tests for the continuation gate (R7-C04) and the claimed delivery of pending findings (R7-C05):
// no automatic prompt starts a turn on a held job, and findings are typed by exactly one owner,
// never replayed after a crash mid-send.

package jobs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func promptsContaining(r *rig, text string) int {
	n := 0
	for _, prompt := range r.fake.Prompts() {
		if strings.Contains(prompt.Text, text) {
			n++
		}
	}
	return n
}

// TestTimedWakeWaitsForAHold: a blocked job under a usage-limit hold keeps its wake-up; the wake
// that comes due types nothing and leaves it blocked, and once the hold is lifted it wakes.
func TestTimedWakeWaitsForAHold(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Wait on CI", Dir: t.TempDir()})
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "waiting", State: "blocked", Reason: "CI", RecheckIn: "1m"}); err != nil {
		t.Fatal(err)
	}
	r.placeHold(job.ID, HoldUsageLimit, "")
	r.clock.Advance(2 * time.Minute)
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "[crowsnest recheck]"); n != 0 {
		t.Fatalf("the wake-up was typed into a held job (%d)", n)
	}
	if rec := r.record(job.ID); rec.WakeKind != WakeTime {
		t.Fatalf("the held job lost its wake-up: %+v", rec.WakeKind)
	}
	if _, err := r.svc.mutate(ctx, job.ID, func(rec *store.JobRecord, c *change) error {
		liftHoldLocked(rec, c, HoldUsageLimit, LiftCrowsnest, nil)
		rec.Stuck, rec.Cause, rec.CauseDetail = false, "", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "[crowsnest recheck]"); n != 1 {
		t.Fatalf("after the hold went, the wake-up was typed %d times, want 1", n)
	}
}

// TestReviewFindingsWaitForALatentBudgetHold: a budget reached while the reviewer ran holds the
// job when the changes verdict comes back; the findings are kept, not typed, and go once the
// human raises the budget.
func TestReviewFindingsWaitForALatentBudgetHold(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.State, rec.ReviewRound, rec.BudgetState = string(StateInReview), 1, BudgetExceeded
	})
	r.placeHold(job.ID, HoldReviewing, "")
	r.placeHold(job.ID, HoldBudget, "")
	r.svc.sendBackForChanges(ctx, r.get(job.ID), Job{ID: "j_99"}, "Fix the off-by-one.", "")
	if n := promptsContaining(r, "asked for changes"); n != 0 {
		t.Fatalf("the findings were typed into a job held by its budget (%d)", n)
	}
	rec := r.record(job.ID)
	if rec.PendingFeedback == "" || State(rec.State) != StateNeedsYou {
		t.Fatalf("after the verdict: %s, pending %q", rec.State, rec.PendingFeedback)
	}
	big := 100.0
	if _, err := r.svc.SetJobBudget(ctx, job.ID, BudgetInput{USD: &big}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if n := promptsContaining(r, "asked for changes"); n != 1 {
		t.Fatalf("after the raise the findings were typed %d times, want 1", n)
	}
	if rec := r.record(job.ID); rec.PendingFeedback != "" {
		t.Fatalf("delivered findings still pending: %q", rec.PendingFeedback)
	}
}

// TestLongWaitCirclesAreFound is R7-C06: a ring of 33 jobs, closed by its last edge, is refused
// (a 32-hop walk used to accept it), and a 40-job ring left by an older crowsnest is found by the
// invariant check and broken.
func TestLongWaitCirclesAreFound(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	const ring = 40
	ids := make([]string, ring)
	for i := range ids {
		ids[i] = r.create(CreateInput{Brief: "Ring", Dir: t.TempDir()}).ID
	}
	block := func(id, on string) error {
		_, err := r.svc.Update(ctx, id, UpdateInput{Progress: "waiting", State: "blocked", Reason: "needs " + on, UntilJob: on})
		return err
	}
	for i := 0; i < 32; i++ {
		if err := block(ids[i], ids[i+1]); err != nil {
			t.Fatalf("edge %d: %v", i, err)
		}
	}
	if err := block(ids[32], ids[0]); err == nil || !strings.Contains(err.Error(), "circle") {
		t.Fatalf("closing a 33-job circle: %v", err)
	}
	// An existing 40-job circle, written directly.
	for i := 32; i < ring; i++ {
		next := ids[(i+1)%ring]
		r.edit(ids[i], func(rec *store.JobRecord) {
			rec.State, rec.WakeKind, rec.WakeJob = string(StateBlocked), WakeJob, mustParse(next)
		})
	}
	r.svc.tickInvariants(ctx)
	broken := 0
	for _, id := range ids {
		if r.record(id).WakeKind == WakeHuman {
			broken++
		}
	}
	if broken == 0 {
		t.Fatal("a 40-job circle was not found")
	}
}

// TestFindingsHaveOneOwner: the check's callback, the review's and the tick may all try to deliver
// the same findings at once; they are typed once. A delivery a dead crowsnest left in flight is
// settled as "may have arrived", not typed again.
func TestFindingsHaveOneOwner(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Build it", Dir: t.TempDir()})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.PendingFeedback = "[crowsnest check] FINDINGS-ONE" })
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.svc.deliverFeedback(ctx, job.ID)
		}()
	}
	wg.Wait()
	if n := promptsContaining(r, "FINDINGS-ONE"); n != 1 {
		t.Fatalf("the findings were typed %d times, want 1", n)
	}

	// A claim from a crowsnest that died while typing.
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.PendingFeedback = "[crowsnest check] FINDINGS-TWO"
		rec.LifecycleOp, rec.LifecycleOwner, rec.LifecycleAt = opFeedback, "a-dead-process", r.clock.Now().Add(-time.Minute)
	})
	r.svc.Tick(ctx)
	r.svc.deliverFeedback(ctx, job.ID)
	if n := promptsContaining(r, "FINDINGS-TWO"); n != 0 {
		t.Fatalf("findings that may have arrived were typed again (%d)", n)
	}
	if rec := r.record(job.ID); rec.PendingFeedback != "" || rec.LifecycleOp != "" {
		t.Fatalf("the stale delivery was not settled: pending %q, op %q", rec.PendingFeedback, rec.LifecycleOp)
	}
}
