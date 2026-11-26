// Tests for /schedule (the list, Run now, the switch) and /orchestrator/context and /compact.

package httpd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/schedule"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

func TestScheduleRoutes(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "check.md"), []byte("---\nevery: 15m\n---\n# Anything stuck?\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}
	kicked := 0
	service := schedule.New(schedule.Options{KV: db, Sources: []schedule.Source{{Dir: dir, Origin: schedule.OriginDir}}, Kick: func() { kicked++ }})
	h := newHarness(t, harnessOptions{schedule: service})
	cookie := h.login()
	list := func(response *httptest.ResponseRecorder) schedule.View {
		t.Helper()
		var body struct{ Data schedule.View }
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Data
	}
	if response := h.do(request(http.MethodGet, "/api/v1/schedule", "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", response.Code)
	}
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/schedule", ""), cookie))
	view := list(response)
	if response.Code != http.StatusOK || len(view.Tasks) != 1 || view.Tasks[0].Title != "Anything stuck?" || !view.Tasks[0].Enabled ||
		view.Tasks[0].NextDueAt == nil || len(view.Problems) != 1 || len(view.Dirs) != 1 {
		t.Fatalf("GET: %d %s", response.Code, response.Body)
	}
	response = h.do(withCookie(request(http.MethodPut, "/api/v1/schedule/check", `{"enabled":false}`), cookie))
	if view = list(response); response.Code != http.StatusOK || view.Tasks[0].Enabled || view.Tasks[0].NextDueAt != nil {
		t.Fatalf("PUT: %d %s", response.Code, response.Body)
	}
	if response := h.do(withCookie(request(http.MethodPut, "/api/v1/schedule/check", `{"enable":true}`), cookie)); response.Code != http.StatusBadRequest {
		t.Fatalf("misspelt body: %d", response.Code)
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/schedule/check/run", ""), cookie))
	if view = list(response); response.Code != http.StatusOK || !view.Tasks[0].RunNow || kicked != 1 {
		t.Fatalf("run: %d %s kicked=%d", response.Code, response.Body, kicked)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/schedule/nope/run", ""), cookie)); response.Code != http.StatusNotFound {
		t.Fatalf("unknown task: %d", response.Code)
	}
}

func TestOrchestratorContextAndCompact(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := fake.New()
	_, _, pane := f.AddWorkspace("orchestrator", "/o")
	_ = f.AddAgent(pane, orch.Name, "claude", herdr.StatusIdle, "s")
	_ = f.SetScreen(pane, fleet.StripANSI(journalEmptyInput), journalEmptyInput)
	o := orch.New(orch.Options{Client: f, KV: db, Dir: t.TempDir(), StateDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)})
	if _, err := o.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	tender := orch.NewTender(orch.TenderOptions{
		Client: f, KV: db, CompactAt: 150_000, FreshAt: -1,
		Usage: func(context.Context) (transcript.Usage, error) {
			return transcript.Usage{Tokens: 120_000, Known: true}, nil
		},
	})
	h := newHarness(t, harnessOptions{orchestrator: o, tender: tender, herdr: f})
	cookie := h.login()
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/orchestrator/context", ""), cookie))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tokens":120000`) || !strings.Contains(response.Body.String(), `"threshold":150000`) {
		t.Fatalf("context: %d %s", response.Code, response.Body)
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/orchestrator/compact", ""), cookie))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "last_compact_at") {
		t.Fatalf("compact: %d %s", response.Code, response.Body)
	}
	if prompts := f.Prompts(); len(prompts) != 1 || !strings.HasPrefix(prompts[0].Text, "/compact ") {
		t.Fatalf("typed %+v", prompts)
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/orchestrator/compact", ""), cookie))
	if response.Code != http.StatusConflict { // the fake is now working on the /compact
		t.Fatalf("second compact while working: %d %s", response.Code, response.Body)
	}
}
