// Regression tests for the R1 review findings on conversations: cursors across a restart
// (R1-C05), pins that follow the orchestrator between panes (R1-C06), a read that must still
// announce what it found (R1-C07), and a session change between two conversations that both
// have no transcript yet (R1-C11); a pin racing a poll (R1b-C04).

package transcript

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCursorFromAnEarlierProcessResets is R1-C05: the browser keeps its cursor across a
// crowsnest restart, and the new process's first conversation must not recognise it.
func TestCursorFromAnEarlierProcessResets(t *testing.T) {
	startGenerations() // as the earlier process did at start-up
	before := NewConversation(SourceTranscript, "a")
	for i := range 10 {
		before.Apply([]Message{{ID: fmt.Sprint("a", i), Kind: KindUser, Text: "old"}})
	}
	held := before.Cursor()

	startGenerations() // crowsnest restarted
	after := NewConversation(SourceTranscript, "b")
	for i := range 12 {
		after.Apply([]Message{{ID: fmt.Sprint("b", i), Kind: KindUser, Text: "new"}})
	}
	page := after.Page(held, 0)
	if !page.Reset || len(page.Messages) != 12 {
		t.Fatalf("a cursor from before the restart (%s, now %s) was taken as current: reset=%v, %d messages", held, after.Cursor(), page.Reset, len(page.Messages))
	}
}

// TestPinFollowsTheOrchestratorToANewPane is R1-C06.
func TestPinFollowsTheOrchestratorToANewPane(t *testing.T) {
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("orchestrator", Origin{PaneID: "w1:p1", Cwd: "/x", SessionID: "s1"})
	now := time.Unix(1_000_000, 0)
	manager := NewManager(ManagerOptions{
		ConfigDir: t.TempDir(), Lookup: origins.lookup, IdleTimeout: time.Minute,
		Now: func() time.Time { return now },
	})
	ctx := context.Background()
	if err := manager.Pin(ctx, "orchestrator"); err != nil {
		t.Fatal(err)
	}
	// The pane is closed and the orchestrator is recovered in another one.
	origins.set("orchestrator", Origin{PaneID: "w1:p2", Cwd: "/x", SessionID: "s1"})
	if err := manager.Pin(ctx, "orchestrator"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	manager.PollAll(ctx)
	if _, ok := manager.watchers["w1:p1"]; ok {
		t.Fatal("the old pane's conversation stayed pinned after the orchestrator moved")
	}
	if _, ok := manager.watchers["w1:p2"]; !ok {
		t.Fatal("the orchestrator's current conversation was dropped")
	}
}

// TestGoneAgentIsNoLongerFollowed is R1-C06's other half: a conversation whose agent no longer
// exists is dropped at the next poll instead of being looked up every second until it idles.
func TestGoneAgentIsNoLongerFollowed(t *testing.T) {
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("w3:p1", Origin{PaneID: "w3:p1", Cwd: "/x", SessionID: "s"})
	manager := NewManager(ManagerOptions{ConfigDir: t.TempDir(), Lookup: origins.lookup})
	ctx := context.Background()
	if _, err := manager.Messages(ctx, "w3:p1", "", 0); err != nil {
		t.Fatal(err)
	}
	origins.mu.Lock()
	delete(origins.origins, "w3:p1")
	origins.mu.Unlock()
	manager.PollAll(ctx)
	if len(manager.watchers) != 0 {
		t.Fatal("a conversation whose agent is gone is still followed")
	}
}

// TestReadStillAnnouncesWhatItFound is R1-C07: two browsers watch one agent. A reads right
// after an append, before the background poll; B must still get a message event.
func TestReadStillAnnouncesWhatItFound(t *testing.T) {
	config := t.TempDir()
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("w1:p1", Origin{PaneID: "w1:p1", Cwd: "/work", SessionID: "s"})
	path := Path(config, "/work", "s")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	appendFile(t, path, userLine("1", "hello"))
	var mu sync.Mutex
	notified := 0
	manager := NewManager(ManagerOptions{
		ConfigDir: config, Lookup: origins.lookup,
		Notify: func(string, string) { mu.Lock(); notified++; mu.Unlock() },
	})
	ctx := context.Background()
	page, _ := manager.Messages(ctx, "w1:p1", "", 0)
	mu.Lock()
	notified = 0
	mu.Unlock()

	appendFile(t, path, userLine("2", "again"))
	if next, _ := manager.Messages(ctx, "w1:p1", page.Cursor, 0); texts(next.Messages) != "again" { // browser A
		t.Fatalf("A's read = %+v", next)
	}
	manager.PollAll(ctx) // the timer
	mu.Lock()
	defer mu.Unlock()
	if notified == 0 {
		t.Fatal("the change A's read found was never announced, so B stays stale")
	}
}

// TestSessionChangeBetweenTwoFallbacksResets is R1-C11: two fresh sessions in a row, neither
// with a transcript yet.
func TestSessionChangeBetweenTwoFallbacksResets(t *testing.T) {
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("w1:p1", Origin{PaneID: "w1:p1", Cwd: "/x", SessionID: "a"})
	manager := NewManager(ManagerOptions{
		ConfigDir: t.TempDir(), Lookup: origins.lookup,
		Fallback: func(context.Context, string) (string, error) { return "❯ ", nil },
	})
	ctx := context.Background()
	first, _ := manager.Messages(ctx, "w1:p1", "", 0)
	if first.SessionID != "a" || first.Source != SourcePane {
		t.Fatalf("first page = %+v", first)
	}
	origins.set("w1:p1", Origin{PaneID: "w1:p1", Cwd: "/x", SessionID: "b"})
	second, _ := manager.Messages(ctx, "w1:p1", first.Cursor, 0)
	if second.SessionID != "b" || !second.Reset {
		t.Fatalf("after a fallback-to-fallback session change = %+v (cursor %s → %s)", second, first.Cursor, second.Cursor)
	}
}

// TestPinRacingAPollIsRaceFree is R1b-C04: the orchestrator's status callback pins from its own goroutine
// while the manager's poll rewrites the watcher's origin. Under -race the two must not touch the
// origin without the same lock.
func TestPinRacingAPollIsRaceFree(t *testing.T) {
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("orchestrator", Origin{PaneID: "w1:p1", Cwd: "/x", SessionID: "s1"})
	origins.set("w1:p1", Origin{PaneID: "w1:p1", Cwd: "/x", SessionID: "s1"})
	manager := NewManager(ManagerOptions{ConfigDir: t.TempDir(), Lookup: origins.lookup, IdleTimeout: time.Hour})
	ctx := context.Background()
	if err := manager.Pin(ctx, "orchestrator"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			_ = manager.Pin(ctx, "orchestrator")
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			manager.PollAll(ctx)
		}
	}()
	wg.Wait()
}
