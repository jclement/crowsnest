// Regression tests for the R3 review's supervisor findings (reviews/R3-codex.md). Each sets up
// the reviewer's failure scenario and asserts the fixed behaviour; REPORT.md says how each was
// seen to fail with the old code.

package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
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
