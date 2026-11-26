package httpd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/opener"
	"github.com/jclement/crowsnest/internal/setup"
	"github.com/jclement/crowsnest/internal/store"
)

func init() {
	// Stored owners hash with argon2id; the production cost would make this suite slow.
	auth.SetArgon2Params(auth.Argon2Params{Time: 1, Memory: 64, Threads: 1})
}

const ownerPassword = "a long owner password"

// setupHarness is a locked first-run server over a database with no owner, and a way to build
// the normal server over the same database once setup is done.
type setupHarness struct {
	*harness
	db      *store.Store
	setup   *setup.Service
	mu      sync.Mutex
	tokens   []string
	authSvc  *auth.Service
	handoffs *opener.Handoffs
}

func newSetupHarness(t *testing.T) *setupHarness {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/setup.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &setupHarness{db: db}
	h.authSvc, err = auth.New(ctx, auth.Options{}, db)
	if err != nil {
		t.Fatal(err)
	}
	h.setup, err = setup.New(setup.Options{Auth: h.authSvc, DefaultName: "quark", Announce: func(token string, _ bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.tokens = append(h.tokens, token)
	}})
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	cfg := testConfig(false)
	cfg.Username, cfg.Password = "", ""
	h.handoffs = opener.NewHandoffs(func() string { return "/setup#token=" + h.token() }, nil)
	server, err := New(Deps{Config: cfg, Auth: h.authSvc, Hub: hub.New(), Logger: slog.New(slog.NewTextHandler(logs, nil)),
		Version: "v9.9.9-test", Web: testWeb, Setup: h.setup, Handoffs: h.handoffs})
	if err != nil {
		t.Fatal(err)
	}
	h.harness = &harness{t: t, server: server, logs: logs, clock: &clock{}}
	return h
}

func (h *setupHarness) token() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tokens[len(h.tokens)-1]
}

// fullServer is the normal server serve builds after setup, on the same database.
func (h *setupHarness) fullServer(nameExplicit bool) *harness {
	h.t.Helper()
	service, err := auth.New(context.Background(), auth.Options{}, h.db)
	if err != nil {
		h.t.Fatal(err)
	}
	logs := &syncBuffer{}
	cfg := testConfig(false)
	cfg.Username, cfg.Password = "", ""
	cfg.Name, cfg.NameExplicit = "quark", nameExplicit
	server, err := New(Deps{Config: cfg, Auth: service, Hub: hub.New(), Logger: slog.New(slog.NewTextHandler(logs, nil)),
		Version: "v9.9.9-test", Web: testWeb, Names: h.db})
	if err != nil {
		h.t.Fatal(err)
	}
	return &harness{t: h.t, server: server, logs: logs, clock: &clock{}}
}

func setupBody(token, username, password, name string) string {
	body, _ := json.Marshal(map[string]string{"token": token, "username": username, "password": password, "name": name})
	return string(body)
}

func TestSetupModeLocksEverythingElse(t *testing.T) {
	h := newSetupHarness(t)

	var health struct {
		Data HealthResponse `json:"data"`
	}
	response := h.do(request(http.MethodGet, "/api/v1/health", ""))
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil || response.Code != 200 || !health.Data.SetupRequired {
		t.Fatalf("health: %d %s", response.Code, response.Body)
	}

	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/auth/me", ""),
		request(http.MethodPost, "/api/v1/auth/login", loginBody("jeff", "x")),
		request(http.MethodGet, "/api/v1/fleet", ""),
		request(http.MethodGet, "/api/v1/events", ""),
		request(http.MethodGet, "/api/v1/jobs", ""),
		request(http.MethodPost, "/api/v1/jobs", `{"brief":"x"}`),
		request(http.MethodGet, "/api/v1/account", ""),
		request(http.MethodPost, "/api/v1/admin/update", ""),
	} {
		response := h.do(r)
		if response.Code != http.StatusForbidden || decodeError(t, response).Code != codeSetupRequired {
			t.Errorf("%s %s: %d %s", r.Method, r.URL.Path, response.Code, response.Body)
		}
	}
	// Terminal sockets: refused before any upgrade.
	for _, path := range []string{"/api/v1/term/tui", "/api/v1/term/pane/w1:p1"} {
		r := request(http.MethodGet, path, "")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Sec-WebSocket-Version", "13")
		r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		r.Header.Set("Origin", testOrigin)
		response := h.do(r)
		if response.Code != http.StatusForbidden || decodeError(t, response).Code != codeSetupRequired {
			t.Errorf("websocket %s: %d %s", path, response.Code, response.Body)
		}
	}

	// Pages go to /setup; /setup and the files it needs are served.
	for _, path := range []string{"/", "/chat", "/login", "/terminal"} {
		response := h.do(request(http.MethodGet, path, ""))
		if response.Code != http.StatusFound || response.Header().Get("Location") != "/setup" {
			t.Errorf("%s: %d %q", path, response.Code, response.Header().Get("Location"))
		}
	}
	for _, path := range []string{"/setup", "/assets/index-abc123.js", "/theme-init.js", "/icons/crowsnest.svg", "/manifest.webmanifest"} {
		if response := h.do(request(http.MethodGet, path, "")); response.Code != http.StatusOK {
			t.Errorf("%s: %d", path, response.Code)
		}
	}

	var info struct {
		Data SetupInfo `json:"data"`
	}
	response = h.do(request(http.MethodGet, "/api/v1/setup", ""))
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil || !info.Data.Required || info.Data.DefaultName != "quark" || info.Data.MinPasswordLength != auth.MinPasswordLength {
		t.Fatalf("setup info: %s", response.Body)
	}
}

func TestSetupObeysOriginAndCSRFHeader(t *testing.T) {
	h := newSetupHarness(t)
	body := setupBody(h.token(), "jeff", ownerPassword, "")
	noOrigin := request(http.MethodPost, "/api/v1/setup", body)
	noOrigin.Header.Del("Origin")
	foreign := request(http.MethodPost, "/api/v1/setup", body)
	foreign.Header.Set("Origin", "http://evil.example")
	noHeader := request(http.MethodPost, "/api/v1/setup", body)
	noHeader.Header.Del("X-Crowsnest")
	badHost := request(http.MethodPost, "/api/v1/setup", body)
	badHost.Host, badHost.Header["Origin"] = "evil.example", []string{"http://evil.example"}
	for name, r := range map[string]*http.Request{"no origin": noOrigin, "foreign origin": foreign, "no X-Crowsnest": noHeader, "foreign host": badHost} {
		if response := h.do(r); response.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", name, response.Code, response.Body)
		}
	}
	if !h.setup.Required() {
		t.Fatal("a refused request completed setup")
	}
}

func TestSetupHappyPathThenGone(t *testing.T) {
	h := newSetupHarness(t)
	token := h.token()

	wrong := h.do(request(http.MethodPost, "/api/v1/setup", setupBody("nope", "jeff", ownerPassword, "")))
	if wrong.Code != http.StatusForbidden || decodeError(t, wrong).Code != codeBadToken {
		t.Fatalf("wrong token: %d %s", wrong.Code, wrong.Body)
	}
	short := h.do(request(http.MethodPost, "/api/v1/setup", setupBody(token, "jeff", "short", "")))
	if short.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d %s", short.Code, short.Body)
	}

	response := h.do(request(http.MethodPost, "/api/v1/setup", setupBody(token, "jeff", ownerPassword, "devbox")))
	if response.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", response.Code, response.Body)
	}
	cookie := sessionCookieFrom(t, response)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", cookie)
	}
	if strings.Contains(response.Body.String(), token) || strings.Contains(h.logs.String(), token) {
		t.Fatal("the token appears in a response or the request log")
	}

	// Between completion and the full server: other routes say "starting", setup says gone.
	if r := h.do(request(http.MethodGet, "/api/v1/fleet", "")); r.Code != http.StatusServiceUnavailable || decodeError(t, r).Code != codeStarting {
		t.Fatalf("after setup, before the full server: %d %s", r.Code, r.Body)
	}
	again := h.do(request(http.MethodPost, "/api/v1/setup", setupBody(token, "mallory", ownerPassword, "")))
	if again.Code != http.StatusGone {
		t.Fatalf("second setup: %d %s", again.Code, again.Body)
	}

	// The full server: the setup session is signed in, setup is gone for good.
	full := h.fullServer(false)
	me := full.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie))
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"jeff"`) || !strings.Contains(me.Body.String(), `"name":"quark"`) {
		t.Fatalf("me after setup: %d %s", me.Code, me.Body)
	}
	if r := full.do(request(http.MethodPost, "/api/v1/setup", setupBody(token, "mallory", ownerPassword, ""))); r.Code != http.StatusGone {
		t.Fatalf("setup on the full server: %d", r.Code)
	}
	if r := full.do(request(http.MethodGet, "/api/v1/setup", "")); !strings.Contains(r.Body.String(), `"required":false`) {
		t.Fatalf("setup info on the full server: %s", r.Body)
	}
	login := full.do(request(http.MethodPost, "/api/v1/auth/login", loginBody("jeff", ownerPassword)))
	if login.Code != http.StatusOK {
		t.Fatalf("login with the chosen password: %d %s", login.Code, login.Body)
	}
}

// The setup harness's server, signed in through setup, as a starting point for the account tests.
func setUpAndSignIn(t *testing.T) (*setupHarness, *harness, *http.Cookie) {
	t.Helper()
	h := newSetupHarness(t)
	response := h.do(request(http.MethodPost, "/api/v1/setup", setupBody(h.token(), "jeff", ownerPassword, "")))
	if response.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", response.Code, response.Body)
	}
	full := h.fullServer(false)
	return h, full, sessionCookieFrom(t, response)
}

func TestAccountAndPasswordChange(t *testing.T) {
	_, full, mine := setUpAndSignIn(t)
	other := full.login2("jeff", ownerPassword)

	account := full.do(withCookie(request(http.MethodGet, "/api/v1/account", ""), mine))
	if account.Code != 200 || !strings.Contains(account.Body.String(), `"source":"database"`) || !strings.Contains(account.Body.String(), `"name_managed":false`) {
		t.Fatalf("account: %s", account.Body)
	}

	change := func(current, next string) *http.Request {
		body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		return withCookie(request(http.MethodPost, "/api/v1/account/password", string(body)), mine)
	}
	if r := full.do(change("wrong password!", "a brand new password")); r.Code != http.StatusForbidden || decodeError(t, r).Code != codeWrongPassword {
		t.Fatalf("wrong current: %d %s", r.Code, r.Body)
	}
	if r := full.do(change(ownerPassword, "short")); r.Code != http.StatusBadRequest {
		t.Fatalf("short new: %d %s", r.Code, r.Body)
	}
	response := full.do(change(ownerPassword, "a brand new password"))
	if response.Code != http.StatusOK {
		t.Fatalf("change: %d %s", response.Code, response.Body)
	}
	renewed := sessionCookieFrom(t, response)

	if r := full.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), other)); r.Code != http.StatusUnauthorized {
		t.Fatalf("the other session after a password change: %d", r.Code)
	}
	if r := full.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), mine)); r.Code != http.StatusUnauthorized {
		t.Fatalf("this browser's old cookie after a password change: %d", r.Code)
	}
	if r := full.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), renewed)); r.Code != http.StatusOK {
		t.Fatalf("this browser's new cookie: %d", r.Code)
	}
}

func TestPasswordChangeRefusedForConfigLogin(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	if r := h.do(withCookie(request(http.MethodGet, "/api/v1/account", ""), cookie)); !strings.Contains(r.Body.String(), `"source":"config"`) {
		t.Fatalf("account: %s", r.Body)
	}
	body := `{"current_password":"` + testPassword + `","new_password":"a brand new password"}`
	if r := h.do(withCookie(request(http.MethodPost, "/api/v1/account/password", body), cookie)); r.Code != http.StatusConflict {
		t.Fatalf("change: %d %s", r.Code, r.Body)
	}
}

func TestSetName(t *testing.T) {
	h, full, cookie := setUpAndSignIn(t)
	if r := full.do(withCookie(request(http.MethodPut, "/api/v1/account/name", `{"name":"no spaces allowed"}`), cookie)); r.Code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", r.Code)
	}
	if r := full.do(withCookie(request(http.MethodPut, "/api/v1/account/name", `{"name":"devbox"}`), cookie)); r.Code != http.StatusOK {
		t.Fatalf("set name: %d %s", r.Code, r.Body)
	}
	if r := full.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)); !strings.Contains(r.Body.String(), `"name":"devbox"`) {
		t.Fatalf("me after a rename: %s", r.Body)
	}
	if name, _ := h.db.GetKV(context.Background(), store.InstanceNameKey); name != "devbox" {
		t.Fatalf("stored name %q", name)
	}

	managed := h.fullServer(true)
	if r := managed.do(withCookie(request(http.MethodPut, "/api/v1/account/name", `{"name":"other"}`), cookie)); r.Code != http.StatusConflict {
		t.Fatalf("rename under --name: %d", r.Code)
	}
}

// login2 signs in with explicit credentials.
func (h *harness) login2(username, password string) *http.Cookie {
	h.t.Helper()
	response := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(username, password)))
	if response.Code != http.StatusOK {
		h.t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	return sessionCookieFrom(h.t, response)
}

// The browser opener's handoff (DESIGN.md 3.3): a one-time id that, from loopback only,
// redirects relatively to the setup page with the token in the fragment.
func TestSetupHandoff(t *testing.T) {
	h := newSetupHarness(t)
	id := h.handoffs.New()
	remote := request(http.MethodGet, "/setup/open/"+id, "")
	remote.RemoteAddr = "192.0.2.7:5000"
	if location := h.do(remote).Header().Get("Location"); location != "/setup" {
		t.Fatalf("a remote peer got %q", location)
	}
	// The remote attempt spent nothing it could use, but the id is gone (one use, whoever tried).
	fresh := h.handoffs.New()
	response := h.do(request(http.MethodGet, "/setup/open/"+fresh, ""))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/setup#token="+h.token() || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("loopback redemption: %d %q", response.Code, response.Header().Get("Location"))
	}
	if again := h.do(request(http.MethodGet, "/setup/open/"+fresh, "")); again.Header().Get("Location") != "/setup" {
		t.Fatalf("a second use: %q", again.Header().Get("Location"))
	}
	if strings.Contains(h.logs.String(), h.token()) {
		t.Fatal("the token reached the request log")
	}
}
