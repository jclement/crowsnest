// Tests for the InstructionsLoaded hook on the agent socket (DESIGN.md 21.3): whose files they
// are, that the size comes from a stat, and that the hook never fails.

package agentapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
)

func TestInstructionsLoadedHook(t *testing.T) {
	// The layers need the fixture's store, which exists only once startSocket returns: serve a
	// second socket over it with the layers wired.
	base := startSocket(t)
	f := startSocketOn(t, base, instructions.New(instructions.Options{Store: base.db}))
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(file, []byte("twelve bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := func(path string) json.RawMessage {
		body, _ := json.Marshal(map[string]any{"session_id": "s1", "cwd": dir, "file_path": path, "memory_type": "Project",
			"load_reason": "session_start", "hook_event_name": "InstructionsLoaded", "globs": []string{"*.go"}})
		return body
	}
	id := startRunningJob(t, f)
	cases := []struct {
		name    string
		pane    string // the token's pane ("" for no token); minted per case, since a new token for a pane replaces the old
		job     string
		body    json.RawMessage
		subject string // "" means nothing is recorded for anyone listed
	}{
		{"a job agent's token", "w5:p1", id, input(file), id},
		{"a relative path, against cwd", "w5:p1", id, input("CLAUDE.md"), id},
		{"the orchestrator's token", "w1:p1", "", input(file), "orchestrator"},
		{"a lead's pane", "w7:p1", "", input(file), "lead:crowsnest"},
		{"no token", "", "", input(file), ""},
		{"not JSON", "w1:p1", "", json.RawMessage(`"just a string"`), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := ""
			if tc.pane != "" {
				token = f.token(t, tc.pane, tc.job)
			}
			client := NewClient(f.socket, token)
			if tc.job != "" {
				client = client.WithJob(tc.job)
			}
			out, err := client.Hook(ctx, jobs.HookInstructionsLoaded, tc.body)
			if err != nil || out != "" {
				t.Fatalf("the hook must answer nothing and never fail: %q %v", out, err)
			}
			if tc.subject == "" {
				return
			}
			loaded, err := f.db.ListLoadedInstructions(ctx, tc.subject)
			if err != nil || len(loaded) != 1 || loaded[0].Path != file || loaded[0].Size != 12 || loaded[0].MemoryType != "Project" ||
				loaded[0].LoadReason != "session_start" || loaded[0].SessionID != "s1" {
				t.Fatalf("recorded for %s: %+v %v", tc.subject, loaded, err)
			}
		})
	}
	// The unidentified caller recorded nothing under a name of its own choosing.
	for _, subject := range []string{"orchestrator", "lead:crowsnest", id} {
		if loaded, _ := f.db.ListLoadedInstructions(ctx, subject); len(loaded) != 1 {
			t.Fatalf("%s: %d rows, want exactly 1", subject, len(loaded))
		}
	}
}

// startSocketOn serves a second agent API over the fixture's store, with the instruction layers
// wired, and returns a fixture pointing at it.
func startSocketOn(t *testing.T, base *socketFixture, layers *instructions.Service) *socketFixture {
	t.Helper()
	path := filepath.Join(shortDir(t), "run", "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = NewServer(ServerOptions{Questions: base.service, Identity: base.identity, Jobs: base.jobs, Hub: base.hub, Version: "test",
			Instructions: layers, SubjectOf: func(_ context.Context, pane string) string {
				if pane == "w7:p1" {
					return "lead:crowsnest"
				}
				return ""
			}}).Serve(ctx, listener)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	copied := *base
	copied.socket = path
	return &copied
}
