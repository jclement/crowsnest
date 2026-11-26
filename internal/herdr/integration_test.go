//go:build integration

// Integration tests against a real herdr (`mise run test:integration`). Each test gets its own
// throwaway session named crowsnest-test-<random>, started headless through EnsureSession and
// stopped and deleted at the end. The helper enforces the safety rules from DESIGN.md section 15
// rather than trusting the tests: the name must match ^crowsnest-test-, every herdr child gets an
// environment with HERDR_* removed, and teardown touches only the session the helper created.

package herdr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// testSessionPattern is the only kind of session these tests may create, stop or delete.
var testSessionPattern = regexp.MustCompile(`^crowsnest-test-[a-z0-9]+$`)

// newTestSession starts a fresh headless herdr session and registers its teardown.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr is not installed; skipping the real-herdr integration tests")
	}
	suffix := make([]byte, 5)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "crowsnest-test-" + hex.EncodeToString(suffix)
	if !testSessionPattern.MatchString(name) {
		t.Fatalf("refusing to create session %q: test sessions must match %s", name, testSessionPattern)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := EnsureSession(ctx, SessionOptions{Name: name, LogDir: t.TempDir(), StartTimeout: 20 * time.Second})
	t.Cleanup(func() { teardownTestSession(t, name) })
	if err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	if !session.Started {
		t.Fatalf("session %s was already running; a fresh random session should not be", name)
	}
	return session
}

// teardownTestSession stops and deletes a test session. It refuses any other name.
func teardownTestSession(t *testing.T, name string) {
	t.Helper()
	if !testSessionPattern.MatchString(name) {
		t.Errorf("refusing to tear down %q: not a test session", name)
		return
	}
	for _, args := range [][]string{{"session", "stop", name, "--json"}, {"session", "delete", name, "--json"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "herdr", args...)
		cmd.Env = ScrubEnv(os.Environ())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("herdr %s: %v: %s", strings.Join(args, " "), err, output)
		}
		cancel()
	}
}

// eventually polls check every 100 ms until it returns true or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIntegrationSession(t *testing.T) {
	session := newTestSession(t)
	client := session.Client
	ctx := context.Background()
	t.Logf("session %s at %s, herdr %s protocol %d", session.Name, session.SocketPath, session.Pong.Version, session.Pong.Protocol)

	if session.Pong.Version == "" || session.Pong.Protocol == 0 {
		t.Fatalf("pong = %+v", session.Pong)
	}
	if session.Pong.Protocol != ProtocolVersion {
		t.Logf("note: herdr protocol %d, crowsnest written for %d", session.Pong.Protocol, ProtocolVersion)
	}

	// A second EnsureSession finds the running server instead of starting another.
	again, err := EnsureSession(ctx, SessionOptions{Name: session.Name, LogDir: t.TempDir()})
	if err != nil || again.Started || again.SocketPath != session.SocketPath {
		t.Fatalf("EnsureSession on a running session = %+v, %v", again, err)
	}

	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Version != session.Pong.Version {
		t.Errorf("snapshot version %q, ping version %q", snapshot.Version, session.Pong.Version)
	}
	if _, err := client.AgentGet(ctx, "no-such-agent"); !IsNotFound(err) {
		t.Errorf("AgentGet of a missing agent: err = %v, want not found", err)
	}
	if _, err := client.Subscribe(ctx, []Subscription{{Type: EventAgentStatusChanged}}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("status subscription without a pane: err = %v, want invalid_request", err)
	}
}

func TestIntegrationWorkspacePaneAndEvents(t *testing.T) {
	session := newTestSession(t)
	client := session.Client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lifecycle := make([]Subscription, 0, len(LifecycleEventTypes))
	for _, eventType := range LifecycleEventTypes {
		lifecycle = append(lifecycle, Subscription{Type: eventType})
	}
	stream, err := client.Subscribe(ctx, lifecycle)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = stream.Close() }()

	created, err := client.WorkspaceCreate(ctx, WorkspaceCreateParams{Label: "itest", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("workspace.create: %v", err)
	}
	pane := created.RootPane.ID
	if created.Workspace.Label != "itest" || pane == "" || created.Tab.WorkspaceID != created.Workspace.ID {
		t.Fatalf("workspace.create = %+v", created)
	}
	waitForEvents(t, stream, "workspace_created", "pane_created")

	// Wait for the shell to settle, then run a command and read its output back.
	eventually(t, 15*time.Second, "a shell in the new pane", func() bool {
		info, err := client.PaneProcessInfo(ctx, pane)
		return err == nil && len(info.ForegroundProcesses) > 0
	})
	time.Sleep(time.Second) // shells print their prompt after the process appears
	marker := fmt.Sprintf("crowsnest-marker-%d", time.Now().UnixNano())
	if err := client.PaneRun(ctx, pane, "echo "+marker); err != nil {
		t.Fatalf("pane run: %v", err)
	}
	eventually(t, 10*time.Second, "the command's output", func() bool {
		read, err := client.PaneRead(ctx, pane, ReadOptions{Source: SourceRecent, Lines: 50})
		return err == nil && slices.Contains(trimmedLines(read.Text), marker)
	})
	ansi, err := client.PaneRead(ctx, pane, ReadOptions{Source: SourceVisible, ANSI: true})
	if err != nil || ansi.Format != FormatANSI {
		t.Fatalf("ANSI read = %+v, %v", ansi, err)
	}

	// A tab, and the listings that should now include it.
	tab, err := client.TabCreate(ctx, TabCreateParams{WorkspaceID: created.Workspace.ID, Label: "second"})
	if err != nil {
		t.Fatalf("tab.create: %v", err)
	}
	waitForEvents(t, stream, "tab_created")
	tabs, err := client.TabList(ctx, created.Workspace.ID)
	if err != nil || len(tabs) != 2 {
		t.Fatalf("tab.list = %+v, %v", tabs, err)
	}
	panes, err := client.PaneList(ctx, "")
	if err != nil || len(panes) != 2 {
		t.Fatalf("pane.list = %+v, %v", panes, err)
	}
	if got, err := client.PaneGet(ctx, tab.RootPane.ID); err != nil || got.TabID != tab.Tab.ID {
		t.Fatalf("pane.get = %+v, %v", got, err)
	}
	workspaces, err := client.WorkspaceList(ctx)
	if err != nil || len(workspaces) != 1 || workspaces[0].Label != "itest" {
		t.Fatalf("workspace.list = %+v, %v", workspaces, err)
	}

	// Per-pane subscriptions are accepted when they carry the pane id.
	statusStream, err := client.Subscribe(ctx, []Subscription{{Type: EventAgentStatusChanged, PaneID: pane}})
	if err != nil {
		t.Fatalf("status subscription: %v", err)
	}
	_ = statusStream.Close()

	if err := client.PaneSendKeys(ctx, pane, []string{"ctrl+c"}); err != nil {
		t.Fatalf("pane.send_keys: %v", err)
	}
	agents, err := client.AgentList(ctx)
	if err != nil || len(agents) != 0 {
		t.Fatalf("agent.list in a session with no agents = %+v, %v", agents, err)
	}
}

// waitForEvents reads the stream until every kind has been seen.
func waitForEvents(t *testing.T, stream EventStream, kinds ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, kind := range kinds {
		want[kind] = true
	}
	deadline := time.After(10 * time.Second)
	for len(want) > 0 {
		select {
		case event, ok := <-stream.Events():
			if !ok {
				t.Fatalf("event stream ended (%v) still waiting for %v", stream.Err(), want)
			}
			delete(want, event.Kind)
		case <-deadline:
			t.Fatalf("timed out waiting for events %v", want)
		}
	}
}

func trimmedLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return lines
}
