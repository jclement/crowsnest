// The folder picker's routes: owner session only (no session, a read token: 401), read-only
// (no other method), refusals mapped to crowsnest's own sentences, and the facts call naming
// the project that holds a folder.

package httpd

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/browse"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/store"
)

// browseFixture is a home with a git repo and a project in it, and a folder outside.
type browseFixture struct {
	home, repo, outside string
	service             *browse.Service
	projects            *projects.Service
}

func newBrowseFixture(t *testing.T, fsys browse.FS) browseFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := browseFixture{home: filepath.Join(base, "home"), outside: filepath.Join(base, "outside")}
	f.repo = filepath.Join(f.home, "Developer", "repo")
	for _, dir := range []string{filepath.Join(f.repo, ".git"), filepath.Join(f.repo, "app"), f.outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@ssh.dev.azure.com:v3/o/p/r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.projects = projects.New(projects.Options{Store: db, Home: f.home})
	name, dir := "Repo", f.repo
	if _, err := f.projects.Create(context.Background(), projects.Input{Name: &name, Dir: &dir}); err != nil {
		t.Fatal(err)
	}
	f.service = browse.New(browse.Options{Home: f.home, FS: fsys, Timeout: 100 * time.Millisecond})
	return f
}

func TestBrowseRoutesNeedTheOwnersSession(t *testing.T) {
	f := newBrowseFixture(t, nil)
	h := newHarness(t, harnessOptions{browse: f.service, machines: true, ops: fakeOps{}})
	paths := []string{"/api/v1/browse", "/api/v1/browse/facts?path=~", "/api/v1/browse/places"}
	for _, path := range paths {
		if code := h.do(request(http.MethodGet, path, "")).Code; code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", path, code)
		}
	}
	cookie := h.login()
	minted := h.do(withCookie(request(http.MethodPost, "/api/v1/tokens", `{"name":"bedev"}`), cookie))
	var body struct {
		Data mintResponse `json:"data"`
	}
	_ = json.Unmarshal(minted.Body.Bytes(), &body)
	if body.Data.Token == "" {
		t.Fatalf("mint: %d %s", minted.Code, minted.Body)
	}
	for _, path := range paths {
		r := request(http.MethodGet, path, "")
		r.Header.Set("Authorization", "Bearer "+body.Data.Token)
		if code := h.do(r).Code; code != http.StatusUnauthorized {
			t.Errorf("%s with a read token: %d, want 401", path, code)
		}
		if code := h.do(withCookie(request(http.MethodGet, path, ""), cookie)).Code; code != http.StatusOK {
			t.Errorf("%s with the session: %d, want 200", path, code)
		}
	}
	// Nothing writes: no other method is routed (the API's catch-all answers 404).
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if code := h.do(withCookie(request(method, "/api/v1/browse", `{}`), cookie)).Code; code < 400 {
			t.Errorf("%s /browse: %d, want a refusal", method, code)
		}
	}
}

func TestBrowseListAndRefusals(t *testing.T) {
	f := newBrowseFixture(t, nil)
	h := newHarness(t, harnessOptions{browse: f.service, projects: f.projects})
	cookie := h.login()
	get := func(path string) (int, map[string]any, map[string]any) {
		t.Helper()
		response := h.do(withCookie(request(http.MethodGet, path, ""), cookie))
		var out struct {
			Data  map[string]any `json:"data"`
			Error map[string]any `json:"error"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return response.Code, out.Data, out.Error
	}
	code, listing, _ := get("/api/v1/browse?path=" + "~/Developer")
	if code != http.StatusOK || listing["path"] != filepath.Join(f.home, "Developer") {
		t.Fatalf("list: %d %v", code, listing)
	}
	entries, _ := listing["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["git"] != true {
		t.Fatalf("entries %v, want the repo marked git", entries)
	}
	cases := []struct {
		path string
		code int
		says string
	}{
		{f.outside, http.StatusForbidden, "outside what the picker may browse"},
		{f.home + "/../outside", http.StatusForbidden, "outside"},
		{"relative", http.StatusBadRequest, "absolute path"},
		{f.home + "/nope", http.StatusNotFound, "no such folder"},
	}
	for _, tc := range cases {
		code, _, failure := get("/api/v1/browse?path=" + tc.path)
		message, _ := failure["message"].(string)
		if code != tc.code || !strings.Contains(message, tc.says) {
			t.Errorf("%s: %d %q, want %d saying %q", tc.path, code, message, tc.code, tc.says)
		}
		if strings.Contains(message, f.home) {
			t.Errorf("%s: the refusal echoes a path: %q", tc.path, message)
		}
	}

	code, facts, _ := get("/api/v1/browse/facts?path=" + f.repo)
	project, _ := facts["project"].(map[string]any)
	if code != http.StatusOK || facts["git"] != true || facts["host"] != browse.HostAzure || facts["branch"] != "main" || project["exact"] != true || project["name"] != "Repo" {
		t.Fatalf("facts: %d %v", code, facts)
	}
	_, inside, _ := get("/api/v1/browse/facts?path=" + filepath.Join(f.repo, "app"))
	if holder, _ := inside["project"].(map[string]any); holder["exact"] != false || inside["git_top"] != f.repo {
		t.Fatalf("facts inside the project: %v", inside)
	}
	_, above, _ := get("/api/v1/browse/facts?path=~/Developer")
	if contains, _ := above["contains"].([]any); len(contains) != 1 || above["project"] != nil {
		t.Fatalf("facts above the project: %v", above)
	}
	_, places, _ := get("/api/v1/browse/places")
	if places["home"] != f.home {
		t.Fatalf("places: %v", places)
	}
}

// hangingFS never answers ReadDir: a dead network mount.
type hangingFS struct {
	browse.OS
	release chan struct{}
}

func (f hangingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	<-f.release
	return f.OS.ReadDir(name)
}

func TestBrowseSlowMountIsATimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f := newBrowseFixture(t, hangingFS{release: release})
	h := newHarness(t, harnessOptions{browse: f.service})
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/browse?path=~", ""), h.login()))
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), "TIMEOUT") {
		t.Fatalf("slow mount: %d %s", response.Code, response.Body)
	}
}

func TestBrowseUnavailableWithoutTheService(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if code := h.do(withCookie(request(http.MethodGet, "/api/v1/browse", ""), h.login())).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("no browse service: %d, want 503", code)
	}
}
