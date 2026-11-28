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

	"github.com/jclement/crowsnest/internal/agenttrust"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/jobs"
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

// fakeFolderTrust records what it was asked to trust.
type fakeFolderTrust struct {
	on      bool
	asked   [][]string
	trusted map[string]bool
}

func (f *fakeFolderTrust) Enabled() bool { return f.on }
func (f *fakeFolderTrust) Trust(dirs []string) []agenttrust.Result {
	f.asked = append(f.asked, dirs)
	for _, dir := range dirs {
		f.trusted[dir] = true
	}
	return []agenttrust.Result{{Agent: "claude", Status: agenttrust.StatusTrusted}}
}
func (f *fakeFolderTrust) Trusted(dir string) bool { return f.trusted[dir] }

// Creating a project records folder trust only when the human ticked the box and the setting is
// on; POST …/trust does it for an existing project, and is refused while the setting is off.
func TestProjectFolderTrust(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	mk := func(name string) string {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		resolved, _ := filepath.EvalSymlinks(dir)
		return resolved
	}
	ticked, unticked, later := mk("ticked"), mk("unticked"), mk("later")
	trust := &fakeFolderTrust{on: true, trusted: map[string]bool{}}
	h := newHarness(t, harnessOptions{projects: projects.New(projects.Options{Store: db, Home: home}), folderTrust: trust})
	cookie := h.login()
	create := func(name, dir, extra string) (int, map[string]any) {
		response := h.do(withCookie(request(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":%q,"dir":%q%s}`, name, dir, extra)), cookie))
		var out struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return response.Code, out.Data
	}
	code, view := create("Ticked", ticked, `,"trust_folder":true`)
	if code != http.StatusCreated || len(trust.asked) != 1 || trust.asked[0][0] != ticked {
		t.Fatalf("ticked: %d %v asked %v", code, view, trust.asked)
	}
	if view["folder_trusted"] != true || view["trust"] == nil {
		t.Errorf("the view should say the folder is trusted and what was done: %v", view)
	}
	code, view = create("Unticked", unticked, ``)
	if code != http.StatusCreated || len(trust.asked) != 1 || view["folder_trusted"] != false {
		t.Fatalf("unticked must not write: %d %v asked %v", code, view, trust.asked)
	}
	trust.on = false
	code, _ = create("Later", later, `,"trust_folder":true`)
	if code != http.StatusCreated || len(trust.asked) != 1 {
		t.Fatalf("setting off must not write: %d asked %v", code, trust.asked)
	}
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/projects/later/trust", ""), cookie))
	if response.Code != http.StatusConflict || len(trust.asked) != 1 {
		t.Fatalf("trust with the setting off: %d %s", response.Code, response.Body)
	}
	trust.on = true
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/projects/later/trust", ""), cookie))
	if response.Code != http.StatusOK || len(trust.asked) != 2 || trust.asked[1][0] != later {
		t.Fatalf("trust: %d %s asked %v", response.Code, response.Body, trust.asked)
	}
}

// R9-S4 and R9-S5: a project's view names what its folder runs at agent start, and its .codex/
// with whether it is allowed for Codex jobs. POST …/codex-config allows exactly the fingerprint
// the page showed (a stale one is a 409), and trusting the folder (whose banner listed the .codex
// files) allows it too.
func TestProjectAgentStartAndCodexConfig(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	mk := func(name string) string {
		dir := filepath.Join(home, name)
		for path, body := range map[string]string{
			".codex/hooks.json":     `{"hooks": {}}`,
			".claude/settings.json": `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "x"}]}]}}`,
		} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		resolved, _ := filepath.EvalSymlinks(dir)
		return resolved
	}
	allowed, trusted := mk("allowed"), mk("trusted")
	jobService := jobs.New(jobs.Options{Store: db})
	trust := &fakeFolderTrust{on: true, trusted: map[string]bool{}}
	h := newHarness(t, harnessOptions{projects: projects.New(projects.Options{Store: db, Home: home}), folderTrust: trust, jobs: jobService})
	cookie := h.login()
	call := func(method, target, body string) (int, map[string]any) {
		t.Helper()
		response := h.do(withCookie(request(method, target, body), cookie))
		var out struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return response.Code, out.Data
	}
	code, view := call(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":"Allowed","dir":%q}`, allowed))
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, view)
	}
	findings, _ := json.Marshal(view["agent_start"])
	if !strings.Contains(string(findings), `{"kind":"settings","path":".claude/settings.json","runs":true}`) || !strings.Contains(string(findings), `{"kind":"codex","path":".codex/hooks.json","runs":true}`) {
		t.Errorf("agent_start: %s", findings)
	}
	config, _ := view["codex_config"].(map[string]any)
	if config == nil || config["seen"] != false || config["fingerprint"] == "" {
		t.Fatalf("codex_config: %v", view["codex_config"])
	}
	if code, _ := call(http.MethodPost, "/api/v1/projects/allowed/codex-config", `{"fingerprint":"not-what-the-page-showed"}`); code != http.StatusConflict {
		t.Errorf("a stale fingerprint: %d", code)
	}
	code, view = call(http.MethodPost, "/api/v1/projects/allowed/codex-config", fmt.Sprintf(`{"fingerprint":%q}`, config["fingerprint"]))
	if after, _ := view["codex_config"].(map[string]any); code != http.StatusOK || after["seen"] != true {
		t.Fatalf("allow: %d %v", code, view["codex_config"])
	}

	// The form sends the .codex/ it showed beside the box (R10 S5a); trusting is seeing exactly that.
	shown, err := agenttrust.CodexConfigOf(trusted)
	if err != nil {
		t.Fatal(err)
	}
	code, view = call(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":"Trusted","dir":%q,"trust_folder":true,"codex_fingerprint":%q}`, trusted, shown.Fingerprint))
	if after, _ := view["codex_config"].(map[string]any); code != http.StatusCreated || after["seen"] != true {
		t.Fatalf("trusting the folder did not allow its listed .codex/: %d %v", code, view["codex_config"])
	}

	// R10 S5a: the .codex/ changed after the page was drawn. Creating with the box ticked trusts
	// nothing and marks nothing seen; the project page's Trust this folder is refused (409); with
	// what is there now it goes ahead. A client that sends no fingerprint trusts, but sees nothing.
	stale := mk("stale")
	before, _ := agenttrust.CodexConfigOf(stale)
	if err := os.WriteFile(filepath.Join(stale, ".codex", "hooks.json"), []byte(`{"hooks": {"Stop": []}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := len(trust.asked)
	code, view = call(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":"Stale","dir":%q,"trust_folder":true,"codex_fingerprint":%q}`, stale, before.Fingerprint))
	if after, _ := view["codex_config"].(map[string]any); code != http.StatusCreated || after["seen"] != false || len(trust.asked) != asked {
		t.Fatalf("a changed .codex/ must stop the trust: %d %v asked %d→%d", code, view["codex_config"], asked, len(trust.asked))
	}
	if code, _ := call(http.MethodPost, "/api/v1/projects/stale/trust", fmt.Sprintf(`{"codex_fingerprint":%q}`, before.Fingerprint)); code != http.StatusConflict || len(trust.asked) != asked {
		t.Fatalf("Trust this folder over a changed .codex/: %d", code)
	}
	now, _ := agenttrust.CodexConfigOf(stale)
	code, view = call(http.MethodPost, "/api/v1/projects/stale/trust", fmt.Sprintf(`{"codex_fingerprint":%q}`, now.Fingerprint))
	if after, _ := view["codex_config"].(map[string]any); code != http.StatusOK || after["seen"] != true || len(trust.asked) != asked+1 {
		t.Fatalf("Trust this folder with what is there: %d %v", code, view["codex_config"])
	}
	quiet := mk("quiet") // its own .codex/ (seen is per fingerprint, and the others were seen)
	if err := os.WriteFile(filepath.Join(quiet, ".codex", "hooks.json"), []byte(`{"hooks": {"PreToolUse": []}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, view = call(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":"Quiet","dir":%q,"trust_folder":true}`, quiet))
	if after, _ := view["codex_config"].(map[string]any); code != http.StatusCreated || after["seen"] != false || len(trust.asked) != asked+2 {
		t.Fatalf("no fingerprint sent: trusted, nothing seen: %d %v", code, view["codex_config"])
	}
}
