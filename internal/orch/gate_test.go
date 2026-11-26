// The start gate: nothing starts while it gives a reason, the orchestrator shows waiting, and it
// starts by itself (or on Kick) once the gate opens. Launch settings are read per start, and the
// Codex engine starts codex with its instructions pointer.

package orch

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr/fake"
)

func waitForState(t *testing.T, rig *testRig, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status := rig.orch.Status()
		if status.State == want {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("never reached %s: %+v", want, status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGateHoldsTheStartUntilItOpens(t *testing.T) {
	f := fake.New()
	var reason atomic.Value
	reason.Store("herdr is not installed")
	rig := newRig(t, f, func(o *Options) {
		o.WatchInterval = time.Hour
		o.Gate = func(context.Context) string { return reason.Load().(string) }
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rig.orch.Run(ctx)
	status := waitForState(t, rig, StateWaiting)
	if status.Detail != "herdr is not installed" || len(f.Starts()) != 0 {
		t.Fatalf("waiting status %+v, starts %d", status, len(f.Starts()))
	}
	if _, err := rig.orch.Restart(ctx); !errors.Is(err, ErrWaiting) {
		t.Fatalf("a manual restart while waiting: %v", err)
	}
	reason.Store("")
	rig.orch.Kick()
	waitForState(t, rig, StateIdle)
	if len(f.Starts()) != 1 {
		t.Fatalf("starts = %d", len(f.Starts()))
	}
}

func TestGateClosingAfterStartDoesNotRestartGoneAgent(t *testing.T) {
	f := fake.New()
	var open atomic.Bool
	open.Store(true)
	rig := newRig(t, f, func(o *Options) {
		o.Gate = func(context.Context) string {
			if open.Load() {
				return ""
			}
			return "herdr went away"
		}
	})
	if _, err := rig.orch.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := rig.orch.Status()
	_ = f.ExitAgent(status.PaneID)
	open.Store(false)
	rig.orch.Watch(context.Background())
	if got := rig.orch.Status(); got.State != StateWaiting || len(f.Starts()) != 1 {
		t.Fatalf("status %+v, starts %d", got, len(f.Starts()))
	}
	open.Store(true)
	rig.orch.Watch(context.Background())
	if got := rig.orch.Status(); got.State != StateIdle || len(f.Starts()) != 2 {
		t.Fatalf("after the gate opened: %+v, starts %d", got, len(f.Starts()))
	}
	if rig.orch.restartsInLastHourLocked() != 0 {
		t.Error("a start after waiting spent the restart budget")
	}
}

func TestLaunchSettingsAreReadPerStart(t *testing.T) {
	f := fake.New()
	model := "haiku"
	rig := newRig(t, f, func(o *Options) {
		o.Launch = func() LaunchSettings { return LaunchSettings{Model: model, PermissionMode: "plan"} }
	})
	if _, err := rig.orch.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	model = "opus"
	if _, err := rig.orch.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	starts := f.Starts()
	if argValue(starts[0].Args, "--model") != "haiku" || argValue(starts[1].Args, "--model") != "opus" || argValue(starts[1].Args, "--permission-mode") != "plan" {
		t.Fatalf("starts %+v", starts)
	}
	if rig.orch.Status().Model != "opus" {
		t.Errorf("status model %q", rig.orch.Status().Model)
	}
}

func TestCodexEngine(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f, func(o *Options) {
		o.Launch = func() LaunchSettings { return LaunchSettings{Engine: EngineCodex, Model: "gpt-5"} }
	})
	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	start := f.Starts()[0]
	if start.Kind != "codex" || !slices.Contains(start.Args, "workspace-write") || argValue(start.Args, "--model") != "gpt-5" || slices.Contains(start.Args, "--settings") {
		t.Fatalf("codex start %+v", start)
	}
	if status.Engine != EngineCodex {
		t.Errorf("status engine %q", status.Engine)
	}
	prompts := f.Prompts()
	if len(prompts) == 0 || !strings.Contains(prompts[len(prompts)-1].Text, "CROWSNEST.md") {
		t.Fatalf("codex was not pointed at its instructions: %+v", prompts)
	}
}
