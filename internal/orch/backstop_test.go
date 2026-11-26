// Tests for the herdr-side backstop (DESIGN.md 6.3): BackstopDue as a table, then the poker's
// loop with a fake jobs service: an idle job agent with no Stop hook is nudged after 60 s and
// not again within 60 s; a stop whose hook ran is left alone; a draft, a dialog or a working
// agent is never nudged. The real double-nudge test against the jobs service is in
// internal/jobs/backstop_test.go.

package orch

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/journal"
)

func TestBackstopDue(t *testing.T) {
	now := pokeEpoch
	since := now.Add(-BackstopIdle)
	idle := AgentSeen{PaneID: "w2:p1", Status: herdr.StatusIdle, Since: since}
	cases := []struct {
		name  string
		job   BackstopJob
		agent AgentSeen
		next  time.Time
		want  bool
	}{
		{name: "idle 60 s, no hook", agent: idle, want: true},
		{name: "done counts as settled", agent: AgentSeen{Status: herdr.StatusDone, Since: since}, want: true},
		{name: "idle 59 s", agent: AgentSeen{Status: herdr.StatusIdle, Since: now.Add(-59 * time.Second)}},
		{name: "working", agent: AgentSeen{Status: herdr.StatusWorking, Since: since}},
		{name: "blocked is the Inbox's", agent: AgentSeen{Status: herdr.StatusBlocked, Since: since}},
		{name: "unknown", agent: AgentSeen{Status: herdr.StatusUnknown, Since: since}},
		{name: "looked at less than 60 s ago", agent: idle, next: now.Add(time.Second)},
		{name: "hook ran as the agent settled", job: BackstopJob{LastStopAt: since.Add(-3 * time.Second)}, agent: idle},
		{name: "hook ran after the agent settled", job: BackstopJob{LastStopAt: since.Add(time.Second)}, agent: idle},
		{name: "hook ran for an earlier stop", job: BackstopJob{LastStopAt: since.Add(-StopHookSlack - time.Second)}, agent: idle, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BackstopDue(tc.job, tc.agent, now, tc.next); got != tc.want {
				t.Fatalf("BackstopDue = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeBackstop is the jobs service as the backstop sees it.
type fakeBackstop struct {
	mu     sync.Mutex
	jobs   []BackstopJob
	nudged []string
}

func (f *fakeBackstop) BackstopJobs(context.Context) ([]BackstopJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.jobs), nil
}

func (f *fakeBackstop) NudgeIfDue(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nudged = append(f.nudged, id)
	return true, nil
}

func (f *fakeBackstop) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.nudged)
}

func (f *fakeBackstop) stamp(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[0].LastStopAt = at
}

func backstopRig(t *testing.T) (*pokeRig, *fakeBackstop) {
	t.Helper()
	backstop := &fakeBackstop{}
	r := newPokeRig(t, func(o *PokerOptions) { o.Backstop = backstop })
	backstop.jobs = []BackstopJob{{ID: "j_5", PaneID: r.builder}}
	return r, backstop
}

func TestBackstopNudgesAnIdleAgentWithoutHooks(t *testing.T) {
	r, backstop := backstopRig(t)
	r.set(r.builder, herdr.StatusIdle)
	r.check()
	r.clock.Advance(BackstopIdle - time.Second)
	r.check()
	if backstop.count() != 0 {
		t.Fatal("nudged before 60 s")
	}
	r.clock.Advance(time.Second)
	r.check()
	r.clock.Advance(30 * time.Second)
	r.check()
	if backstop.count() != 1 {
		t.Fatalf("nudges %v, want one", backstop.nudged)
	}
	r.clock.Advance(30 * time.Second)
	r.check()
	if backstop.count() != 2 {
		t.Fatalf("still idle a minute after the nudge: nudges %v", backstop.nudged)
	}
	page, _ := r.journal.List(context.Background(), journal.Query{Kinds: []journal.Kind{journal.KindBackstop}})
	if page.Total != 2 {
		t.Fatalf("backstop journal entries %d", page.Total)
	}
}

func TestBackstopLeavesAStopTheHookHandled(t *testing.T) {
	r, backstop := backstopRig(t)
	r.check() // working
	r.clock.Advance(10 * time.Second)
	backstop.stamp(r.clock.Now()) // the Stop hook ran as the turn ended
	r.clock.Advance(2 * time.Second)
	r.set(r.builder, herdr.StatusDone) // herdr notices a moment later
	r.check()
	r.clock.Advance(5 * BackstopIdle)
	r.check()
	if backstop.count() != 0 {
		t.Fatalf("a stop the hook handled was nudged too: %v", backstop.nudged)
	}
	// The next stop, without a hook (it failed), is the backstop's.
	r.set(r.builder, herdr.StatusWorking)
	r.check()
	r.clock.Advance(time.Minute)
	r.set(r.builder, herdr.StatusDone)
	r.check()
	r.clock.Advance(BackstopIdle)
	r.check()
	if backstop.count() != 1 {
		t.Fatalf("a hookless stop was not nudged: %v", backstop.nudged)
	}
}

func TestBackstopNeverTypesOverADraftOrADialog(t *testing.T) {
	for name, screen := range map[string]string{"draft": draftInput, "dialog": dialogInput} {
		t.Run(name, func(t *testing.T) {
			r, backstop := backstopRig(t)
			r.set(r.builder, herdr.StatusIdle)
			r.screen(r.builder, screen)
			r.check()
			r.clock.Advance(3 * BackstopIdle)
			r.check()
			if backstop.count() != 0 {
				t.Fatalf("nudged over a %s", name)
			}
		})
	}
}
