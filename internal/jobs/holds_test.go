package jobs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/throttle"
)

// fakePause is a pause switch the test flips.
type fakePause struct {
	mu     sync.Mutex
	paused bool
	held   []string
}

func (p *fakePause) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

func (p *fakePause) Set(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = paused
}

func (p *fakePause) Held(_ context.Context, key, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.held = append(p.held, key)
}

func (p *fakePause) Keys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.held...)
}

// fakeThrottle is a throttle with fixed settings and a load hold the test sets.
type fakeThrottle struct {
	mu       sync.Mutex
	settings throttle.Settings
	holding  bool
}

func (f *fakeThrottle) Settings(context.Context) throttle.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings
}

func (f *fakeThrottle) Load(context.Context) throttle.LoadState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holding {
		return throttle.LoadState{Holding: true, Reason: "the machine is busy: load 4.0 per core for a minute (hold above 2.0)"}
	}
	return throttle.LoadState{}
}

func TestSlotLimitHoldsAndStartsInOrder(t *testing.T) {
	limit := &fakeThrottle{settings: throttle.Settings{MaxRunning: 2}}
	r := newRig(t, func(o *Options) { o.Throttle = limit })
	ctx := context.Background()
	first := r.create(CreateInput{Brief: "One", Dir: t.TempDir()})
	second := r.create(CreateInput{Brief: "Two", Dir: t.TempDir()})
	third := r.create(CreateInput{Brief: "Three", Dir: t.TempDir()})
	fourth := r.create(CreateInput{Brief: "Four", Dir: t.TempDir()})
	if first.State != StateRunning || second.State != StateRunning {
		t.Fatalf("the first two must start: %s %s", first.State, second.State)
	}
	if third.State != StateQueued || third.Cause != CauseHeld || third.CauseDetail != "waiting for a slot: 2 of 2 running" {
		t.Fatalf("third: %s %s %q", third.State, third.Cause, third.CauseDetail)
	}
	if fourth.CauseDetail != "waiting for a slot: 2 of 2 running, 1 ahead in the queue" {
		t.Fatalf("fourth: %q", fourth.CauseDetail)
	}
	if !hasEvent(third, EventLaunch, "Held: waiting for a slot") {
		t.Fatal("the hold is not on the timeline")
	}
	// Leads and the orchestrator are not counted; neither are standing agents' jobs. A job that
	// finishes frees its slot, and the oldest waiting job takes it, not the newest.
	if _, err := r.svc.Done(ctx, first.ID, DoneInput{ResultMD: "ok", Verified: []string{"x"}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(ctx)
	if d := r.get(third.ID); d.State != StateRunning {
		t.Fatalf("the oldest waiting job did not start: %s %q", d.State, d.CauseDetail)
	}
	if d := r.get(fourth.ID); d.State != StateQueued || d.CauseDetail != "waiting for a slot: 2 of 2 running" {
		t.Fatalf("fourth after a slot freed: %s %q", d.State, d.CauseDetail)
	}
	// Start anyway goes past the limit.
	started, err := r.svc.StartAnyway(ctx, fourth.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d := r.get(started.ID); d.State != StateRunning || !hasEvent(d, EventLaunch, "started it anyway") {
		t.Fatalf("start anyway: %s %q", d.State, d.CauseDetail)
	}
	if _, err := r.svc.StartAnyway(ctx, fourth.ID); err == nil {
		t.Fatal("start anyway on a running job must be refused")
	}
}

func TestProjectLimit(t *testing.T) {
	limit := &fakeThrottle{settings: throttle.Settings{MaxRunning: 10, ProjectLimits: map[int64]int{1: 1}}}
	r := newRig(t, func(o *Options) { o.Throttle = limit })
	ctx := context.Background()
	a := r.create(CreateInput{Brief: "A", Dir: t.TempDir()})
	b := r.create(CreateInput{Brief: "B", Dir: t.TempDir()})
	// File both under project 1 by hand (projects are tested elsewhere), the second still queued.
	for _, id := range []string{a.ID, b.ID} {
		record, _ := r.db.GetJob(ctx, mustParse(id))
		record.ProjectID = 1
		if id == b.ID {
			record.State, record.PaneID, record.AgentName = string(StateQueued), "", ""
		}
		if err := r.db.UpdateJob(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	r.svc.launch(ctx, b.ID)
	if d := r.get(b.ID); d.State != StateQueued || d.CauseDetail != "waiting for a slot: 1 of 1 running in this project" {
		t.Fatalf("project limit: %s %q", d.State, d.CauseDetail)
	}
}

func TestLoadHoldHoldsNewStartsOnly(t *testing.T) {
	limit := &fakeThrottle{settings: throttle.Settings{MaxRunning: 10}}
	r := newRig(t, func(o *Options) { o.Throttle = limit })
	ctx := context.Background()
	running := r.create(CreateInput{Brief: "Running", Dir: t.TempDir()})
	limit.mu.Lock()
	limit.holding = true
	limit.mu.Unlock()
	held := r.create(CreateInput{Brief: "Held", Dir: t.TempDir()})
	if held.State != StateQueued || !strings.HasPrefix(held.CauseDetail, "waiting: the machine is busy") {
		t.Fatalf("load hold: %s %q", held.State, held.CauseDetail)
	}
	if d := r.get(running.ID); d.State != StateRunning {
		t.Fatal("the load hold touched running work")
	}
	limit.mu.Lock()
	limit.holding = false
	limit.mu.Unlock()
	r.svc.Tick(ctx)
	if d := r.get(held.ID); d.State != StateRunning || d.Cause == CauseHeld {
		t.Fatalf("did not start once the load fell: %s %q", d.State, d.CauseDetail)
	}
}

// TestPauseSuppressesEveryAutomation is the pause table (DESIGN.md 6.13): each automatic action
// is held while paused, each human action still works, and the Stop hook lets the turn end.
func TestPauseSuppressesEveryAutomation(t *testing.T) {
	ctx := context.Background()
	type rigPaused struct {
		*rig
		pause *fakePause
	}
	setup := func(t *testing.T) rigPaused {
		pause := &fakePause{}
		r := newRig(t, func(o *Options) { o.Pause = pause })
		return rigPaused{r, pause}
	}

	automatic := []struct {
		name string
		run  func(t *testing.T, r rigPaused) (acted bool)
	}{
		{"a new job does not start", func(t *testing.T, r rigPaused) bool {
			r.pause.Set(true)
			job := r.create(CreateInput{Brief: "New", Dir: t.TempDir()})
			if job.Cause != CauseHeld || job.CauseDetail != pausedText {
				t.Fatalf("held reason %q", job.CauseDetail)
			}
			return job.State != StateQueued
		}},
		{"a no-directory job is not dispatched to the orchestrator", func(t *testing.T, r rigPaused) bool {
			_, _, pane := r.fake.AddWorkspace("orchestrator", "/tmp")
			_ = r.fake.AddAgent(pane, OrchestratorName, "claude", herdr.StatusIdle, "s-orch")
			r.mu.Lock()
			r.orch = OrchestratorInfo{PaneID: pane, Ready: true}
			r.mu.Unlock()
			r.pause.Set(true)
			job := r.create(CreateInput{Brief: "For the orchestrator"})
			r.svc.Tick(ctx)
			return r.get(job.ID).State != StateQueued
		}},
		{"an exited agent is not restarted", func(t *testing.T, r rigPaused) bool {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			starts := len(r.fake.Starts())
			r.pause.Set(true)
			_ = r.fake.ExitAgent(job.PaneID)
			r.clock.Advance(time.Minute)
			r.svc.Tick(ctx)
			return len(r.fake.Starts()) != starts
		}},
		{"a limit is not retried", func(t *testing.T, r rigPaused) bool {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
			_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "rate_limit"})
			prompts := len(r.fake.Prompts())
			r.pause.Set(true)
			r.clock.Advance(10 * time.Minute)
			r.svc.Tick(ctx)
			return len(r.fake.Prompts()) != prompts
		}},
		{"the backstop does not nudge", func(t *testing.T, r rigPaused) bool {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			r.pause.Set(true)
			nudged, err := r.svc.NudgeIfDue(ctx, r.backstopJob(job.ID))
			return nudged || err != nil
		}},
		{"the Stop hook does not nudge", func(t *testing.T, r rigPaused) bool {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			r.pause.Set(true)
			reply, err := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{SessionID: job.SessionID})
			if err != nil {
				t.Fatal(err)
			}
			if !hasEvent(r.get(job.ID), EventHook, "paused") {
				t.Fatal("the allowed stop is not recorded as allowed for the pause")
			}
			return reply.Stdout != ""
		}},
	}
	for _, tc := range automatic {
		t.Run("held: "+tc.name, func(t *testing.T) {
			r := setup(t)
			if tc.run(t, r) {
				t.Fatal("acted while paused")
			}
		})
	}

	human := []struct {
		name string
		run  func(t *testing.T, r rigPaused) error
	}{
		{"Nudge", func(t *testing.T, r rigPaused) error {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			r.pause.Set(true)
			_, err := r.svc.Nudge(ctx, job.ID)
			return err
		}},
		{"a message", func(t *testing.T, r rigPaused) error {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			r.pause.Set(true)
			_, err := r.svc.Message(ctx, job.ID, MessageInput{Text: "how is it going?"})
			return err
		}},
		{"Start anyway", func(t *testing.T, r rigPaused) error {
			r.pause.Set(true)
			job := r.create(CreateInput{Brief: "New", Dir: t.TempDir()})
			if _, err := r.svc.StartAnyway(ctx, job.ID); err != nil {
				return err
			}
			if d := r.get(job.ID); d.State != StateRunning {
				t.Fatalf("start anyway while paused: %s", d.State)
			}
			return nil
		}},
		{"Restart agent", func(t *testing.T, r rigPaused) error {
			job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
			r.pause.Set(true)
			_, err := r.svc.RestartAgent(ctx, job.ID)
			return err
		}},
	}
	for _, tc := range human {
		t.Run("works: "+tc.name, func(t *testing.T) {
			r := setup(t)
			if err := tc.run(t, r); err != nil {
				t.Fatalf("a human action failed while paused: %v", err)
			}
		})
	}

	t.Run("resume starts what was held", func(t *testing.T) {
		r := setup(t)
		r.pause.Set(true)
		job := r.create(CreateInput{Brief: "New", Dir: t.TempDir()})
		r.svc.Tick(ctx)
		if r.get(job.ID).State != StateQueued {
			t.Fatal("started while paused")
		}
		r.pause.Set(false)
		r.svc.Tick(ctx)
		if d := r.get(job.ID); d.State != StateRunning {
			t.Fatalf("did not start after resume: %s %q", d.State, d.CauseDetail)
		}
	})
}
