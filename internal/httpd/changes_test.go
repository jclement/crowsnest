package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/store"
)

// oneJob resolves j_1 to a checkout and nothing else.
type oneJob struct{ root string }

func (o oneJob) Job(_ context.Context, id string) (changes.Target, error) {
	if id != "j_1" {
		return changes.Target{}, &changes.Error{Kind: "not_found", Message: "no job " + id}
	}
	return changes.Target{Subject: "j_1", Kind: changes.KindJob, JobID: "j_1", Label: "j_1", Root: o.root, BaseBranch: "main"}, nil
}
func (o oneJob) Project(context.Context, string) (changes.Target, error) {
	return changes.Target{}, &changes.Error{Kind: "not_found", Message: "no project"}
}
func (o oneJob) Orchestrator(context.Context) (changes.Target, error) {
	return changes.Target{}, &changes.Error{Kind: "not_found", Message: "no orchestrator"}
}
func (o oneJob) ProjectJobs(context.Context, string) ([]changes.Target, error) { return nil, nil }
func (o oneJob) Retired(context.Context, string) bool                          { return false }

func changesHarness(t *testing.T, requireViewed bool) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for key, value := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "T",
		"GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(key, value)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "first")
	git("checkout", "-q", "-b", "job/1")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "A")
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := changes.New(changes.Options{Store: db, Resolver: oneJob{root: dir}})
	return newHarness(t, harnessOptions{changes: service, requireViewed: func() bool { return requireViewed }})
}

func TestChangesRoutesNeedASession(t *testing.T) {
	h := changesHarness(t, false)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/changes/jobs/j_1", ""),
		request(http.MethodGet, "/api/v1/changes/jobs/j_1/file?path=a.go", ""),
		request(http.MethodPut, "/api/v1/changes/jobs/j_1/viewed", `{"all":true}`),
		request(http.MethodGet, "/api/v1/changes/progress", ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d", r.Method, r.URL, response.Code)
		}
	}
}

func TestChangesSummaryFileAndMarks(t *testing.T) {
	h := changesHarness(t, false)
	cookie := h.login()
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/changes/jobs/j_1?scope=committed", ""), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", response.Code, response.Body)
	}
	var summary struct {
		Data changes.Summary `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil || summary.Data.Total != 1 {
		t.Fatalf("summary = %s (%v)", response.Body, err)
	}
	hash := summary.Data.Files[0].Hash
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/changes/jobs/j_1/file?path=a.go&scope=committed", ""), cookie)); response.Code != http.StatusOK {
		t.Fatalf("file = %d %s", response.Code, response.Body)
	}
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/changes/jobs/j_1/file?path=../../etc/passwd", ""), cookie)); response.Code != http.StatusNotFound {
		t.Errorf("a path outside the diff = %d", response.Code)
	}
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/changes/jobs/j_9", ""), cookie)); response.Code != http.StatusNotFound {
		t.Errorf("an unknown job = %d", response.Code)
	}
	// A state change without the app's header or origin is refused (CSRF), with a session too.
	bare := httptest.NewRequest(http.MethodPut, "/api/v1/changes/jobs/j_1/viewed", nil)
	bare.Host, bare.RemoteAddr = testHost, "127.0.0.1:50000"
	if response := h.do(withCookie(bare, cookie)); response.Code != http.StatusForbidden {
		t.Errorf("a PUT without origin = %d", response.Code)
	}
	stale := `{"scope":"committed","files":[{"path":"a.go","hash":"nope"}]}`
	if response := h.do(withCookie(request(http.MethodPut, "/api/v1/changes/jobs/j_1/viewed", stale), cookie)); response.Code != http.StatusConflict {
		t.Errorf("a stale hash = %d %s", response.Code, response.Body)
	}
	body := `{"scope":"committed","files":[{"path":"a.go","hash":"` + hash + `"}]}`
	response = h.do(withCookie(request(http.MethodPut, "/api/v1/changes/jobs/j_1/viewed", body), cookie))
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &summary) != nil || summary.Data.Viewed != 1 {
		t.Fatalf("mark = %d %s", response.Code, response.Body)
	}
	response = h.do(withCookie(request(http.MethodDelete, "/api/v1/changes/jobs/j_1/viewed", `{"scope":"committed"}`), cookie))
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &summary) != nil || summary.Data.Viewed != 0 {
		t.Fatalf("clear = %d %s", response.Code, response.Body)
	}
}

func TestLandGateOnUnviewedFiles(t *testing.T) {
	ctx := context.Background()
	off := changesHarness(t, false)
	recorder := httptest.NewRecorder()
	if off.server.landUnviewed(ctx, recorder, "j_1", "merge") {
		t.Fatal("refused with the setting off")
	}
	on := changesHarness(t, true)
	if on.server.landUnviewed(ctx, httptest.NewRecorder(), "j_1", "discard") {
		t.Error("a discard was refused for unviewed files")
	}
	recorder = httptest.NewRecorder()
	if !on.server.landUnviewed(ctx, recorder, "j_1", "merge") || recorder.Code != http.StatusConflict {
		t.Fatalf("not refused: %d %s", recorder.Code, recorder.Body)
	}
	if body := decodeError(t, recorder); body.Code != "UNVIEWED" {
		t.Errorf("code = %q", body.Code)
	}
	if _, err := on.server.deps.Changes.Mark(ctx, changes.Ask{Kind: changes.KindJob, Ref: "j_1"}, changes.ScopeAll, nil, true); err != nil {
		t.Fatal(err)
	}
	if on.server.landUnviewed(ctx, httptest.NewRecorder(), "j_1", "merge") {
		t.Error("refused once every file was viewed")
	}
}
