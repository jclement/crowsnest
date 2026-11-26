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

// App schedules over HTTP: create, edit, switch, delete; task files are read-only; ?project=.
func TestScheduleAppRoutes(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "check.md"), []byte("---\nevery: 15m\n---\n# Anything stuck?\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := schedule.New(schedule.Options{
		KV: db, Store: db, StateDir: t.TempDir(), Sources: []schedule.Source{{Dir: dir, Origin: schedule.OriginDir}},
		Projects: func(context.Context) map[string]int64 { return map[string]int64{"alpha": 5} },
	})
	h := newHarness(t, harnessOptions{schedule: service})
	cookie := h.login()
	send := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		return h.do(withCookie(request(method, target, body), cookie))
	}
	list := func(response *httptest.ResponseRecorder) schedule.View {
		t.Helper()
		var body struct{ Data schedule.View }
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Data
	}
	digest := `{"name":"digest","every":"1d","after":"17:00","project_id":5,"body_md":"# Digest\nSummarise alpha."}`

	for _, r := range []*http.Request{
		request(http.MethodPost, "/api/v1/schedule", digest),
		request(http.MethodPut, "/api/v1/schedule/check", `{"enabled":false}`),
		request(http.MethodDelete, "/api/v1/schedule/check", ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d", r.Method, r.URL, response.Code)
		}
	}
	noCSRF := withCookie(request(http.MethodPost, "/api/v1/schedule", digest), cookie)
	noCSRF.Header.Del("X-Crowsnest")
	if response := h.do(noCSRF); response.Code != http.StatusForbidden {
		t.Fatalf("without the CSRF header: %d", response.Code)
	}

	response := send(http.MethodPost, "/api/v1/schedule", digest)
	view := list(response)
	if response.Code != http.StatusCreated || len(view.Tasks) != 2 {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	for _, want := range []string{`"origin":"app"`, `"editable":true`, `"target":"lead"`, `"project":"alpha"`, `"project_id":5`,
		`"body_md":"# Digest\nSummarise alpha."`, `"shadowed":false`, `"runs":[]`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("create response lacks %s: %s", want, response.Body)
		}
	}
	if response := send(http.MethodPost, "/api/v1/schedule", strings.Replace(digest, "digest", "check", 1)); response.Code != http.StatusConflict {
		t.Fatalf("a file's name: %d %s", response.Code, response.Body)
	}
	if response := send(http.MethodPost, "/api/v1/schedule", `{"name":"x","every":"1m","body_md":"x"}`); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "too often") {
		t.Fatalf("invalid: %d %s", response.Code, response.Body)
	}
	if response := send(http.MethodPost, "/api/v1/schedule", `{"name":"x","evry":"1h"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", response.Code)
	}

	if response := send(http.MethodGet, "/api/v1/schedule?project=5", ""); len(list(response).Tasks) != 1 || list(response).Tasks[0].Name != "digest" {
		t.Fatalf("project 5: %s", response.Body)
	}
	if response := send(http.MethodGet, "/api/v1/schedule?project=0", ""); len(list(response).Tasks) != 1 || list(response).Tasks[0].Name != "check" {
		t.Fatalf("project 0: %s", response.Body)
	}
	if response := send(http.MethodGet, "/api/v1/schedule?project=alpha", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("project by slug: %d", response.Code)
	}

	edit := `{"every":"4h","as":"job","dir":"/tmp/alpha","role":"builder","engine":"codex","project_id":5,"body_md":"Build alpha.","enabled":false}`
	response = send(http.MethodPut, "/api/v1/schedule/digest", edit)
	if view = list(response); response.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", response.Code, response.Body)
	}
	for _, row := range view.Tasks {
		if row.Name == "digest" && (row.Target != schedule.TargetJob || row.Engine != "codex" || row.Enabled || row.Every != "4h") {
			t.Fatalf("edited row: %+v", row)
		}
	}
	if response := send(http.MethodPut, "/api/v1/schedule/digest", `{"every":"4h"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("a partial edit is a full replacement and needs a body: %d %s", response.Code, response.Body)
	}
	if response := send(http.MethodPut, "/api/v1/schedule/check", `{"every":"1h","body_md":"x"}`); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "read-only: edit the file") {
		t.Fatalf("edit a file: %d %s", response.Code, response.Body)
	}
	if response := send(http.MethodPut, "/api/v1/schedule/check", `{"enabled":false}`); response.Code != http.StatusOK {
		t.Fatalf("a file's switch stays the app's: %d %s", response.Code, response.Body)
	}
	if response := send(http.MethodPut, "/api/v1/schedule/check", `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("empty PUT: %d", response.Code)
	}

	if response := send(http.MethodDelete, "/api/v1/schedule/check", ""); response.Code != http.StatusConflict {
		t.Fatalf("delete a file: %d %s", response.Code, response.Body)
	}
	response = send(http.MethodDelete, "/api/v1/schedule/digest", "")
	if view = list(response); response.Code != http.StatusOK || len(view.Tasks) != 1 {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	if response := send(http.MethodDelete, "/api/v1/schedule/digest", ""); response.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", response.Code)
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
