package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// TestAllowedLandSurvivesACrash (R6-C10, R5-C09): the human's Allow is a claimed step, not
// consumed before the land runs. A crash after the Allow is recorded and before Land ran leaves
// the claim, and recovery finishes it; a merge that happened before crowsnest recorded it is
// found and recorded, not merged again.
func TestAllowedLandSurvivesACrash(t *testing.T) {
	for _, alreadyMerged := range []bool{false, true} {
		name := "land never ran"
		if alreadyMerged {
			name = "merged but not recorded"
		}
		t.Run(name, func(t *testing.T) {
			l := newLandRequestRig(t)
			ctx := context.Background()
			repo := landRepo(t)
			job := landedJob(t, l.rig, repo)
			question, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), "tests pass")
			if err != nil {
				t.Fatal(err)
			}
			l.answer(question.ID, "allow", "") // the answer is stored; the callback never runs
			// The Allow was recorded as a claim, then crowsnest stopped before Land.
			l.edit(job.ID, func(rec *store.JobRecord) { rec.LandApprovedAt = l.clock.Now() })
			if alreadyMerged {
				gitIn(t, repo, "merge", "-q", "--no-ff", "--no-edit", job.WorktreeBranch)
			}
			l.svc.ReconcileLandRequests(ctx)
			if d := l.get(job.ID); d.LandState != "" || d.LandRequest == "" {
				t.Fatalf("recovery took over a land still within its own process's grace: %+v", d.Job)
			}
			l.clock.Advance(landApprovedGrace + time.Second)
			l.svc.ReconcileLandRequests(ctx)
			d := l.get(job.ID)
			if d.LandState != LandedMerged || d.LandRequest != "" {
				t.Fatalf("the allowed land was lost: %s request %q (%s)", d.LandState, d.LandRequest, d.LandDetail)
			}
			if alreadyMerged != strings.Contains(d.LandDetail, "found merged") {
				t.Fatalf("detail %q", d.LandDetail)
			}
			merges := gitIn(t, repo, "rev-list", "--merges", "--count", "HEAD")
			if merges != "1" {
				t.Fatalf("%s merge commits on the base, want 1", merges)
			}
		})
	}
}

// TestAnAllowedLandCutShortByACrashIsFinished (R10 new-M3): crowsnest stopped mid-land, after the
// land reserved the branch and before it merged. The dead process's reservation is dropped before
// land requests are reconciled, at start (Recover) and on the tick, so the land the human allowed
// is finished rather than refused by that reservation and dropped.
func TestAnAllowedLandCutShortByACrashIsFinished(t *testing.T) {
	for _, path := range []string{"at start", "on the tick"} {
		t.Run(path, func(t *testing.T) {
			l := newLandRequestRig(t)
			ctx := context.Background()
			repo := landRepo(t)
			job := landedJob(t, l.rig, repo)
			question, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), "tests pass")
			if err != nil {
				t.Fatal(err)
			}
			l.answer(question.ID, "allow", "")
			// The Allow was claimed and the land had reserved the branch when crowsnest stopped.
			l.edit(job.ID, func(rec *store.JobRecord) {
				rec.LandApprovedAt = l.clock.Now()
				rec.BranchOp, rec.BranchOpOwner, rec.BranchOpAt = branchLand, "a-dead-process", l.clock.Now()
			})
			l.clock.Advance(landApprovedGrace + time.Second)
			if path == "at start" {
				l.svc.Recover(ctx)
			} else {
				l.svc.Tick(ctx)
			}
			d := l.get(job.ID)
			if d.LandState != LandedMerged || d.LandRequest != "" {
				t.Fatalf("the allowed land was dropped: state %q request %q (%s)\n%s", d.LandState, d.LandRequest, d.LandDetail, timelineText(d))
			}
			if merges := gitIn(t, repo, "rev-list", "--merges", "--count", "HEAD"); merges != "1" {
				t.Fatalf("%s merge commits on the base, want 1", merges)
			}
		})
	}
}
