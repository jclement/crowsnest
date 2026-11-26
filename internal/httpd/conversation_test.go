// Tests for GET /agents/{target}/messages (paging, fallback, errors), POST /agents/{target}/prompt
// (sent, queued, draft, blocked, stalled) and the /orchestrator routes, against the fake herdr.

package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	testRule       = "\x1b[38;5;244m──────────────────────────────\x1b[39m"
	testEmptyInput = testRule + "\n❯ \x1b[7m \x1b[27m\n" + testRule + "\n"
	testDraftInput = testRule + "\n❯ draft at the terminal\x1b[7m \x1b[27m\n" + testRule + "\n"
)

// conversationHarness: a fake with the orchestrator (claude, transcript on disk with five user
// messages) and a codex agent with no transcript.
type conversationHarness struct {
	*harness
	fake   *fake.Fake
	orch   *orch.Orchestrator
	path   string // the orchestrator's transcript
	codex  string // the codex pane
	claude string
}

func newConversationHarness(t *testing.T) *conversationHarness {
	t.Helper()
	f := fake.New()
	_, _, orchPane := f.AddWorkspace("orchestrator", "/work/orch")
	_ = f.AddAgent(orchPane, orch.Name, "claude", herdr.StatusIdle, "orch-session")
	_ = f.SetScreen(orchPane, fleet.StripANSI(testEmptyInput), testEmptyInput)
	_, codexPane := f.AddTab("w1", "lint", "/work/orch")
	_ = f.AddAgent(codexPane, "linter", "codex", herdr.StatusIdle, "")
	_ = f.SetScreen(codexPane, "codex> no lint errors\n", "")

	claudeDir := t.TempDir()
	path := transcript.Path(claudeDir, "/work/orch", "orch-session")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	var lines strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&lines, `{"type":"user","uuid":"u%d","message":{"role":"user","content":"message %d"}}`+"\n", i, i)
	}
	_ = os.WriteFile(path, []byte(lines.String()), 0o600)

	db, _ := store.OpenMemory(context.Background())
	t.Cleanup(func() { _ = db.Close() })
	o := orch.New(orch.Options{Client: f, KV: db, Session: "crowsnest-test", Dir: t.TempDir(), StateDir: t.TempDir(),
		ShellWait: 100 * time.Millisecond, Logger: slog.New(slog.DiscardHandler)})
	if _, err := o.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager := transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Lookup: orch.TranscriptLookup(f, o), Fallback: orch.PaneFallback(f),
	})
	h := newHarness(t, harnessOptions{noAuth: true, herdr: f, conversations: manager, orchestrator: o})
	return &conversationHarness{harness: h, fake: f, orch: o, path: path, codex: codexPane, claude: claudeDir}
}

func (c *conversationHarness) messages(t *testing.T, target, query string) transcript.Page {
	t.Helper()
	response := c.do(request(http.MethodGet, "/api/v1/agents/"+url.PathEscape(target)+"/messages"+query, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("GET messages %s%s = %d %s", target, query, response.Code, response.Body)
	}
	return decodeData[transcript.Page](t, response.Body.Bytes())
}

func TestMessagesPaging(t *testing.T) {
	c := newConversationHarness(t)
	first := c.messages(t, "orchestrator", "?limit=3")
	if !first.Reset || !first.Earlier || len(first.Messages) != 3 || first.Messages[0].Text != "message 3" || first.Source != transcript.SourceTranscript {
		t.Fatalf("first page = %+v", first)
	}
	if again := c.messages(t, "orchestrator", "?after="+first.Cursor); again.Reset || len(again.Messages) != 0 {
		t.Fatalf("nothing new = %+v", again)
	}
	file, _ := os.OpenFile(c.path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = file.WriteString(`{"type":"assistant","uuid":"a1","message":{"id":"m","content":[{"type":"text","text":"**six**"}]}}` + "\n")
	_ = file.Close()
	// The same conversation addressed by pane id.
	next := c.messages(t, c.orch.Status().PaneID, "?after="+url.QueryEscape(first.Cursor))
	if next.Reset || len(next.Messages) != 1 || next.Messages[0].Kind != transcript.KindAssistant || next.Messages[0].Text != "**six**" {
		t.Fatalf("next page = %+v", next)
	}
}

func TestMessagesFallbackAndErrors(t *testing.T) {
	c := newConversationHarness(t)
	page := c.messages(t, c.codex, "")
	if page.Source != transcript.SourcePane || len(page.Messages) != 1 || page.Messages[0].Text != "codex> no lint errors" {
		t.Fatalf("fallback = %+v", page)
	}
	for path, want := range map[string]int{
		"/api/v1/agents/nobody/messages":              http.StatusNotFound,
		"/api/v1/agents/orchestrator/messages?limit=0": http.StatusBadRequest,
		"/api/v1/agents/orchestrator/messages?limit=x": http.StatusBadRequest,
		"/api/v1/agents/bad%20target/messages":         http.StatusBadRequest,
	} {
		if response := c.do(request(http.MethodGet, path, "")); response.Code != want {
			t.Errorf("GET %s = %d, want %d (%s)", path, response.Code, want, response.Body)
		}
	}
	empty := newHarness(t, harnessOptions{noAuth: true})
	if response := empty.do(request(http.MethodGet, "/api/v1/agents/x/messages", "")); response.Code != http.StatusServiceUnavailable {
		t.Errorf("without a manager = %d", response.Code)
	}
}

func prompt(c *conversationHarness, target, body string) *jsonResponse {
	response := c.do(request(http.MethodPost, "/api/v1/agents/"+url.PathEscape(target)+"/prompt", body))
	return &jsonResponse{code: response.Code, body: response.Body.Bytes()}
}

type jsonResponse struct {
	code int
	body []byte
}

func (r *jsonResponse) errorDetail(t *testing.T) (string, json.RawMessage) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code   string          `json:"code"`
			Detail json.RawMessage `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &envelope); err != nil {
		t.Fatalf("not an error: %s", r.body)
	}
	return envelope.Error.Code, envelope.Error.Detail
}

func TestPromptEndpoint(t *testing.T) {
	c := newConversationHarness(t)
	response := prompt(c, "orchestrator", `{"text":"status please"}`)
	if response.code != http.StatusOK {
		t.Fatalf("prompt = %d %s", response.code, response.body)
	}
	result := decodeData[orch.PromptResult](t, response.body)
	if !result.Started || result.Queued || result.Status != herdr.StatusWorking {
		t.Fatalf("result = %+v", result)
	}
	if prompts := c.fake.Prompts(); len(prompts) != 1 || prompts[0].Text != "status please" {
		t.Fatalf("prompts = %+v", prompts)
	}
	// Now it is working: a second send is queued.
	queued := decodeData[orch.PromptResult](t, prompt(c, "orchestrator", `{"text":"and this"}`).body)
	if !queued.Queued {
		t.Fatalf("second send = %+v", queued)
	}
}

func TestPromptEndpointRefusals(t *testing.T) {
	c := newConversationHarness(t)
	pane := c.orch.Status().PaneID
	_ = c.fake.SetScreen(pane, fleet.StripANSI(testDraftInput), testDraftInput)
	response := prompt(c, "orchestrator", `{"text":"hello"}`)
	code, detail := response.errorDetail(t)
	if response.code != http.StatusConflict || code != codeDraft || !strings.Contains(string(detail), "draft at the terminal") {
		t.Fatalf("draft = %d %s", response.code, response.body)
	}
	if forced := prompt(c, "orchestrator", `{"text":"hello","force_over_draft":true}`); forced.code != http.StatusOK {
		t.Fatalf("forced = %d %s", forced.code, forced.body)
	}

	_ = c.fake.SetStatus(pane, herdr.StatusBlocked)
	_ = c.fake.SetScreen(pane, fleet.StripANSI(dialogScreen), dialogScreen)
	response = prompt(c, "orchestrator", `{"text":"hello"}`)
	code, detail = response.errorDetail(t)
	var blocked BlockedDetail
	_ = json.Unmarshal(detail, &blocked)
	if response.code != http.StatusConflict || code != codeBlocked || !blocked.Dialog.Present || blocked.ScreenHash == "" {
		t.Fatalf("blocked = %d %s", response.code, response.body)
	}

	for body, want := range map[string]int{`{"text":""}`: 400, `not json`: 400, `{"text":"` + strings.Repeat("x", orch.MaxPromptBytes+1) + `"}`: 400} {
		if got := prompt(c, "orchestrator", body); got.code != want {
			t.Errorf("body %.20q = %d, want %d", body, got.code, want)
		}
	}
	if got := prompt(c, "nobody", `{"text":"hi"}`); got.code != http.StatusNotFound {
		t.Errorf("unknown agent = %d", got.code)
	}
}

func TestPromptEndpointStalled(t *testing.T) {
	c := newConversationHarness(t)
	c.fake.FailNext("agent.prompt", herdr.ErrAgentPromptStalled)
	response := prompt(c, "orchestrator", `{"text":"hello"}`)
	result := decodeData[orch.PromptResult](t, response.body)
	if response.code != http.StatusOK || result.Started || result.Note == "" {
		t.Fatalf("stalled = %d %+v", response.code, result)
	}
}

func TestOrchestratorEndpoints(t *testing.T) {
	c := newConversationHarness(t)
	response := c.do(request(http.MethodGet, "/api/v1/orchestrator", ""))
	status := decodeData[orch.Status](t, response.Body.Bytes())
	if response.Code != http.StatusOK || status.State != orch.StateIdle || status.SessionID != "orch-session" {
		t.Fatalf("GET /orchestrator = %d %s", response.Code, response.Body)
	}
	response = c.do(request(http.MethodPost, "/api/v1/orchestrator/new", ""))
	fresh := decodeData[orch.Status](t, response.Body.Bytes())
	if response.Code != http.StatusOK || fresh.SessionID == "orch-session" || fresh.SessionID == "" {
		t.Fatalf("POST /orchestrator/new = %d %s", response.Code, response.Body)
	}
	response = c.do(request(http.MethodPost, "/api/v1/orchestrator/restart", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("POST /orchestrator/restart = %d %s", response.Code, response.Body)
	}

	none := newHarness(t, harnessOptions{noAuth: true})
	for _, r := range []*http.Request{request(http.MethodGet, "/api/v1/orchestrator", ""), request(http.MethodPost, "/api/v1/orchestrator/restart", "")} {
		if response := none.do(r); response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s without orchestrator = %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestConversationRoutesRequireAuth(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/agents/orchestrator/messages", ""),
		request(http.MethodPost, "/api/v1/agents/orchestrator/prompt", `{"text":"hi"}`),
		request(http.MethodGet, "/api/v1/orchestrator", ""),
		request(http.MethodPost, "/api/v1/orchestrator/restart", ""),
		request(http.MethodPost, "/api/v1/orchestrator/new", ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated = %d", r.Method, r.URL.Path, response.Code)
		}
	}
	// A cross-site POST is refused before auth even looks at it.
	cross := request(http.MethodPost, "/api/v1/agents/orchestrator/prompt", `{"text":"hi"}`)
	cross.Header.Set("Origin", "https://evil.example")
	if response := h.do(cross); response.Code != http.StatusForbidden {
		t.Errorf("cross-origin prompt = %d", response.Code)
	}
}
