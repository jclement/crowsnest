// Tests for the fake herdr's rules (the ones callers depend on) and for the demo scenario.

package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
)

func TestFakeAgentsAndPrompts(t *testing.T) {
	ctx := context.Background()
	f := New()
	_, _, pane := f.AddWorkspace("work", "/src")
	if _, err := f.AgentGet(ctx, pane); !herdr.IsNotFound(err) {
		t.Fatalf("a shell pane is not an agent: err = %v", err)
	}
	agent, err := f.AgentStart(ctx, herdr.AgentStartParams{Name: "builder", Kind: "claude", PaneID: pane})
	if err != nil || agent.Name != "builder" || !agent.LaunchPending {
		t.Fatalf("start = %+v, %v (want the launch pending, as herdr answers over the socket)", agent, err)
	}
	if _, err := f.AgentStart(ctx, herdr.AgentStartParams{Name: "again", Kind: "claude", PaneID: pane}); err == nil {
		t.Fatal("starting a second agent in a busy pane should fail")
	}
	byPane, err := f.AgentGet(ctx, pane)
	if err != nil || byPane.Name != "builder" || byPane.AgentSession == nil || byPane.LaunchPending || !byPane.InteractiveReady {
		t.Fatalf("target by pane id: %+v, %v", byPane, err)
	}

	if _, err := f.AgentPrompt(ctx, "builder", "do the thing", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.Prompts(); len(got) != 1 || got[0].Text != "do the thing" || got[0].PaneID != pane {
		t.Fatalf("prompts = %+v", got)
	}
	if agent, _ := f.AgentGet(ctx, "builder"); agent.AgentStatus != herdr.StatusWorking {
		t.Fatalf("status after prompt = %s", agent.AgentStatus)
	}

	_ = f.SetStatus("builder", herdr.StatusBlocked)
	if _, err := f.AgentPrompt(ctx, "builder", "more", nil); !errors.Is(err, herdr.ErrAgentBlocked) {
		t.Fatalf("prompt to a blocked agent: err = %v", err)
	}
	if len(f.Prompts()) != 1 {
		t.Fatal("a refused prompt must not be recorded as sent")
	}
	if err := f.AgentSendKeys(ctx, "builder", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if keys := f.SentKeys(); len(keys) != 1 || keys[0].PaneID != pane || keys[0].Keys[0] != "1" {
		t.Fatalf("keys = %+v", keys)
	}
	if _, err := f.AgentWait(ctx, "builder", []herdr.AgentStatus{herdr.StatusIdle}, 0); !errors.Is(err, herdr.ErrTimeout) {
		t.Fatalf("wait for a state not reached: err = %v", err)
	}
}

func TestFakeRollUpAndSnapshot(t *testing.T) {
	ctx := context.Background()
	f := New()
	ws, _, first := f.AddWorkspace("work", "/src")
	_, second := f.AddTab(ws, "two", "/src")
	_ = f.AddAgent(first, "a", "claude", herdr.StatusIdle, "")
	_ = f.AddAgent(second, "b", "claude", herdr.StatusBlocked, "")
	snapshot, err := f.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workspaces) != 1 || len(snapshot.Tabs) != 2 || len(snapshot.Panes) != 2 || len(snapshot.Agents) != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Workspaces[0].AgentStatus != herdr.StatusBlocked || snapshot.Workspaces[0].TabCount != 2 {
		t.Fatalf("workspace roll-up = %+v", snapshot.Workspaces[0])
	}
	f.FailNext("session.snapshot", herdr.ErrTimeout)
	if _, err := f.Snapshot(ctx); !errors.Is(err, herdr.ErrTimeout) {
		t.Fatalf("scripted failure: err = %v", err)
	}
	if _, err := f.Snapshot(ctx); err != nil {
		t.Fatal("a scripted failure applies once")
	}
}

func TestFakeSubscriptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := New()
	ws, _, pane := f.AddWorkspace("work", "/src")
	_ = f.AddAgent(pane, "a", "claude", herdr.StatusIdle, "")

	if _, err := f.Subscribe(ctx, []herdr.Subscription{{Type: herdr.EventAgentStatusChanged}}); !errors.Is(err, herdr.ErrInvalidRequest) {
		t.Fatalf("status subscription without a pane: err = %v", err)
	}
	if _, err := f.Subscribe(ctx, []herdr.Subscription{{Type: "tab.created", PaneID: pane}}); !errors.Is(err, herdr.ErrInvalidRequest) {
		t.Fatalf("lifecycle subscription with a pane: err = %v", err)
	}
	stream, err := f.Subscribe(ctx, []herdr.Subscription{{Type: "tab.created"}, {Type: herdr.EventAgentStatusChanged, PaneID: pane}})
	if err != nil {
		t.Fatal(err)
	}
	f.AddTab(ws, "x", "/")
	_ = f.SetStatus("a", herdr.StatusWorking)
	for _, want := range []string{"tab_created", herdr.EventAgentStatusChanged} {
		select {
		case event := <-stream.Events():
			if event.Kind != want {
				t.Fatalf("event = %+v, want %s", event, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no %s event", want)
		}
	}
	f.DropStreams(herdr.ErrEventsLost)
	if _, open := <-stream.Events(); open || !errors.Is(stream.Err(), herdr.ErrEventsLost) {
		t.Fatalf("dropped stream: open %v err %v", open, stream.Err())
	}
}

func TestFakeReads(t *testing.T) {
	ctx := context.Background()
	f := New()
	_, _, pane := f.AddWorkspace("work", "/src")
	_ = f.SetScreen(pane, "plain", "\x1b[1mplain\x1b[0m")
	text, _ := f.PaneRead(ctx, pane, herdr.ReadOptions{})
	ansi, _ := f.PaneRead(ctx, pane, herdr.ReadOptions{ANSI: true})
	if text.Text != "plain" || ansi.Text != "\x1b[1mplain\x1b[0m" || ansi.Format != herdr.FormatANSI {
		t.Fatalf("text %+v ansi %+v", text, ansi)
	}
	if err := f.PaneRun(ctx, pane, "ls"); err != nil {
		t.Fatal(err)
	}
	if runs := f.Runs(); len(runs) != 1 || runs[0].Keys[0] != "ls" {
		t.Fatalf("runs = %+v", runs)
	}
	recent, _ := f.PaneRead(ctx, pane, herdr.ReadOptions{Source: herdr.SourceRecent})
	if recent.Text == "" || recent.Text == text.Text {
		t.Fatalf("recent should include scrollback: %q", recent.Text)
	}
}

func TestDemoFleet(t *testing.T) {
	ctx := context.Background()
	demo := NewDemo()
	snapshot, err := demo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workspaces) != 3 || len(snapshot.Agents) != 5 {
		t.Fatalf("demo has %d workspaces and %d agents", len(snapshot.Workspaces), len(snapshot.Agents))
	}
	statuses := map[herdr.AgentStatus]bool{}
	for _, agent := range snapshot.Agents {
		statuses[agent.AgentStatus] = true
	}
	for _, want := range []herdr.AgentStatus{herdr.StatusIdle, herdr.StatusWorking, herdr.StatusDone, herdr.StatusBlocked} {
		if !statuses[want] {
			t.Errorf("no demo agent is %s", want)
		}
	}

	docs := demo.PaneOf(DemoDocs)
	screen, _ := demo.PaneRead(ctx, docs, herdr.ReadOptions{})
	if dialog := fleet.DetectDialog(screen.Text); !dialog.Present || len(dialog.Options) != 3 {
		t.Fatalf("the docs writer's screen is not a detectable dialog: %+v\n%s", dialog, screen.Text)
	}
	idle, _ := demo.PaneRead(ctx, demo.PaneOf(DemoOrchestrator), herdr.ReadOptions{ANSI: true})
	if draft := fleet.DetectDraft(idle.Text); !draft.InputFound || draft.Present {
		t.Fatalf("the orchestrator's input box should be found and empty (ghost text only): %+v", draft)
	}
}

func TestDemoScript(t *testing.T) {
	ctx := context.Background()
	demo := NewDemo()
	status := func(name string) herdr.AgentStatus {
		agent, err := demo.AgentGet(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return agent.AgentStatus
	}

	demo.Step() // step 1: builder settles, reviewer starts
	if status(DemoBuilder) != herdr.StatusDone || status(DemoReviewer) != herdr.StatusWorking {
		t.Fatalf("after step 1: builder %s reviewer %s", status(DemoBuilder), status(DemoReviewer))
	}

	// Answering the dialog unblocks the docs writer; the next dialog arrives three steps later.
	if err := demo.AgentSendKeys(ctx, DemoDocs, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if status(DemoDocs) != herdr.StatusWorking {
		t.Fatalf("docs writer after answering = %s", status(DemoDocs))
	}
	for range 3 {
		demo.Step()
	}
	if status(DemoDocs) != herdr.StatusBlocked {
		t.Fatalf("docs writer should be blocked again, is %s", status(DemoDocs))
	}

	// A prompted agent works for two steps, then goes idle.
	if _, err := demo.AgentPrompt(ctx, DemoOrchestrator, "status please", nil); err != nil {
		t.Fatal(err)
	}
	demo.Step()
	if status(DemoOrchestrator) != herdr.StatusWorking {
		t.Fatalf("orchestrator one step after a prompt = %s", status(DemoOrchestrator))
	}
	demo.Step()
	if status(DemoOrchestrator) != herdr.StatusIdle {
		t.Fatalf("orchestrator two steps after a prompt = %s", status(DemoOrchestrator))
	}
}
