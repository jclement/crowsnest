// Tests for the --demo board: seeded against the demo fleet, each job with a pane is held by the
// agent it names; ten minutes of the demo loop leave what waits on the human alone; and the
// dialog job follows its pane's dialog when the human answers it.

package jobs

import (
	"context"
	"testing"

	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/herdr/fake"
)

func TestDemoBoard(t *testing.T) {
	r := newRig(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	demo := fake.NewDemo()
	panes := DemoPanes{PaneOf: demo.PaneOf, DialogOf: demo.DialogCommand}
	if err := SeedDemo(ctx, r.svc, panes); err != nil {
		t.Fatal(err)
	}
	jobs, err := r.svc.List(ctx, nil)
	if err != nil || len(jobs) != 17 {
		t.Fatalf("%d jobs, %v", len(jobs), err)
	}
	byAgent := map[string]Job{}
	for _, job := range jobs {
		if job.PaneID == "" {
			continue
		}
		byAgent[job.AgentName] = job
		if agent, err := demo.AgentGet(ctx, job.PaneID); err != nil || agent.Name != job.AgentName {
			t.Errorf("%s names %s; its pane holds %q (%v)", job.ID, job.AgentName, agent.Name, err)
		}
	}
	handbook, formKit := byAgent[demostory.HandbookPublish], byAgent[demostory.FormKit]
	if handbook.CauseDetail != "Claude wants to run: rm -rf dist && npm run build" {
		t.Fatalf("the handbook job says %q", handbook.CauseDetail)
	}
	if child := byAgent[demostory.CacheReviewer]; child.ParentID != byAgent[demostory.CacheBuilder].ID || byAgent[demostory.CacheBuilder].Origin != "lead-crowsnest" {
		t.Fatalf("parent %q, origin %q", child.ParentID, byAgent[demostory.CacheBuilder].Origin)
	}

	// Ten minutes of the loop: progress moves, the limit retries, nothing is answered.
	for step := range 30 {
		r.clock.Advance(demoStep)
		demo.Step()
		stepDemo(ctx, r.svc, map[string]string{demostory.WebhookBuilder: byAgent[demostory.WebhookBuilder].ID, demostory.FormKit: formKit.ID}, step)
		reconcileDemoDialog(ctx, r.svc, handbook.ID, panes)
	}
	if got := r.get(handbook.ID); got.State != StateNeedsYou || got.CauseDetail != handbook.CauseDetail {
		t.Fatalf("after ten minutes the handbook job is %s %q", got.State, got.CauseDetail)
	}
	if got := r.get(formKit.ID); !got.Stuck || got.Cause != CauseLimit || got.RetryCount < 2 {
		t.Fatalf("after ten minutes the form kit job is stuck=%v %s, %d retries", got.Stuck, got.Cause, got.RetryCount)
	}

	// The human answers the dialog: the job runs, then waits at the publish dialog that follows.
	if err := demo.AgentSendKeys(ctx, handbook.PaneID, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	reconcileDemoDialog(ctx, r.svc, handbook.ID, panes)
	if got := r.get(handbook.ID); got.State != StateRunning {
		t.Fatalf("after the answer the handbook job is %s", got.State)
	}
	for range 3 {
		demo.Step()
	}
	reconcileDemoDialog(ctx, r.svc, handbook.ID, panes)
	if got := r.get(handbook.ID); got.State != StateNeedsYou || got.CauseDetail != "Claude wants to run: git push --force origin gh-pages" {
		t.Fatalf("the handbook job at its next dialog: %s %q", got.State, got.CauseDetail)
	}
}
