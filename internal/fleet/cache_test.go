// Tests for the fleet cache against the fake herdr: the view's shape, event-driven refresh,
// per-pane status subscriptions, recovery after events_lost, the poll fallback, and errors.

package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
)

// testFleet builds a small session: one workspace with an idle Claude agent and a shell tab.
func testFleet(t *testing.T) (*fake.Fake, string) {
	t.Helper()
	f := fake.New()
	_, _, agentPane := f.AddWorkspace("crowsnest", "/src/crowsnest")
	if err := f.AddAgent(agentPane, "builder", "claude", herdr.StatusIdle, "11111111-2222"); err != nil {
		t.Fatal(err)
	}
	wsID := "w1"
	f.AddTab(wsID, "shell", "/src/crowsnest")
	return f, agentPane
}

func newTestCache(f *fake.Fake, events *hub.Hub, poll time.Duration) *Cache {
	return New(Options{Client: f, Hub: events, Session: "crowsnest-test", Debounce: 10 * time.Millisecond, Poll: poll})
}

// waitFleet reads hub events until one satisfies ok, failing after a timeout.
func waitFleet(t *testing.T, sub *hub.Subscription, what string, ok func(Fleet) bool) Fleet {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event, open := <-sub.Events():
			if !open {
				t.Fatalf("hub subscription closed while waiting for %s", what)
			}
			if fleet, isFleet := event.Data.(Fleet); event.Type == EventType && isFleet && ok(fleet) {
				return fleet
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func agentStatus(fleet Fleet, name string) herdr.AgentStatus {
	for _, agent := range fleet.Agents {
		if agent.Name == name {
			return agent.Status
		}
	}
	return ""
}

func tabCount(fleet Fleet) int {
	total := 0
	for _, ws := range fleet.Workspaces {
		total += len(ws.Tabs)
	}
	return total
}

func TestFleetShape(t *testing.T) {
	f, agentPane := testFleet(t)
	if err := f.SetScreen(agentPane, "❯ 1. Yes\n  2. No\n\nEsc to cancel\n", ""); err != nil {
		t.Fatal(err)
	}
	cache := newTestCache(f, hub.New(), time.Hour)
	fleet, err := cache.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fleet.Session != "crowsnest-test" || fleet.HerdrVersion != fake.Version || fleet.Revision != 1 {
		t.Fatalf("header = %+v", fleet)
	}
	if len(fleet.Workspaces) != 1 || len(fleet.Workspaces[0].Tabs) != 2 {
		t.Fatalf("workspaces = %+v", fleet.Workspaces)
	}
	first := fleet.Workspaces[0].Tabs[0].Panes[0]
	if first.Agent == nil || first.Agent.Name != "builder" || first.Agent.Target != "builder" || !first.Agent.HasDialog || !first.Agent.HasTranscript {
		t.Fatalf("agent pane = %+v agent %+v", first, first.Agent)
	}
	if shell := fleet.Workspaces[0].Tabs[1].Panes[0]; shell.Agent != nil || shell.Cwd != "/src/crowsnest" {
		t.Fatalf("shell pane = %+v", shell)
	}
	if len(fleet.Agents) != 1 || !fleet.Agents[0].NeedsYou || fleet.Agents[0].Workspace != "crowsnest" {
		t.Fatalf("agents = %+v", fleet.Agents)
	}
	if fleet.Counts.Agents != 1 || fleet.Counts.Dialogs != 1 || fleet.Counts.Blocked != 1 || fleet.Counts.Panes != 2 || fleet.Counts.Workspaces != 1 {
		t.Fatalf("counts = %+v", fleet.Counts)
	}
}

func TestRefreshPublishesOnlyChanges(t *testing.T) {
	f, _ := testFleet(t)
	events := hub.New()
	sub := events.Subscribe(16)
	cache := newTestCache(f, events, time.Hour)
	ctx := context.Background()
	for range 3 {
		if err := cache.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
	}
	fleet, _ := cache.Fleet(ctx)
	if fleet.Revision != 1 {
		t.Fatalf("revision = %d after three identical refreshes, want 1", fleet.Revision)
	}
	if got := len(sub.Events()); got != 1 {
		t.Fatalf("%d hub events, want 1", got)
	}
	if err := f.SetStatus("builder", herdr.StatusWorking); err != nil {
		t.Fatal(err)
	}
	if err := cache.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if fleet, _ := cache.Fleet(ctx); fleet.Revision != 2 || agentStatus(fleet, "builder") != herdr.StatusWorking {
		t.Fatalf("after a change: revision %d status %s", fleet.Revision, agentStatus(fleet, "builder"))
	}
}

func TestLifecycleEventsRefresh(t *testing.T) {
	f, _ := testFleet(t)
	events := hub.New()
	sub := events.Subscribe(64)
	cache := newTestCache(f, events, time.Hour) // no poll: only events can refresh
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Run(ctx)
	waitFleet(t, sub, "the first fleet", func(fl Fleet) bool { return tabCount(fl) == 2 })
	waitStreams(t, f, 2)

	f.AddTab("w1", "logs", "/var/log")
	waitFleet(t, sub, "the new tab", func(fl Fleet) bool { return tabCount(fl) == 3 })
}

func TestAgentStatusSubscription(t *testing.T) {
	f, _ := testFleet(t)
	events := hub.New()
	sub := events.Subscribe(64)
	cache := newTestCache(f, events, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Run(ctx)
	waitFleet(t, sub, "the first fleet", func(fl Fleet) bool { return fl.Revision >= 1 })
	waitStreams(t, f, 2)

	// SetStatus emits only pane.agent_status_changed, so this proves the per-pane stream works.
	if err := f.SetStatus("builder", herdr.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	waitFleet(t, sub, "blocked", func(fl Fleet) bool { return agentStatus(fl, "builder") == herdr.StatusBlocked })
}

func TestResubscribesAfterEventsLost(t *testing.T) {
	f, _ := testFleet(t)
	events := hub.New()
	sub := events.Subscribe(64)
	cache := newTestCache(f, events, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Run(ctx)
	waitFleet(t, sub, "the first fleet", func(fl Fleet) bool { return fl.Revision >= 1 })
	waitStreams(t, f, 2)

	f.DropStreams(herdr.ErrEventsLost)
	waitStreams(t, f, 2) // both subscriptions came back
	f.AddTab("w1", "after", "/tmp")
	waitFleet(t, sub, "a tab added after resubscribing", func(fl Fleet) bool { return tabCount(fl) == 3 })
}

func TestPollFallback(t *testing.T) {
	f, _ := testFleet(t)
	for range 1000 {
		f.FailNext("events.subscribe", errors.New("subscriptions are broken"))
	}
	events := hub.New()
	sub := events.Subscribe(64)
	cache := newTestCache(f, events, 30*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Run(ctx)
	waitFleet(t, sub, "the first fleet", func(fl Fleet) bool { return fl.Revision >= 1 })
	if err := f.SetStatus("builder", herdr.StatusDone); err != nil {
		t.Fatal(err)
	}
	waitFleet(t, sub, "done via the poll", func(fl Fleet) bool { return agentStatus(fl, "builder") == herdr.StatusDone })
}

func TestRefreshErrorIsRecorded(t *testing.T) {
	f, _ := testFleet(t)
	events := hub.New()
	sub := events.Subscribe(16)
	cache := newTestCache(f, events, time.Hour)
	ctx := context.Background()

	f.FailNext("session.snapshot", errors.New("connection refused"))
	if _, err := cache.Fleet(ctx); err == nil {
		t.Fatal("Fleet with no good snapshot yet should fail")
	}
	if err := cache.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.FailNext("session.snapshot", errors.New("connection refused"))
	if err := cache.Refresh(ctx); err == nil {
		t.Fatal("refresh should report the failure")
	}
	waitFleet(t, sub, "an error fleet", func(fl Fleet) bool { return fl.Error != "" && fl.Counts.Agents == 1 })
	if err := cache.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	waitFleet(t, sub, "the error cleared", func(fl Fleet) bool { return fl.Error == "" })
}

func waitStreams(t *testing.T, f *fake.Fake, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for f.Streams() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%d event streams open, want %d", f.Streams(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
