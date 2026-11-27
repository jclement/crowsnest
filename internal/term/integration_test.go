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

// dismissOnboarding clears herdr's first-run screen if it is up. A machine whose herdr TUI was
// never opened (a CI runner, a fresh container) shows it over everything, and it swallows
// ctrl+b q. Enter takes its "continue", which opens settings at the integrations page, and Esc
// closes that. herdr then records `onboarding = false` in its own config, as any first use of
// its TUI would; on a machine already past it this does nothing.
func dismissOnboarding(t *testing.T, bridge Bridge, seen *bytes.Buffer) {
	t.Helper()
	readUntilQuiet(t, bridge, seen, 500*time.Millisecond, 5*time.Second)
	if !strings.Contains(fleet.StripANSI(seen.String()), "terminal workspace manager") {
		return
	}
	t.Log("herdr is showing its first-run screen; continuing past it")
	seen.Reset()
	_, _ = bridge.Write([]byte("\r"))
	waitForText(t, bridge, seen, "esc close", 10*time.Second)
	_, _ = bridge.Write([]byte{0x1b})
	readUntilQuiet(t, bridge, seen, 500*time.Millisecond, 5*time.Second)
}

// readUntilQuiet reads output into seen until none has arrived for quiet, or for at most limit.
func readUntilQuiet(t *testing.T, bridge Bridge, seen *bytes.Buffer, quiet, limit time.Duration) {
	t.Helper()
	deadline := time.After(limit)
	for {
		select {
		case chunk, ok := <-bridge.Output():
			if !ok {
				t.Fatalf("output ended (%s) while waiting for the screen to settle", bridge.Reason())
			}
			seen.Write(chunk)
		case <-time.After(quiet):
			return
		case <-deadline:
			return
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
	dismissOnboarding(t, again, &seen)
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

// fakeMachineLabel is the saved SSH machine planted in a throwaway state directory: herdr's
// client draws saved machines in its sidebar by label. Its target cannot resolve (.invalid).
const fakeMachineLabel = "CNFAKEBOX"

// plantSavedMachine writes herdr's saved-machine catalog (the format `herdr machine list --json`
// reads, found on herdr 0.9.5: a 32-hex id, label, target, session, enabled) under a throwaway
// "user" state directory and points this test's XDG_STATE_HOME at it, so a client that inherits
// the environment reads it as the user's own. The real ~/.local/state/herdr is never read or
// written.
func plantSavedMachine(t *testing.T) string {
	t.Helper()
	userState := t.TempDir()
	clientDir := userState + "/herdr/client"
	if err := os.MkdirAll(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := `{"version":1,"ssh":[{"id":"0123456789abcdef0123456789abcdef","label":"` + fakeMachineLabel +
		`","target":"crowsnest-test@nowhere.invalid","session":"default","enabled":true}]}`
	if err := os.WriteFile(clientDir+"/endpoints.json", []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", userState)
	return userState
}

// TestIntegrationTUIClientDoesNotSeeSavedMachines is R4 5.5: with a state directory of
// crowsnest's own (the default) the Terminal view's client attaches to the session but shows
// none of the user's saved SSH machines; with --terminal-machines (no state directory) it
// inherits the user's and shows them. The user's catalog is left as it was.
func TestIntegrationTUIClientDoesNotSeeSavedMachines(t *testing.T) {
	session, _ := testSession(t)
	userState := plantSavedMachine(t)
	before, _ := os.ReadFile(userState + "/herdr/client/endpoints.json")

	own := t.TempDir() + "/terminal-client"
	isolated := HerdrLauncher{Session: session.Name, ClientStateDir: own}.TUI()
	if err := isolated.Start(140, 40); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = isolated.Close() })
	var seen bytes.Buffer
	waitForText(t, isolated, &seen, "itest", 10*time.Second)
	dismissOnboarding(t, isolated, &seen)
	readUntilQuiet(t, isolated, &seen, time.Second, 5*time.Second)
	if strings.Contains(fleet.StripANSI(seen.String()), fakeMachineLabel) {
		t.Fatalf("the isolated client shows the user's saved machine %s", fakeMachineLabel)
	}
	_ = isolated.Close()
	waitForEnd(t, isolated, 5*time.Second)

	optIn := HerdrLauncher{Session: session.Name}.TUI()
	if err := optIn.Start(140, 40); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = optIn.Close() })
	seen.Reset()
	waitForText(t, optIn, &seen, fakeMachineLabel, 15*time.Second)
	_ = optIn.Close()
	waitForEnd(t, optIn, 5*time.Second)

	after, _ := os.ReadFile(userState + "/herdr/client/endpoints.json")
	if !bytes.Equal(before, after) {
		t.Errorf("the user's saved-machine catalog changed:\nbefore %s\nafter  %s", before, after)
	}
}
