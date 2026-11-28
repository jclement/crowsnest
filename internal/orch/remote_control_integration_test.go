//go:build integration

// Remote Control against a real Claude (DESIGN.md 7.10): an orchestrator started by Ensure with
// `--remote-control <name>` in a throwaway herdr session comes up interactive (the flag is
// accepted and does not block the start), and its screen is read for what Claude Code says about
// Remote Control. No prompt is sent, so it costs no tokens beyond start-up. Opt-in like the other
// real-Claude tests (CROWSNEST_E2E_CLAUDE=1, `mise run test:claude`).

package orch_test

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

func TestIntegrationClaudeRemoteControl(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1")
	}
	for _, tool := range []string{"herdr", "claude"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	// Registered before the session, so it runs after the agents are gone: they record their
	// folders in the user's own files, and the test removes exactly its own (DESIGN 15).
	var forgetDir string
	t.Cleanup(func() { forgetScratch(t, forgetDir) })
	session := startSession(t)
	dir, err := filepath.Abs("e2e-rc-" + randomSuffix(t))
	forgetDir = dir
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	claudeDir, err := transcript.ConfigDir(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	name := "crowsnest-test rc " + randomSuffix(t)[:4]
	o := orch.New(orch.Options{
		Client: session.Client, KV: db, Session: session.Name, Dir: dir, StateDir: t.TempDir(),
		Model: "haiku", PermissionMode: "auto", Version: "integration", ClaudeConfigDir: claudeDir,
		RemoteControlName: name, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := o.Ensure(ctx)
	if err != nil {
		t.Fatalf("Ensure with --remote-control: %v (%+v)", err, status)
	}
	if status.State != orch.StateIdle || status.RemoteControl != name {
		t.Fatalf("status %+v", status)
	}
	// Give Claude a few seconds to register the session, then read what it shows.
	var screen string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		read, err := session.Client.PaneRead(ctx, status.PaneID, herdr.ReadOptions{Source: herdr.SourceRecent, Lines: 200})
		if err == nil {
			screen = read.Text
			if strings.Contains(strings.ToLower(screen), "remote-control is active") {
				break
			}
		}
		time.Sleep(time.Second)
	}
	t.Logf("pane after start:\n%s", screen)
	if !strings.Contains(strings.ToLower(screen), "remote-control is active") {
		t.Errorf("Claude did not report Remote Control active; see the log above")
	}
	o.Watch(ctx) // serve runs this on a timer
	got := o.Status()
	if got.RemoteControlNote != orch.RemoteControlActive {
		t.Errorf("status note %q, want %q (this login may not be able to use Remote Control; see the pane above)", got.RemoteControlNote, orch.RemoteControlActive)
		return
	}
	// The link is captured from the real banner (DESIGN.md 7.10), and it is the one on the screen.
	if !orch.ValidRemoteControlURL(got.RemoteControlURL) {
		t.Fatalf("Remote Control is active but no session URL was captured: %+v", got)
	}
	if want := orch.ParseRemoteControlURL(screen); want != got.RemoteControlURL {
		t.Errorf("captured %q, the screen shows %q", got.RemoteControlURL, want)
	}
	t.Logf("Remote Control session URL captured (%d characters)", len(got.RemoteControlURL))
}
