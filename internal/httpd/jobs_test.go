package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/store"
)

// jobHarness is a harness with auth on and a jobs service over the fake herdr.
func jobHarness(t *testing.T) (*harness, *jobs.Service, *fake.Fake, *http.Cookie) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := fake.New()
	service := jobs.New(jobs.Options{Store: db, Herdr: f, Synchronous: true,
		Launch: jobs.LaunchOptions{StateDir: t.TempDir(), Executable: "/bin/crowsnest", AgentSocket: "/tmp/test.sock"}})
	h := newHarness(t, harnessOptions{jobs: service, herdr: f})
	return h, service, f, h.login()
}

func TestJobRoutesNeedASession(t *testing.T) {
	h, _, _, _ := jobHarness(t)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/jobs", ""),
		request(http.MethodPost, "/api/v1/jobs", `{"brief":"x"}`),
		request(http.MethodGet, "/api/v1/jobs/j_1", ""),
		request(http.MethodPost, "/api/v1/jobs/j_1/message", `{"text":"x"}`),
		request(http.MethodPost, "/api/v1/jobs/j_1/cancel", ""),
		request(http.MethodPost, "/api/v1/jobs/j_1/nudge", ""),
		request(http.MethodPost, "/api/v1/jobs/j_1/resume", ""),
		request(http.MethodGet, "/api/v1/roles", ""),
		request(http.MethodGet, "/api/v1/dirs", ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestJobCreateIsGuardedAgainstCrossSiteRequests(t *testing.T) {
	h, service, _, cookie := jobHarness(t)
	noHeader := withCookie(request(http.MethodPost, "/api/v1/jobs", `{"brief":"x"}`), cookie)
	noHeader.Header.Del("X-Crowsnest")
	foreign := withCookie(request(http.MethodPost, "/api/v1/jobs", `{"brief":"x"}`), cookie)
	foreign.Header.Set("Origin", "https://evil.example")
	for _, r := range []*http.Request{noHeader, foreign} {
		if response := h.do(r); response.Code != http.StatusForbidden {
			t.Errorf("cross-site create: %d", response.Code)
		}
	}
	if list, _ := service.List(context.Background(), nil); len(list) != 0 {
		t.Fatalf("a job was created: %+v", list)
	}
}

func TestJobCreateDetailAndActions(t *testing.T) {
	h, _, f, cookie := jobHarness(t)
	dir := t.TempDir()
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/jobs", `{"brief":"Fix the bug\nIn the parser.","dir":"`+dir+`","role":"reviewer"}`), cookie))
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	var created struct{ Data jobs.Job }
	_ = json.Unmarshal(response.Body.Bytes(), &created)
	if created.Data.ID != "j_1" || created.Data.Title != "Fix the bug" {
		t.Fatalf("created %+v", created.Data)
	}
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/jobs/j_1", ""), cookie))
	var detail struct{ Data jobs.Detail }
	_ = json.Unmarshal(response.Body.Bytes(), &detail)
	if detail.Data.State != jobs.StateRunning || len(detail.Data.Timeline) == 0 || detail.Data.Questions == nil {
		t.Fatalf("detail %d %+v", response.Code, detail.Data)
	}
	_ = f.SetStatus(detail.Data.PaneID, herdr.StatusIdle)
	if response = h.do(withCookie(request(http.MethodPost, "/api/v1/jobs/j_1/message", `{"text":"also the lexer"}`), cookie)); response.Code != http.StatusOK {
		t.Fatalf("message: %d %s", response.Code, response.Body)
	}
	if response = h.do(withCookie(request(http.MethodPost, "/api/v1/jobs/j_1/nudge", ""), cookie)); response.Code != http.StatusOK {
		t.Fatalf("nudge: %d %s", response.Code, response.Body)
	}
	if response = h.do(withCookie(request(http.MethodPost, "/api/v1/jobs/j_1/cancel", ""), cookie)); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cancelled"`) {
		t.Fatalf("cancel: %d %s", response.Code, response.Body)
	}
	if response = h.do(withCookie(request(http.MethodPost, "/api/v1/jobs/j_1/cancel", ""), cookie)); response.Code != http.StatusBadRequest {
		t.Fatalf("second cancel: %d", response.Code)
	}
	if response = h.do(withCookie(request(http.MethodGet, "/api/v1/jobs/j_9", ""), cookie)); response.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", response.Code)
	}
	if response = h.do(withCookie(request(http.MethodPost, "/api/v1/jobs", `{"brief":""}`), cookie)); response.Code != http.StatusBadRequest {
		t.Fatalf("empty brief: %d", response.Code)
	}
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/dirs", ""), cookie))
	if !strings.Contains(response.Body.String(), dir) {
		t.Fatalf("dirs: %s", response.Body)
	}
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/roles", ""), cookie))
	for _, role := range []string{"lead", "builder", "reviewer", "tester"} {
		if !strings.Contains(response.Body.String(), `"name":"`+role+`"`) {
			t.Fatalf("roles: %s", response.Body)
		}
	}
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/jobs?state=cancelled", ""), cookie))
	if !strings.Contains(response.Body.String(), `"total":1`) {
		t.Fatalf("list: %s", response.Body)
	}
}

func TestJobRoutesWithoutAServiceAnswer503(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/jobs", ""), cookie)); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service: %d", response.Code)
	}
}
