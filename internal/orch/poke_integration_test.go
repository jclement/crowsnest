//go:build integration

// The real-Claude poke test (M8, DESIGN.md 7.4 and 15): a real orchestrator on haiku in a
// throwaway crowsnest-test-* session is given a manual poke with one synthetic reason through
// POST /orchestrator/poke; the poke must be delivered (outcome sent, the turn started), appear in
// the transcript tagged `poke`, and be answered. Same safety rules and cost gate as
// integration_test.go (`mise run test:claude`).

package orch_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

func TestIntegrationClaudePoke(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	session := startSession(t)
	dir, err := filepath.Abs("e2e-poke-" + randomSuffix(t))
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	o := orch.New(orch.Options{
		Client: session.Client, KV: db, Session: session.Name, Dir: dir, StateDir: t.TempDir(),
		Model: "haiku", PermissionMode: "auto", Version: "integration", ClaudeConfigDir: claudeDir, Logger: logger,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	status, err := o.Ensure(ctx)
	if err != nil || status.State == orch.StateNeedsTerminal {
		t.Fatalf("Ensure: %v (status %+v)", err, status)
	}
	log := journal.New(db, nil, logger, nil)
	poker := orch.NewPoker(orch.PokerOptions{Client: session.Client, KV: db, Journal: log, Logger: logger, Status: o.Status})
	manager := transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Lookup: orch.TranscriptLookup(session.Client, o), Fallback: orch.PaneFallback(session.Client),
	})
	authService, err := auth.New(ctx, auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: hub.New(), Logger: logger, Version: "integration",
		Herdr: session.Client, Conversations: manager, Orchestrator: o, Journal: log, Poker: poker,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	waitIdle(t, ts, o)

	var before transcript.Page
	call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages", nil, &before)

	poker.Add(ctx, orch.Reason{Key: "manual:e2e", Source: orch.SourceManual,
		Text: `job j_99 is done: "write the changelog" (a synthetic test reason: reply in one short sentence saying what you would check, and do not call any tools)`})
	var result orch.PokeResult
	sent := time.Now()
	if code := call(t, ts, http.MethodPost, "/api/v1/orchestrator/poke", nil, &result); code != http.StatusOK {
		t.Fatalf("POST poke = %d", code)
	}
	t.Logf("poke result after %s: %+v", time.Since(sent).Round(time.Millisecond), result)
	if result.Outcome != orch.OutcomeSent || !result.Started || len(result.Reasons) != 1 {
		t.Fatalf("the poke was not delivered: %+v", result)
	}

	cursor, pokeSeen, reply := before.Cursor, false, ""
	deadline := time.Now().Add(2 * time.Minute)
	for (reply == "" || !pokeSeen) && time.Now().Before(deadline) {
		var page transcript.Page
		call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages?after="+cursor, nil, &page)
		for _, message := range page.Messages {
			t.Logf("message seq=%d kind=%s tag=%s text=%q", message.Seq, message.Kind, message.Tag, truncate(message.Text, 120))
			if message.Kind == transcript.KindUser && message.Tag == transcript.TagPoke && strings.Contains(message.Text, "j_99") {
				pokeSeen = true
			}
			if pokeSeen && message.Kind == transcript.KindAssistant && strings.TrimSpace(message.Text) != "" {
				reply = message.Text
			}
		}
		cursor = page.Cursor
		time.Sleep(time.Second)
	}
	if !pokeSeen || reply == "" {
		t.Fatalf("poke in transcript=%v, reply=%q within 2 minutes", pokeSeen, reply)
	}
	t.Logf("the orchestrator answered the poke %s after it was sent: %q", time.Since(sent).Round(time.Millisecond), reply)
	page, _ := log.List(ctx, journal.Query{Kinds: []journal.Kind{journal.KindPoke}})
	if page.Total != 1 {
		t.Fatalf("poke journal entries: %d", page.Total)
	}
}
