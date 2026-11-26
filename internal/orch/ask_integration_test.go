//go:build integration

// The real-Claude test for M6 (DESIGN.md section 15, "E2E real Claude"): a real orchestrator on
// haiku, started with crowsnest's --mcp-config and --settings, is asked to call the `ask` MCP
// tool. The question must appear through the browser API with the orchestrator's pane as its
// source (so the agent token crowsnest wrote into mcp.json reached the MCP server and the socket
// mapped it to the pane; R1b N2, N7), without Claude stopping at a permission
// prompt under --permission-mode auto; it is answered through the browser API, and the answer
// line must arrive in the orchestrator's transcript, typed back by the deliverer.
//
// Opt-in and paid: -tags integration and CROWSNEST_E2E_CLAUDE=1 (`mise run test:claude`). Same
// safety rules as integration_test.go: a throwaway crowsnest-test-* session, HERDR_* scrubbed.

package orch_test

import (
	"context"
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

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const askPrompt = "Call your crowsnest `ask` tool (mcp__crowsnest__ask) exactly once with: title \"Which fruit for the test?\", " +
	"kind \"choice\", options apple (label \"Apple\") and kiwi (label \"Kiwi\"), recommend \"apple\", and wait false. " +
	"Do not use any other tool. After the call returns, reply with one line: asked. When an answer message arrives later, reply with the fruit's name only."

// tokenPattern finds agent tokens, kept out of the test log.
var tokenPattern = regexp.MustCompile(`cnt_[A-Za-z0-9_-]+`)

func TestIntegrationClaudeAsk(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	short, err := os.MkdirTemp("/tmp", "cn-ask-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	binary := buildCrowsnest(t, short)

	session := startSession(t)
	dir, err := filepath.Abs("e2e-orchestrator-" + randomSuffix(t))
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
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Questions, the deliverer and the agent socket, wired as serve wires them.
	events := hub.New()
	var deliverer *questions.Deliverer
	service := questions.New(questions.Options{Store: db, Hub: events, Logger: logger, OnSettled: func(questions.Question) { deliverer.Kick() }})
	deliverer = questions.NewDeliverer(questions.DelivererOptions{Store: db, Prompt: questions.HerdrPrompter(session.Client), Logger: logger, Interval: time.Second})
	go deliverer.Run(ctx)
	socket := filepath.Join(short, "agent.sock")
	listener, err := agentapi.Listen(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	identity := agentid.New(db, nil)
	go func() {
		_ = agentapi.NewServer(agentapi.ServerOptions{Questions: service, Identity: identity, Hub: events, Logger: logger}).Serve(ctx, listener)
	}()

	stateDir := t.TempDir()
	// CROWSNEST_E2E_PERMISSION_MODE probes other modes (M6 checked "default" too; DESIGN.md 7.6).
	mode := os.Getenv("CROWSNEST_E2E_PERMISSION_MODE")
	if mode == "" {
		mode = "auto"
	}
	o := orch.New(orch.Options{
		Client: session.Client, KV: db, Session: session.Name, Dir: dir, StateDir: stateDir,
		Model: "haiku", PermissionMode: mode, Version: "integration", ClaudeConfigDir: claudeDir,
		BinDir: filepath.Dir(binary), BasePath: os.Getenv("PATH"), Executable: binary, AgentSocket: socket, Logger: logger,
		Identity: identity,
	})
	status, err := o.Ensure(ctx)
	if err != nil || status.State == orch.StateNeedsTerminal {
		t.Fatalf("Ensure: %+v %v", status, err)
	}
	for _, name := range []string{orch.SettingsFile, orch.MCPConfigFile} {
		body, _ := os.ReadFile(filepath.Join(stateDir, name))
		t.Logf("%s: %s", name, tokenPattern.ReplaceAllString(strings.TrimSpace(string(body)), "cnt_<redacted>"))
	}

	manager := transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Lookup: orch.TranscriptLookup(session.Client, o), Fallback: orch.PaneFallback(session.Client),
	})
	ts := askAPIServer(t, session.Client, o, manager, db, service)

	var before transcript.Page
	call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages", nil, &before)
	var result orch.PromptResult
	if code := call(t, ts, http.MethodPost, "/api/v1/agents/orchestrator/prompt", orch.PromptRequest{Text: askPrompt}, &result); code != http.StatusOK || !result.Started {
		t.Fatalf("POST prompt = %d %+v", code, result)
	}
	sent := time.Now()

	question := waitForQuestion(t, ctx, ts, session.Client, status.PaneID)
	t.Logf("question %s appeared %s after the prompt: %+v", question.ID, time.Since(sent).Round(time.Millisecond), question)
	if question.SourcePane != status.PaneID {
		t.Errorf("source pane %q, want the orchestrator's %q (its agent token did not reach the socket through the MCP server)", question.SourcePane, status.PaneID)
	}
	if question.Wait {
		t.Errorf("the orchestrator asked with wait=true; the prompt said false")
	}

	waitIdle(t, ts, o)
	var answered questions.Question
	if code := call(t, ts, http.MethodPost, "/api/v1/questions/"+question.ID+"/answer",
		questions.AnswerInput{Value: []byte(`"kiwi"`), Note: "the green one"}, &answered); code != http.StatusOK {
		t.Fatalf("answer = %d", code)
	}
	t.Logf("answered: delivery=%s", answered.Delivery)
	if answered.Delivery != questions.DeliveryType {
		t.Fatalf("a non-blocking ask must be typed back, got delivery %q", answered.Delivery)
	}
	answerLine, reply := waitForAnswerInTranscript(t, ts, before.Cursor, question.ID)
	t.Logf("answer line in the transcript: %q; reply: %q", answerLine, reply)
	stored, _ := service.Get(ctx, question.ID)
	if stored.DeliveredAt == nil {
		t.Errorf("delivered_at not recorded: %+v", stored)
	}
}

// buildCrowsnest compiles the binary Claude will start as `crowsnest mcp`.
func buildCrowsnest(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(dir, "crowsnest")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/crowsnest")
	build.Env = append(herdr.ScrubEnv(os.Environ()), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building crowsnest: %v\n%s", err, output)
	}
	return binary
}

func askAPIServer(t *testing.T, client herdr.Client, o *orch.Orchestrator, manager *transcript.Manager, db *store.Store, service *questions.Service) *httptest.Server {
	t.Helper()
	authService, err := auth.New(context.Background(), auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: hub.New(), Logger: slog.New(slog.DiscardHandler), Version: "integration",
		Herdr: client, Conversations: manager, Orchestrator: o, Questions: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// waitForQuestion polls the browser API for an open question, failing early (with the screen) if
// the orchestrator stops at a dialog, which would mean the MCP tool prompted for permission.
func waitForQuestion(t *testing.T, ctx context.Context, ts *httptest.Server, client herdr.Client, paneID string) questions.Question {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var open []questions.Question
		call(t, ts, http.MethodGet, "/api/v1/questions?status=open", nil, &open)
		if len(open) > 0 {
			return open[0]
		}
		read, err := client.PaneRead(ctx, paneID, herdr.ReadOptions{Source: herdr.SourceVisible})
		if err == nil && fleet.DetectDialog(fleet.StripANSI(read.Text)).Present {
			t.Fatalf("the orchestrator stopped at a dialog instead of calling ask (a permission prompt?):\n%s", read.Text)
		}
		time.Sleep(time.Second)
	}
	t.Fatal("no question arrived through the browser API within 2 minutes")
	return questions.Question{}
}

// waitForAnswerInTranscript waits for the typed answer line (a user message) in the transcript,
// and for the reply after it when one comes.
func waitForAnswerInTranscript(t *testing.T, ts *httptest.Server, cursor, id string) (string, string) {
	t.Helper()
	answerLine, reply := "", ""
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) && (answerLine == "" || reply == "") {
		var page transcript.Page
		call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages?after="+cursor, nil, &page)
		for _, message := range page.Messages {
			if message.Kind == transcript.KindUser && strings.Contains(message.Text, "[crowsnest answer "+id+"]") {
				answerLine = message.Text
			}
			if answerLine != "" && message.Kind == transcript.KindAssistant && strings.Contains(strings.ToLower(message.Text), "kiwi") {
				reply = message.Text
			}
		}
		if page.Cursor != "" {
			cursor = page.Cursor
		}
		time.Sleep(time.Second)
	}
	if answerLine == "" {
		t.Fatal("the answer line never reached the orchestrator's transcript")
	}
	if !strings.Contains(answerLine, "Kiwi") || !strings.Contains(answerLine, "the green one") {
		t.Errorf("answer line %q lacks the label or the note", answerLine)
	}
	return answerLine, reply
}
