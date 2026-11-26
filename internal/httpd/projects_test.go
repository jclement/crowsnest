// The project routes (DESIGN.md 21.1, 21.2, 10): a session and the CSRF header are required,
// validation errors are 400 with the human's sentence, a project is read by id or slug, archived
// and back, and the lead actions start and stop a lead on the fake herdr.

package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/store"
)

func TestProjectRoutes(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	service := projects.New(projects.Options{Store: db, Home: home})
	herdrFake := fake.New()
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	leads := projects.NewLeads(ctx, service, db, func(p projects.Project) (*orch.Orchestrator, *orch.Poker) {
		return orch.New(orch.Options{Client: herdrFake, KV: db, Dir: p.Dir, StateDir: filepath.Join(state, p.Slug),
			AgentName: projects.AgentName(p.Slug), Workspace: "lead " + p.Slug, KVPrefix: "lead." + p.Slug,
			ShellWait: 200 * time.Millisecond, Logger: slog.New(slog.DiscardHandler)}), nil
	}, slog.New(slog.DiscardHandler), nil)
	h := newHarness(t, harnessOptions{projects: service, leads: leads})
	cookie := h.login()
	send := func(method, target, body string) (int, map[string]any) {
		t.Helper()
		response := h.do(withCookie(request(method, target, body), cookie))
		var out struct {
			Data  map[string]any `json:"data"`
			Error map[string]any `json:"error"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		if out.Error != nil {
			return response.Code, out.Error
		}
		return response.Code, out.Data
	}
	create := fmt.Sprintf(`{"name":"Alpha","dir":%q}`, repo)

	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/projects", ""),
		request(http.MethodPost, "/api/v1/projects", create),
		request(http.MethodPost, "/api/v1/projects/1/lead/start", ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d", r.Method, r.URL, response.Code)
		}
	}
	noCSRF := withCookie(request(http.MethodPost, "/api/v1/projects", create), cookie)
	noCSRF.Header.Del("X-Crowsnest")
	if response := h.do(noCSRF); response.Code != http.StatusForbidden {
		t.Fatalf("without the CSRF header: %d", response.Code)
	}

	if code, body := send(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":"Home","dir":%q}`, home)); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["message"]), "too wide") {
		t.Fatalf("home as a project: %d %v", code, body)
	}
	if code, _ := send(http.MethodPost, "/api/v1/projects", `{"name":"x","dir":"/","bogus":1}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}
	code, created := send(http.MethodPost, "/api/v1/projects", create)
	if code != http.StatusCreated || created["slug"] != "alpha" {
		t.Fatalf("create: %d %v", code, created)
	}
	if code, got := send(http.MethodGet, "/api/v1/projects/alpha", ""); code != http.StatusOK || got["lead"].(map[string]any)["state"] != "off" {
		t.Fatalf("get by slug: %d %v", code, got)
	}
	if code, _ := send(http.MethodGet, "/api/v1/projects/nope", ""); code != http.StatusNotFound {
		t.Fatalf("missing: %d", code)
	}
	if code, got := send(http.MethodPost, "/api/v1/projects/alpha/lead/start", ""); code != http.StatusOK || got["running"] != true {
		t.Fatalf("start lead: %d %v", code, got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, got := send(http.MethodGet, "/api/v1/projects/1", "")
		if got["lead"].(map[string]any)["state"] == "idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lead never idle: %v", got["lead"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code, list := send(http.MethodGet, "/api/v1/projects", ""); code != http.StatusOK || len(list["projects"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	if code, _ := send(http.MethodPost, "/api/v1/projects/alpha/lead/dance", ""); code != http.StatusNotFound {
		t.Fatalf("unknown action: %d", code)
	}
	if code, got := send(http.MethodPost, "/api/v1/projects/alpha/lead/stop", ""); code != http.StatusOK || got["state"] != "off" {
		t.Fatalf("stop lead: %d %v", code, got)
	}
	if code, got := send(http.MethodPut, "/api/v1/projects/alpha", `{"archived":true}`); code != http.StatusOK || got["archived"] != true {
		t.Fatalf("archive: %d %v", code, got)
	}
	if code, _ := send(http.MethodPost, "/api/v1/projects/alpha/lead/start", ""); code != http.StatusConflict {
		t.Fatalf("archived lead start: %d", code)
	}
	if code, list := send(http.MethodGet, "/api/v1/projects?archived=1", ""); code != http.StatusOK || len(list["projects"].([]any)) != 1 {
		t.Fatalf("archived listing: %d %v", code, list)
	}
}
