//go:build integration

// The real-Claude smoke test for M4 (DESIGN.md section 15, "E2E real Claude"): a throwaway herdr
// session, a real orchestrator started by Ensure on a cheap model, one prompt sent through the
// HTTP API, and the assistant's reply read back through the HTTP messages API from the real
// transcript. It costs a few cents, so it runs only with -tags integration AND
// CROWSNEST_E2E_CLAUDE=1 (`mise run test:claude`).
//
// Safety, enforced here rather than by convention: the session is named crowsnest-test-<random>
// and nothing else is created, stopped or deleted; every herdr child gets an environment without
// HERDR_*; the orchestrator's directory is a fresh folder under this package (inside the
// worktree, which Claude already trusts), removed at the end.

package orch_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

var testSessionPattern = regexp.MustCompile(`^crowsnest-test-[a-z0-9]+$`)

func randomSuffix(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 5)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(suffix)
}

// startSession starts a throwaway herdr session and registers its teardown.
func startSession(t *testing.T) *herdr.Session {
	t.Helper()
	name := "crowsnest-test-" + randomSuffix(t)
	if !testSessionPattern.MatchString(name) {
		t.Fatalf("refusing session %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := herdr.EnsureSession(ctx, herdr.SessionOptions{Name: name, LogDir: t.TempDir(), StartTimeout: 20 * time.Second})
	t.Cleanup(func() { stopSession(t, name) })
	if err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	if !session.Started {
		t.Fatalf("session %s was already running", name)
	}
	t.Logf("herdr session %s (herdr %s)", name, session.Pong.Version)
	return session
}

func stopSession(t *testing.T, name string) {
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

// apiServer serves the real HTTP API (no auth, loopback) over the given orchestrator.
func apiServer(t *testing.T, client herdr.Client, o *orch.Orchestrator, manager *transcript.Manager, db *store.Store) *httptest.Server {
	t.Helper()
	authService, err := auth.New(context.Background(), auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: hub.New(), Logger: slog.New(slog.DiscardHandler), Version: "integration",
		Herdr: client, Conversations: manager, Orchestrator: o,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func call(t *testing.T, ts *httptest.Server, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, ts.URL+path, reader)
	request.Header.Set("Origin", ts.URL)
	request.Header.Set("X-Crowsnest", "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	if out != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Data != nil {
			_ = json.Unmarshal(envelope.Data, out)
		}
	}
	if response.StatusCode != http.StatusOK {
		t.Logf("%s %s = %d %s", method, path, response.StatusCode, raw)
	}
	return response.StatusCode
}

func TestIntegrationClaudeOrchestrator(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
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
	dir, err := filepath.Abs("e2e-orchestrator-" + randomSuffix(t))
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

	o := orch.New(orch.Options{
		Client: session.Client, KV: db, Session: session.Name, Dir: dir, StateDir: t.TempDir(),
		Model: "haiku", PermissionMode: "auto", Version: "integration", ClaudeConfigDir: claudeDir,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	started := time.Now()
	status, err := o.Ensure(ctx)
	if err != nil {
		t.Fatalf("Ensure: %v (status %+v)", err, status)
	}
	t.Logf("Ensure took %s: state=%s pane=%s session=%s", time.Since(started).Round(time.Millisecond), status.State, status.PaneID, status.SessionID)
	if status.State == orch.StateNeedsTerminal {
		t.Fatalf("Claude stopped at a start-up prompt in %s; is the worktree trusted?", dir)
	}
	// Idempotent: a second Ensure adopts the running agent.
	if again, err := o.Ensure(ctx); err != nil || again.SessionID != status.SessionID || again.PaneID != status.PaneID {
		t.Fatalf("second Ensure = %+v, %v", again, err)
	}

	manager := transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Lookup: orch.TranscriptLookup(session.Client, o), Fallback: orch.PaneFallback(session.Client),
	})
	ts := apiServer(t, session.Client, o, manager, db)

	var before transcript.Page
	if code := call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages", nil, &before); code != http.StatusOK {
		t.Fatalf("GET messages = %d", code)
	}
	t.Logf("before the prompt: source=%s messages=%d cursor=%s", before.Source, len(before.Messages), before.Cursor)

	var result orch.PromptResult
	sent := time.Now()
	if code := call(t, ts, http.MethodPost, "/api/v1/agents/orchestrator/prompt", orch.PromptRequest{Text: "Reply with exactly one word: pineapple"}, &result); code != http.StatusOK {
		t.Fatalf("POST prompt = %d", code)
	}
	t.Logf("prompt result after %s: %+v", time.Since(sent).Round(time.Millisecond), result)
	if !result.Started {
		t.Fatalf("the turn did not start: %+v", result)
	}

	cursor, reply := before.Cursor, ""
	deadline := time.Now().Add(2 * time.Minute)
	for reply == "" && time.Now().Before(deadline) {
		var page transcript.Page
		call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages?after="+cursor, nil, &page)
		for _, message := range page.Messages {
			t.Logf("message seq=%d kind=%s tag=%s text=%q", message.Seq, message.Kind, message.Tag, truncate(message.Text, 80))
			if message.Kind == transcript.KindAssistant && strings.Contains(strings.ToLower(message.Text), "pineapple") {
				reply = message.Text
			}
		}
		cursor = page.Cursor
		time.Sleep(time.Second)
	}
	if reply == "" {
		t.Fatal("no assistant reply containing pineapple arrived through the messages API within 2 minutes")
	}
	t.Logf("assistant reply arrived %s after the prompt: %q", time.Since(sent).Round(time.Millisecond), reply)

	// Restart resumes the same conversation; New conversation starts a fresh one. Neither sends
	// a prompt, so neither costs tokens.
	waitIdle(t, ts, o)
	var restarted orch.Status
	began := time.Now()
	if code := call(t, ts, http.MethodPost, "/api/v1/orchestrator/restart", nil, &restarted); code != http.StatusOK {
		t.Fatalf("POST restart = %d", code)
	}
	t.Logf("restart took %s: %+v", time.Since(began).Round(time.Millisecond), restarted)
	if restarted.SessionID != status.SessionID || restarted.State == orch.StateFailed {
		t.Fatalf("restart did not resume %s: %+v", status.SessionID, restarted)
	}
	var fresh orch.Status
	began = time.Now()
	if code := call(t, ts, http.MethodPost, "/api/v1/orchestrator/new", nil, &fresh); code != http.StatusOK {
		t.Fatalf("POST new = %d", code)
	}
	t.Logf("new conversation took %s: %+v", time.Since(began).Round(time.Millisecond), fresh)
	if fresh.SessionID == status.SessionID || fresh.SessionID == "" || fresh.PaneID != status.PaneID {
		t.Fatalf("new conversation = %+v", fresh)
	}
	var page transcript.Page
	call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages", nil, &page)
	t.Logf("after new conversation: source=%s session=%s messages=%d", page.Source, page.SessionID, len(page.Messages))
	for _, message := range page.Messages {
		if strings.Contains(message.Text, "pineapple") {
			t.Fatalf("the new conversation still shows the old one: %+v", page)
		}
	}

	var final orch.Status
	call(t, ts, http.MethodGet, "/api/v1/orchestrator", nil, &final)
	fmt.Fprintf(os.Stderr, "final orchestrator status: %+v\n", final)
}

// waitIdle waits for the orchestrator's turn to end, so a restart does not interrupt it.
func waitIdle(t *testing.T, ts *httptest.Server, o *orch.Orchestrator) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		o.Watch(context.Background()) // serve runs this on a timer; the test has no Run loop
		var status orch.Status
		call(t, ts, http.MethodGet, "/api/v1/orchestrator", nil, &status)
		if status.State == orch.StateIdle {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("the orchestrator did not go idle")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
