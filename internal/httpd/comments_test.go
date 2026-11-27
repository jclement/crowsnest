package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/store"
)

// commentHarness is a diff (j_1 resolves to a repository whose commit adds func A to a.go) and a
// jobs service whose j_1 is a running job in that repository, over the fake herdr.
func commentHarness(t *testing.T) (*harness, *fake.Fake, *http.Cookie) {
	t.Helper()
	dir := changesRepo(t)
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := fake.New()
	jobService := jobs.New(jobs.Options{Store: db, Herdr: f, Synchronous: true,
		Launch: jobs.LaunchOptions{StateDir: t.TempDir(), Executable: "/bin/crowsnest", AgentSocket: "/tmp/test.sock"}})
	off := false
	if _, err := jobService.Create(context.Background(), jobs.CreateInput{Brief: "Add A", Dir: dir, Isolate: &off}, ""); err != nil {
		t.Fatal(err)
	}
	attachments, err := attach.New(attach.Options{Store: db, Dir: filepath.Join(t.TempDir(), "attachments")})
	if err != nil {
		t.Fatal(err)
	}
	service := changes.New(changes.Options{Store: db, Resolver: oneJob{root: dir}})
	h := newHarness(t, harnessOptions{changes: service, jobs: jobService, herdr: f, attachments: attachments, attachLayout: attach.Layout{StateDir: t.TempDir()}})
	return h, f, h.login()
}

func fileHash(t *testing.T, h *harness, cookie *http.Cookie) string {
	t.Helper()
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/changes/jobs/j_1?scope=committed", ""), cookie))
	var summary struct {
		Data changes.Summary `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil || len(summary.Data.Files) != 1 {
		t.Fatalf("summary = %s", response.Body)
	}
	return summary.Data.Files[0].Hash
}

func commentList(t *testing.T, h *harness, cookie *http.Cookie) changes.CommentList {
	t.Helper()
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/changes/jobs/j_1/comments", ""), cookie))
	var list struct {
		Data changes.CommentList `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &list) != nil {
		t.Fatalf("comments = %d %s", response.Code, response.Body)
	}
	return list.Data
}

func TestCommentRoutesNeedASessionAndTheAppsHeader(t *testing.T) {
	h, _, cookie := commentHarness(t)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/changes/jobs/j_1/comments", ""),
		request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", `{"path":"a.go"}`),
		request(http.MethodPatch, "/api/v1/changes/jobs/j_1/comments/1", `{"body":"x"}`),
		request(http.MethodDelete, "/api/v1/changes/jobs/j_1/comments/1", ""),
		request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/1/dismiss", ""),
		request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{}`),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d", r.Method, r.URL, response.Code)
		}
	}
	hash := fileHash(t, h, cookie)
	body := `{"path":"a.go","scope":"committed","side":"new","line_start":3,"line_end":3,"body":"rename A","hash":"` + hash + `"}`
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-Crowsnest") },
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
	} {
		forged := withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", body), cookie)
		change(forged)
		if response := h.do(forged); response.Code != http.StatusForbidden {
			t.Errorf("a cross-site comment = %d", response.Code)
		}
		send := withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{}`), cookie)
		change(send)
		if response := h.do(send); response.Code != http.StatusForbidden {
			t.Errorf("a cross-site send = %d", response.Code)
		}
	}
	if list := commentList(t, h, cookie); len(list.Comments) != 0 {
		t.Fatalf("a forged request left a comment: %+v", list.Comments)
	}
}

func TestCommentAndSendToTheJob(t *testing.T) {
	h, f, cookie := commentHarness(t)
	hash := fileHash(t, h, cookie)
	body := `{"path":"a.go","scope":"committed","side":"new","line_start":3,"line_end":3,"body":"rename A to Add","hash":"` + hash + `"}`
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", body), cookie))
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"quote":"func A() {}"`) {
		t.Fatalf("add = %d %s", response.Code, response.Body)
	}
	stale := strings.Replace(body, hash, "nope", 1)
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", stale), cookie)); response.Code != http.StatusConflict {
		t.Errorf("a stale hash = %d", response.Code)
	}
	if list := commentList(t, h, cookie); list.Drafts != 1 {
		t.Fatalf("drafts = %+v", list)
	}
	// The preview sends nothing (the one prompt so far is the job's brief).
	before := len(f.Prompts())
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{"overall":"Nearly there","dry_run":true}`), cookie))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "func A() {}") || len(f.Prompts()) != before {
		t.Fatalf("preview = %d %s (prompts %d)", response.Code, response.Body, len(f.Prompts()))
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{"overall":"Nearly there"}`), cookie))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sent":true`) {
		t.Fatalf("send = %d %s", response.Code, response.Body)
	}
	typed := 0
	for _, prompt := range f.Prompts() {
		if strings.HasPrefix(prompt.Text, "[crowsnest message j_1] Review comments") && strings.Contains(prompt.Text, "   > func A() {}") &&
			strings.Contains(prompt.Text, "Comment: rename A to Add") && strings.Contains(prompt.Text, "Overall: Nearly there") {
			typed++
		}
	}
	if typed != 1 {
		t.Fatalf("one message with the note and its quote, got %d: %+v", typed, f.Prompts())
	}
	if list := commentList(t, h, cookie); list.Drafts != 0 || list.Sent != 1 || list.LastSentAt == nil {
		t.Fatalf("after send = %+v", list)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{}`), cookie)); response.Code != http.StatusBadRequest {
		t.Errorf("a send with nothing pending = %d", response.Code)
	}
}

func TestALongReviewGoesAsAnAttachedFile(t *testing.T) {
	h, f, cookie := commentHarness(t)
	hash := fileHash(t, h, cookie)
	note := strings.Repeat("Please say more about this. ", 200) // over 4 KB with the rest
	body, _ := json.Marshal(map[string]any{"path": "a.go", "scope": "committed", "side": "file", "body": note, "hash": hash})
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", string(body)), cookie)); response.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", response.Code, response.Body)
	}
	before := len(f.Prompts())
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{}`), cookie))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `review-comments.md"`) {
		t.Fatalf("send = %d %s", response.Code, response.Body)
	}
	prompts := f.Prompts()[before:]
	if len(prompts) != 1 || !strings.Contains(prompts[0].Text, "in the attached file") || strings.Contains(prompts[0].Text, note) {
		t.Fatalf("typed = %+v", prompts)
	}
}

func TestCommentEditDismissAndResend(t *testing.T) {
	h, _, cookie := commentHarness(t)
	hash := fileHash(t, h, cookie)
	body := `{"path":"a.go","scope":"committed","side":"new","line_start":1,"line_end":3,"body":"one","hash":"` + hash + `"}`
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", body), cookie)); response.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", response.Code, response.Body)
	}
	id := commentList(t, h, cookie).Comments[0].ID
	path := "/api/v1/changes/jobs/j_1/comments/" + itoa(id)
	if response := h.do(withCookie(request(http.MethodPatch, path, `{"body":"two"}`), cookie)); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"body":"two"`) {
		t.Fatalf("edit = %d %s", response.Code, response.Body)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{}`), cookie)); response.Code != http.StatusOK {
		t.Fatalf("send = %d %s", response.Code, response.Body)
	}
	if response := h.do(withCookie(request(http.MethodPost, path+"/resend", ""), cookie)); response.Code != http.StatusOK || commentList(t, h, cookie).Drafts != 1 {
		t.Fatalf("resend = %d %s", response.Code, response.Body)
	}
	if response := h.do(withCookie(request(http.MethodPost, path+"/dismiss", ""), cookie)); response.Code != http.StatusOK || len(commentList(t, h, cookie).Comments) != 0 {
		t.Fatalf("dismiss = %d %s", response.Code, response.Body)
	}
	if response := h.do(withCookie(request(http.MethodDelete, "/api/v1/changes/jobs/j_1/comments/abc", ""), cookie)); response.Code != http.StatusNotFound {
		t.Errorf("a bad id = %d", response.Code)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
