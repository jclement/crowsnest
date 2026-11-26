//go:build integration

// Integration tests for the bridges against a real herdr (`mise run test:integration`). Each
// test gets its own throwaway session named crowsnest-test-<random>, started headless and
// stopped and deleted at the end; the helper refuses any other name, and every herdr child runs
// with HERDR_* removed from its environment (the bridges do this themselves too), so a test run
// from inside a herdr pane cannot reach that pane's session (DESIGN.md section 15).

package term

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
)

var testSessionPattern = regexp.MustCompile(`^crowsnest-test-[a-z0-9]+$`)

// testSession is a fresh headless session with one workspace, and the id of its shell pane.
func testSession(t *testing.T) (*herdr.Session, string) {
	t.Helper()
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr is not installed; skipping the real-herdr integration tests")
	}
	suffix := make([]byte, 5)
	_, _ = rand.Read(suffix)
	name := "crowsnest-test-" + hex.EncodeToString(suffix)
	if !testSessionPattern.MatchString(name) {
		t.Fatalf("refusing to create session %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := herdr.EnsureSession(ctx, herdr.SessionOptions{
		Name: name, LogDir: t.TempDir(), StartTimeout: 20 * time.Second, Environ: herdr.ScrubEnv(os.Environ()),
	})
	t.Cleanup(func() { teardownSession(t, name) })
	if err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	if !session.Started {
		t.Fatalf("session %s was already running; a fresh random session should not be", name)
	}
	created, err := session.Client.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{Label: "itest", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("creating a workspace: %v", err)
	}
	time.Sleep(1500 * time.Millisecond) // let the shell draw its first prompt
	return session, created.RootPane.ID
}

func teardownSession(t *testing.T, name string) {
	t.Helper()
	if !testSessionPattern.MatchString(name) {
		t.Errorf("refusing to tear down %q", name)
		return
	}
	for _, args := range [][]string{{"session", "stop", name, "--json"}, {"session", "delete", name, "--json"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "herdr", args...)
		cmd.Env = herdr.ScrubEnv(os.Environ())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("herdr %s: %v: %s", strings.Join(args, " "), err, output)
		}
		cancel()
	}
}

// waitForText reads output until its ANSI-stripped text contains want.
func waitForText(t *testing.T, bridge Bridge, seen *bytes.Buffer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for !strings.Contains(fleet.StripANSI(seen.String()), want) {
		select {
		case chunk, ok := <-bridge.Output():
			if !ok {
				t.Fatalf("output ended (%s) before %q appeared; saw %q", bridge.Reason(), want, fleet.StripANSI(seen.String()))
			}
			seen.Write(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for %q; saw %q", want, fleet.StripANSI(seen.String()))
		}
	}
}

func waitForEnd(t *testing.T, bridge Bridge, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-bridge.Output():
			if !ok {
				return bridge.Reason()
			}
		case <-deadline:
			t.Fatalf("output did not end within %s", timeout)
		}
	}
}

// randomEcho returns a command whose output exists only if the shell ran it: the expected
// text is computed by the shell, so the typed command echoing back does not count.
func randomEcho() (command, want string) {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("echo crow-$((%d+1))\r", n.Int64()), fmt.Sprintf("crow-%d", n.Int64()+1)
}

func TestIntegrationPaneBridgeRunsACommand(t *testing.T) {
	session, paneID := testSession(t)
	bridge := HerdrLauncher{Session: session.Name}.Pane(paneID)
	if err := bridge.Start(100, 30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	var seen bytes.Buffer
	command, want := randomEcho()
	if _, err := bridge.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	waitForText(t, bridge, &seen, want, 10*time.Second)
	if err := bridge.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	_ = bridge.Close()
	if reason := waitForEnd(t, bridge, 5*time.Second); reason != "detached" && reason != ReasonClosed {
		t.Errorf("reason after Close = %q", reason)
	}
	// The pane is still there after the controller let go.
	if _, err := session.Client.PaneGet(context.Background(), paneID); err != nil {
		t.Errorf("pane after release: %v", err)
	}
}

func TestIntegrationTUIBridgeLeavesTheSessionRunning(t *testing.T) {
	session, paneID := testSession(t)
	bridge := HerdrLauncher{Session: session.Name}.TUI()
	if err := bridge.Start(120, 40); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	waitForText(t, bridge, &seen, "itest", 10*time.Second) // the workspace label in the sidebar
	_ = bridge.Close()
	reason := waitForEnd(t, bridge, 5*time.Second)
	t.Logf("TUI ended: %s (%d bytes of output)", reason, seen.Len())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.Client.Ping(ctx); err != nil {
		t.Fatalf("the herdr server did not survive the TUI client closing: %v", err)
	}
	if _, err := session.Client.PaneGet(ctx, paneID); err != nil {
		t.Fatalf("the pane did not survive the TUI client closing: %v", err)
	}

	// herdr's own detach key (ctrl+b q) ends the client cleanly, and the session lives on.
	again := HerdrLauncher{Session: session.Name}.TUI()
	if err := again.Start(120, 40); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	seen.Reset()
	waitForText(t, again, &seen, "itest", 10*time.Second)
	_, _ = again.Write([]byte{0x02})
	time.Sleep(200 * time.Millisecond)
	_, _ = again.Write([]byte("q"))
	if reason := waitForEnd(t, again, 10*time.Second); reason != "detached" {
		t.Errorf("after ctrl+b q the TUI ended with %q, want detached", reason)
	}
	if _, err := session.Client.Ping(ctx); err != nil {
		t.Fatalf("the herdr server did not survive a detach: %v", err)
	}
}

func TestIntegrationTakeoverClosesTheFirstController(t *testing.T) {
	session, paneID := testSession(t)
	launcher := HerdrLauncher{Session: session.Name}
	first := launcher.Pane(paneID)
	if err := first.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	select {
	case <-first.Output():
	case <-time.After(10 * time.Second):
		t.Fatal("first controller got no frame")
	}

	second := launcher.Pane(paneID)
	if err := second.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if reason := waitForEnd(t, first, 10*time.Second); reason != "terminal attach taken over" {
		t.Errorf("first controller ended with %q, want the takeover reason", reason)
	}
	var seen bytes.Buffer
	command, want := randomEcho()
	_, _ = second.Write([]byte(command))
	waitForText(t, second, &seen, want, 10*time.Second)
}
