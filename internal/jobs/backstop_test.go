// The backstop against the real jobs service (DESIGN.md 6.3): one stop is never nudged twice.
// A stop whose Stop hook ran (and was nudged by it) is left alone by the herdr-side backstop; a
// stop with no hook is nudged by the backstop, under the same budget.

package jobs

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
)

const emptyInputScreen = "\x1b[38;5;244m────────────────────────────────────────\x1b[39m\n❯ \x1b[7m \x1b[27m\n\x1b[38;5;244m────────────────────────────────────────\x1b[39m\n"

// backstopPoker is a poker running only the backstop, on the rig's clock and fake herdr.
func backstopPoker(r *rig) *orch.Poker {
	return orch.NewPoker(orch.PokerOptions{
		Client: r.fake, KV: r.db, Backstop: r.svc, Now: r.clock.Now, Logger: slog.New(slog.DiscardHandler),
		Status: func() orch.Status { return orch.Status{State: orch.StateDisabled} },
	})
}

func nudgesTyped(r *rig, pane string) int {
	count := 0
	for _, prompt := range r.fake.Prompts() {
		if prompt.PaneID == pane && strings.HasPrefix(prompt.Text, "[crowsnest nudge]") {
			count++
		}
	}
	return count
}

func TestBackstopAndHookNeverNudgeTheSameStop(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if err := r.fake.SetScreen(job.PaneID, fleet.StripANSI(emptyInputScreen), emptyInputScreen); err != nil {
		t.Fatal(err)
	}
	poker := backstopPoker(r)
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	poker.Check(ctx, false)

	// The turn ends: the Stop hook refuses it (a nudge through the hook), and herdr then shows
	// the agent idle (as it does if Claude ignores the refusal and stops anyway).
	reply, err := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{})
	if err != nil || !strings.Contains(reply.Stdout, `"decision":"block"`) {
		t.Fatalf("hook reply %+v %v", reply, err)
	}
	r.clock.Advance(2 * time.Second)
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	poker.Check(ctx, false)
	r.clock.Advance(3 * orch.BackstopIdle)
	poker.Check(ctx, false)
	if typed := nudgesTyped(r, job.PaneID); typed != 0 {
		t.Fatalf("the backstop nudged a stop the hook handled: %d nudges typed", typed)
	}
	if d := r.get(job.ID); d.Nudges != 1 {
		t.Fatalf("nudges %d, want the hook's one", d.Nudges)
	}

	// Next turn, the hook does not run (it failed): the backstop nudges, on the same budget.
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	poker.Check(ctx, false)
	r.clock.Advance(time.Minute)
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	poker.Check(ctx, false)
	r.clock.Advance(orch.BackstopIdle)
	poker.Check(ctx, false)
	if typed := nudgesTyped(r, job.PaneID); typed != 1 {
		t.Fatalf("hookless stop: %d nudges typed, want 1", typed)
	}
	if d := r.get(job.ID); d.Nudges != 2 || !hasEvent(d, EventNudge, "Nudged (2 of 3)") {
		t.Fatalf("budget not shared: nudges %d %+v", d.Nudges, d.Timeline)
	}
}

func TestBackstopJobsListsOnlyRunningUnwaitingJobs(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	running := r.create(CreateInput{Brief: "Run", Dir: t.TempDir()})
	blocked := r.create(CreateInput{Brief: "Block", Dir: t.TempDir()})
	if _, err := r.svc.Update(ctx, blocked.ID, UpdateInput{Progress: "waiting on CI", State: string(StateBlocked), Reason: "CI is down", RecheckIn: "30m"}); err != nil {
		t.Fatal(err)
	}
	list, err := r.svc.BackstopJobs(ctx)
	if err != nil || len(list) != 1 || list[0].ID != running.ID || list[0].PaneID != running.PaneID {
		t.Fatalf("backstop jobs %+v %v", list, err)
	}
}
