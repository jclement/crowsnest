// Tests for the day-thirty routes: sign in with Tailscale (and every way forged identity headers
// are refused), a read token opens the summary and nothing else, the allowlist is the password
// owner's, and clean-up previews unless told otherwise.

package httpd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/retention"
)

const serveStatusForTest = `{"Web":{"` + magicDNS + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:2769"}}}}}`

// tailnetRequest is a request as tailscale serve proxies it for login.
func tailnetRequest(method, target, body, login string) *http.Request {
	r := viaServe(request(method, target, body))
	r.Header.Set("Tailscale-User-Login", login)
	return r
}

func allow(t *testing.T, h *harness, logins ...string) {
	t.Helper()
	if _, err := h.server.deps.Tailscale.SetList(context.Background(), logins); err != nil {
		t.Fatal(err)
	}
}

func TestTailscaleSignsInAnAllowedLogin(t *testing.T) {
	h := newHarness(t, harnessOptions{hostNames: []string{magicDNS}, tailscale: true, serveStatus: serveStatusForTest})
	allow(t, h, "jeff@github")
	response := h.do(tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "Jeff@GitHub"))
	if response.Code != http.StatusOK {
		t.Fatalf("allowed login: %d %s", response.Code, response.Body)
	}
	cookie := sessionCookieFrom(t, response)
	// The session keeps working through serve for that login...
	if again := h.do(withCookie(tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "jeff@github"), cookie)); again.Code != http.StatusOK {
		t.Fatalf("the Tailscale session: %d", again.Code)
	}
	// ...but not when replayed without serve (another peer) or for another login...
	direct := withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)
	direct.Host = magicDNS
	if got := h.do(direct); got.Code != http.StatusUnauthorized {
		t.Errorf("the Tailscale session replayed without serve's header: %d", got.Code)
	}
	// ...and removing the login from the list ends it at once.
	allow(t, h)
	if got := h.do(withCookie(tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "jeff@github"), cookie)); got.Code != http.StatusUnauthorized {
		t.Errorf("a removed login's session: %d", got.Code)
	}
}

func TestTailscaleHeadersAreForgedEverywhereElse(t *testing.T) {
	cases := map[string]struct {
		opts   harnessOptions
		change func(*http.Request)
	}{
		"from a non-loopback peer": {
			opts:   harnessOptions{hostNames: []string{magicDNS}, tailscale: true, serveStatus: serveStatusForTest},
			change: func(r *http.Request) { r.RemoteAddr = "100.101.102.103:5000" },
		},
		"from loopback without --tailscale-auth": {
			opts:   harnessOptions{hostNames: []string{magicDNS}},
			change: func(*http.Request) {},
		},
		"from loopback with no serve config": {
			opts:   harnessOptions{hostNames: []string{magicDNS}, tailscale: true},
			change: func(*http.Request) {},
		},
		"from loopback to a Host serve does not proxy": {
			opts:   harnessOptions{tailscale: true, serveStatus: serveStatusForTest},
			change: func(r *http.Request) { r.Host = testHost },
		},
		"for a login not on the list": {
			opts:   harnessOptions{hostNames: []string{magicDNS}, tailscale: true, serveStatus: serveStatusForTest},
			change: func(r *http.Request) { r.Header.Set("Tailscale-User-Login", "stranger@github") },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, tc.opts)
			if h.server.deps.Tailscale != nil {
				allow(t, h, "jeff@github")
			}
			r := tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "jeff@github")
			tc.change(r)
			if response := h.do(r); response.Code != http.StatusUnauthorized {
				t.Fatalf("%d %s, want 401", response.Code, response.Body)
			}
		})
	}
}

func TestTailscaleAllowlistIsThePasswordOwners(t *testing.T) {
	h := newHarness(t, harnessOptions{hostNames: []string{magicDNS}, tailscale: true, serveStatus: serveStatusForTest})
	login := h.do(tailnetRequest(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword), "jeff@github"))
	cookie := sessionCookieFrom(t, login)
	state := h.do(withCookie(tailnetRequest(http.MethodGet, "/api/v1/auth/tailscale", "", "jeff@github"), cookie))
	if !strings.Contains(state.Body.String(), `"current_login":"jeff@github"`) || !strings.Contains(state.Body.String(), `"serve_configured":true`) {
		t.Fatalf("state %s", state.Body)
	}
	added := h.do(withCookie(tailnetRequest(http.MethodPost, "/api/v1/auth/tailscale/allow-me", "{}", "jeff@github"), cookie))
	if added.Code != http.StatusOK || !strings.Contains(added.Body.String(), `"allowed":["jeff@github"]`) {
		t.Fatalf("allow-me: %d %s", added.Code, added.Body)
	}
	// A session signed in with Tailscale may not widen the list.
	tailnet := sessionCookieFrom(t, h.do(tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "jeff@github")))
	widen := h.do(withCookie(tailnetRequest(http.MethodPut, "/api/v1/auth/tailscale", `{"logins":["jeff@github","friend@github"]}`, "jeff@github"), tailnet))
	if widen.Code != http.StatusForbidden {
		t.Fatalf("a Tailscale session changed the list: %d %s", widen.Code, widen.Body)
	}
}

func TestReadTokenOpensOnlyTheSummary(t *testing.T) {
	h := newHarness(t, harnessOptions{machines: true, ops: fakeOps{}})
	cookie := h.login()
	minted := h.do(withCookie(request(http.MethodPost, "/api/v1/tokens", `{"name":"bedev"}`), cookie))
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", minted.Code, minted.Body)
	}
	var body struct {
		Data mintResponse `json:"data"`
	}
	_ = json.Unmarshal(minted.Body.Bytes(), &body)
	bearer := func(path string) int {
		r := request(http.MethodGet, path, "")
		r.Header.Set("Authorization", "Bearer "+body.Data.Token)
		return h.do(r).Code
	}
	if code := bearer("/api/v1/summary"); code != http.StatusOK {
		t.Errorf("summary with the token: %d", code)
	}
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/tokens", "/api/v1/backup", "/api/v1/machines", "/api/v1/settings"} {
		if code := bearer(path); code != http.StatusUnauthorized {
			t.Errorf("%s with a read token: %d, want 401", path, code)
		}
	}
	if list := h.do(withCookie(request(http.MethodGet, "/api/v1/tokens", ""), cookie)); strings.Contains(list.Body.String(), body.Data.Token) {
		t.Error("the token list shows the token")
	}
	revoke := h.do(withCookie(request(http.MethodDelete, "/api/v1/tokens/1", ""), cookie))
	if revoke.Code != http.StatusOK || bearer("/api/v1/summary") != http.StatusUnauthorized {
		t.Errorf("after revoking: %d, summary %d", revoke.Code, bearer("/api/v1/summary"))
	}
}

// fakeOps answers the ops routes with fixed data.
type fakeOps struct{}

func (fakeOps) BackupName() string { return "crowsnest-test.tar.gz" }
func (fakeOps) WriteBackup(_ context.Context, w io.Writer) error {
	_, err := w.Write([]byte("backup"))
	return err
}
func (fakeOps) Updated(context.Context) *UpdateNotice { return &UpdateNotice{From: "v1", To: "v2"} }
func (fakeOps) DismissUpdated(context.Context) error  { return nil }
func (fakeOps) Storage(context.Context) (retention.Storage, error) {
	return retention.Storage{Worktrees: "landing"}, nil
}
func (f fakeOps) Cleanup(_ context.Context, dryRun bool, confirm []string) (retention.Report, error) {
	return retention.Report{DryRun: dryRun, Confirmed: len(confirm)}, nil
}
func (fakeOps) Summary(context.Context) (Summary, error) {
	return Summary{Name: "quark", Attention: 3}, nil
}
func (fakeOps) ArchivedJobs(context.Context) (map[int64]bool, error)      { return map[int64]bool{}, nil }
func (fakeOps) SearchJobs(context.Context, string, bool) ([]int64, error) { return nil, nil }

func TestCleanupPreviewsUnlessToldOtherwise(t *testing.T) {
	h := newHarness(t, harnessOptions{ops: fakeOps{}})
	cookie := h.login()
	for body, wantDry := range map[string]bool{`{}`: true, `{"dry_run":true}`: true, `{"dry_run":false,"confirm":["j_3"]}`: false} {
		response := h.do(withCookie(request(http.MethodPost, "/api/v1/storage/cleanup", body), cookie))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"dry_run":true`) != wantDry {
			t.Errorf("%s: %d %s", body, response.Code, response.Body)
		}
	}
	if bad := h.do(withCookie(request(http.MethodPost, "/api/v1/storage/cleanup", `{"dry_run":false,"confirm":["; drop"]}`), cookie)); bad.Code != http.StatusBadRequest {
		t.Errorf("a bad confirm id: %d", bad.Code)
	}
	download := h.do(withCookie(request(http.MethodGet, "/api/v1/backup", ""), cookie))
	if download.Code != http.StatusOK || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("backup download: %d %v", download.Code, download.Header())
	}
	if anonymous := h.do(request(http.MethodGet, "/api/v1/backup", "")); anonymous.Code != http.StatusUnauthorized {
		t.Errorf("a backup without signing in: %d", anonymous.Code)
	}
}
