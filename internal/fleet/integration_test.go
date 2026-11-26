//go:build integration

// The fleet cache against a real herdr (`mise run test:integration`; M9). Until now the cache had
// only been smoke-tested by hand; the unit tests drive it with the fake. Here a real session
// changes underneath a running cache and the cache must follow by herdr's events, not by its
// poll (the poll is set to a minute, so only events can explain a change seen within seconds):
// a workspace, then a tab, then an agent that reports itself blocked (as Claude's herdr
// integration does), then that agent going idle.
//
// The session helper is this package's own and enforces DESIGN.md section 15: the name must match
// ^crowsnest-test-, every herdr child gets an environment without HERDR_*, and teardown stops and
// deletes only the session it created.

package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
)

var integrationSessionPattern = regexp.MustCompile(`^crowsnest-test-[a-z0-9]+$`)

// startFleetSession starts a fresh headless session and registers its teardown.
func startFleetSession(t *testing.T) *herdr.Session {
	t.Helper()
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr is not installed; skipping the real-herdr fleet test")
	}
	suffix := make([]byte, 5)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "crowsnest-test-" + hex.EncodeToString(suffix)
	if !integrationSessionPattern.MatchString(name) {
		t.Fatalf("refusing session %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := herdr.EnsureSession(ctx, herdr.SessionOptions{Name: name, LogDir: t.TempDir(), StartTimeout: 20 * time.Second})
	t.Cleanup(func() {
		for _, args := range [][]string{{"session", "stop", name, "--json"}, {"session", "delete", name, "--json"}} {
			herdrCLI(t, "", args...)
		}
	})
	if err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	if !session.Started {
		t.Fatalf("session %s was already running", name)
	}
	return session
}

// herdrCLI runs the herdr CLI with HERDR_* removed, against session when it is not empty.
func herdrCLI(t *testing.T, session string, args ...string) {
	t.Helper()
	if session != "" {
		if !integrationSessionPattern.MatchString(session) {
			t.Fatalf("refusing to address %q", session)
		}
		args = append([]string{"--session", session}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "herdr", args...)
	cmd.Env = herdr.ScrubEnv(os.Environ())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("herdr %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

// waitFor polls the cache every 50 ms until check passes or the timeout ends.
func waitFor(t *testing.T, cache *Cache, timeout time.Duration, what string, check func(Fleet) bool) Fleet {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		fleet, err := cache.Fleet(context.Background())
		if err == nil && check(fleet) {
			return fleet
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s; last fleet %+v (err %v)", timeout, what, fleet, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestIntegrationFleetCacheFollowsHerdrEvents(t *testing.T) {
	session := startFleetSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := hub.New()
	updates := events.Subscribe(64)
	defer updates.Close()
	cache := New(Options{
		Client: session.Client, Hub: events, Session: session.Name,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Poll:   time.Minute, // only events can explain a change seen within seconds
	})
	go cache.Run(ctx)
	waitFor(t, cache, 10*time.Second, "the first snapshot", func(f Fleet) bool { return f.Session == session.Name && f.Error == "" })

	// A workspace appears by its event.
	created, err := session.Client.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{Label: "fleet-itest", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("workspace.create: %v", err)
	}
	pane := created.RootPane.ID
	start := time.Now()
	fleet := waitFor(t, cache, 5*time.Second, "the new workspace", func(f Fleet) bool {
		return len(f.Workspaces) == 1 && f.Workspaces[0].Label == "fleet-itest" && len(f.Workspaces[0].Tabs) == 1
	})
	t.Logf("workspace seen after %s (revision %d)", time.Since(start).Round(time.Millisecond), fleet.Revision)
	revision := fleet.Revision

	// And a tab, with a revision bump and a published fleet event.
	if _, err := session.Client.TabCreate(ctx, herdr.TabCreateParams{WorkspaceID: created.Workspace.ID, Label: "second"}); err != nil {
		t.Fatalf("tab.create: %v", err)
	}
	fleet = waitFor(t, cache, 5*time.Second, "the new tab", func(f Fleet) bool {
		return len(f.Workspaces) == 1 && len(f.Workspaces[0].Tabs) == 2 && f.Workspaces[0].Tabs[1].Label == "second"
	})
	if fleet.Revision <= revision || fleet.Counts.Panes != 2 {
		t.Fatalf("after the tab: revision %d (was %d), counts %+v", fleet.Revision, revision, fleet.Counts)
	}
	sawEvent := false
	for drained := false; !drained; {
		select {
		case event := <-updates.Events():
			sawEvent = sawEvent || event.Type == EventType
		default:
			drained = true
		}
	}
	if !sawEvent {
		t.Fatal("no fleet event was published for the changes")
	}

	// An agent reports itself blocked in the first pane: it is listed and needs you (per-pane
	// status subscription, rebuilt when the pane set changed).
	herdrCLI(t, session.Name, "pane", "report-agent", "--source", "fleet-itest", "--agent", "claude", "--state", "blocked", pane)
	fleet = waitFor(t, cache, 5*time.Second, "the reported agent, blocked", func(f Fleet) bool {
		return len(f.Agents) == 1 && f.Agents[0].PaneID == pane && f.Agents[0].Status == herdr.StatusBlocked && f.Agents[0].NeedsYou
	})
	if fleet.Counts.Agents != 1 || fleet.Counts.Blocked != 1 {
		t.Fatalf("counts with a blocked agent: %+v", fleet.Counts)
	}

	// It goes idle: no longer needs you.
	herdrCLI(t, session.Name, "pane", "report-agent", "--source", "fleet-itest", "--agent", "claude", "--state", "idle", pane)
	waitFor(t, cache, 5*time.Second, "the agent, idle", func(f Fleet) bool {
		return len(f.Agents) == 1 && f.Agents[0].Status == herdr.StatusIdle && !f.Agents[0].NeedsYou
	})
}
