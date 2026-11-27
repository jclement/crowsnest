// Tests for every security rule in DESIGN.md section 4 that M1 implements, positive and
// negative, plus the API envelope, the SPA's routing and cache headers, and the SSE stream.

package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jclement/crowsnest/internal/attention"
	"github.com/jclement/crowsnest/internal/machines"
	"github.com/jclement/crowsnest/internal/tsauth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/browse"
	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/schedule"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/term"
	"github.com/jclement/crowsnest/internal/transcript"
	"github.com/jclement/crowsnest/internal/voice"
)

const (
	testUser     = "jeff"
	testPassword = "correct horse battery staple"
	testHost     = "localhost:2769"
	testOrigin   = "http://localhost:2769"
)

// ---- Harness -----------------------------------------------------------------

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t      *testing.T
	server *Server
	hub    *hub.Hub
	clock  *clock
	logs   *syncBuffer
}

type harnessOptions struct {
	updater   Updater // nil: no self-update (update_available false, /admin/update 409)
	noAuth    bool
	heartbeat time.Duration
	herdr     herdr.Client // nil: the herdr routes answer 503
	fleet     *fleet.Cache
	// M4: conversations and orchestrator (conversation_test.go)
	conversations *transcript.Manager
	orchestrator  *orch.Orchestrator
	terminals     term.Launcher // nil: the terminal routes answer 503
	termQueue     int
	maxTerminals  int
	// M6: the question inbox (questions_test.go); nil: the routes answer 503
	questions *questions.Service
	// M7: jobs (jobs_test.go); nil: the routes answer 503
	jobs *jobs.Service
	// M11: talk mode (voice_test.go); nil: the /voice routes answer 404
	voice *voice.Service
	// M8: the journal and the poker (journal_test.go)
	journal *journal.Journal
	poker   *orch.Poker
	// Web Push (push_test.go); nil: the /push routes answer 503
	push *push.Service
	// Scheduled tasks and upkeep (schedule_test.go); nil: those routes answer 503
	schedule *schedule.Service
	tender   *orch.Tender
	// Spend (spend_test.go); nil: the /spend routes answer 503
	spend *spend.Meter
	// Attachments (attachments_test.go); nil: the routes answer 503
	attachments  *attach.Service
	attachLayout attach.Layout
	// Approvals and notification actions (approvals_test.go); nil: those routes answer 503
	approvals *approvals.Service
	actions   ActionRedeemer
	// R2-L2: --trusted-proxy
	trustedProxies     []netip.Prefix
	trustedProxyHeader string // default x-forwarded-for
	// Instruction layers (instructions_test.go); nil: the routes answer 503
	instructions *instructions.Service
	// Projects and their leads (projects_test.go); nil: the routes answer 503
	projects *projects.Service
	leads    *projects.Leads
	// Extra allowed Host names (the tailnet name detected at start).
	hostNames []string
	// The System page, Settings and the first run (system_test.go); nil: 503
	system System
	// The attention queue and the Now view (attention_test.go); nil: 503
	attention *attention.Service
	now       NowSources
	// Day thirty (ops_test.go): sign in with Tailscale (serveStatus is what `tailscale serve
	// status --json` answers; "" fails), scoped tokens and machines, and the ops service.
	tailscale   bool
	serveStatus string
	machines    bool
	ops         Ops
	// The live diff (changes_test.go); nil: 503
	changes       *changes.Service
	requireViewed func() bool
	// The folder picker (browse_test.go); nil: the /browse routes answer 503
	browse *browse.Service
}

var testWeb = fstest.MapFS{
	"index.html":                {Data: []byte("<!doctype html><title>crowsnest</title>")},
	"theme-init.js":             {Data: []byte("/* theme */")},
	"assets/index-abc123.js":    {Data: []byte("console.log('app')")},
	"assets/index-abc123.css":   {Data: []byte("body{}")},
	"icons/crowsnest.svg":       {Data: []byte("<svg/>")},
	"manifest.webmanifest":      {Data: []byte("{}")},
	"sw.js":                     {Data: []byte("/* service worker */")},
	"assets/nested/chunk-1.js":  {Data: []byte("1")},
	"assets/nested/chunk-2.css": {Data: []byte("2")},
}

func testConfig(noAuth bool) config.Config {
	cfg := config.Config{
		Session:   "crowsnest-test",
		HostNames: []string{"localhost", "127.0.0.1", "::1", "crow.example"},
		NoAuth:    noAuth,
	}
	if !noAuth {
		cfg.Username, cfg.Password = testUser, testPassword
	}
	return cfg
}

func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := &clock{now: time.Now()}
	cfg := testConfig(opts.noAuth)
	cfg.HostNames = append(cfg.HostNames, opts.hostNames...)
	cfg.TrustedProxies = opts.trustedProxies
	cfg.TrustedProxyHeader = opts.trustedProxyHeader
	if cfg.TrustedProxyHeader == "" {
		cfg.TrustedProxyHeader = config.HeaderXForwardedFor
	}
	service, err := auth.New(ctx, auth.Options{Username: cfg.Username, Password: cfg.Password, NoAuth: cfg.NoAuth, Now: c.Now}, db)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	events := hub.New()
	var gate *tsauth.Gate
	if opts.tailscale {
		gate = tsauth.New(tsauth.Options{Enabled: true, KV: db, Port: 2769, Run: func(context.Context, ...string) ([]byte, error) {
			if opts.serveStatus == "" {
				return nil, errors.New("no serve config")
			}
			return []byte(opts.serveStatus), nil
		}})
	}
	var machineService *machines.Service
	if opts.machines {
		machineService = &machines.Service{Store: db}
	}
	server, err := New(Deps{
		Tailscale: gate, Machines: machineService, Ops: opts.ops, Browse: opts.browse,
		Config:         cfg,
		Auth:           service,
		Hub:            events,
		Logger:         slog.New(slog.NewTextHandler(logs, nil)),
		Version:        "v9.9.9-test",
		Web:            testWeb,
		Heartbeat:      opts.heartbeat,
		Herdr:          opts.herdr,
		Fleet:          opts.fleet,
		HerdrVersion:   "0.9.5-test",
		Terminals:      opts.terminals,
		TermQueueBytes: opts.termQueue,
		MaxTerminals:   opts.maxTerminals,

		Conversations: opts.conversations,
		Orchestrator:  opts.orchestrator,
		Questions:     opts.questions,
		Jobs:          opts.jobs,
		Projects:      opts.projects,
		Leads:         opts.leads,
		Spend:         opts.spend,
		Voice:         opts.voice,
		Journal:       opts.journal,
		Poker:         opts.poker,
		Push:          opts.push,
		Updater:       opts.updater,
		Schedule:      opts.schedule,
		Tender:        opts.tender,
		Attachments:   opts.attachments,
		AttachLayout:  opts.attachLayout,
		Instructions:  opts.instructions,
		Approvals:     opts.approvals,
		Actions:       opts.actions,
		System:        opts.system,
		Attention:     opts.attention,
		Now:           opts.now,
		Changes:       opts.changes,
		RequireViewed: opts.requireViewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, server: server, hub: events, clock: c, logs: logs}
}

// request builds a same-origin request as the web app would send it.
func request(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = testHost
	r.RemoteAddr = "127.0.0.1:50000"
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("X-Crowsnest", "1")
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func (h *harness) do(r *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(recorder, r)
	return recorder
}

func loginBody(username, password string) string {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(body)
}

// login signs in and returns the session cookie.
func (h *harness) login() *http.Cookie {
	h.t.Helper()
	response := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword)))
	if response.Code != http.StatusOK {
		h.t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	return sessionCookieFrom(h.t, response)
}

// loginAt signs in through the Host a live test server is reached at (base is its URL): a
// session is valid only at the Host:port it was issued for (R1 M2).
func (h *harness) loginAt(base string) *http.Cookie {
	h.t.Helper()
	host := strings.TrimPrefix(base, "http://")
	r := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))
	r.Host = host
	r.Header.Set("Origin", "http://"+host)
	response := h.do(r)
	if response.Code != http.StatusOK {
		h.t.Fatalf("login at %s: %d %s", host, response.Code, response.Body)
	}
	return sessionCookieFrom(h.t, response)
}

// sessionCookieFrom returns the session cookie a response set, under either name (plain over
// HTTP, __Host- over HTTPS).
func sessionCookieFrom(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookie || cookie.Name == SecureSessionCookie {
			return cookie
		}
	}
	t.Fatalf("no session cookie in %v", response.Header())
	return nil
}

func withCookie(r *http.Request, cookie *http.Cookie) *http.Request {
	r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	return r
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("not an error envelope: %s", response.Body)
	}
	return envelope.Error
}

// ---- Health and envelope -----------------------------------------------------

func TestHealth(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	response := h.do(request(http.MethodGet, "/api/v1/health", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d", response.Code)
	}
	var envelope struct{ Data HealthResponse }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data != (HealthResponse{Status: "ok", Version: "v9.9.9-test"}) {
		t.Fatalf("health %+v", envelope.Data)
	}
	if !strings.Contains(response.Body.String(), `"update_available":false`) {
		t.Fatalf("update_available missing: %s", response.Body)
	}
}

func TestEnvelopeHelpers(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeList(recorder, []string{"a", "b"}, ListMeta{Total: 2})
	if got := strings.TrimSpace(recorder.Body.String()); got != `{"data":["a","b"],"meta":{"total":2}}` {
		t.Fatalf("list envelope %s", got)
	}
	recorder = httptest.NewRecorder()
	writeData(recorder, http.StatusCreated, map[string]int{"n": 1})
	if recorder.Code != http.StatusCreated || strings.TrimSpace(recorder.Body.String()) != `{"data":{"n":1}}` {
		t.Fatalf("data envelope %d %s", recorder.Code, recorder.Body)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("API responses must not be cached")
	}
	recorder = httptest.NewRecorder()
	writeError(recorder, http.StatusNotFound, codeNotFound, "gone")
	if strings.TrimSpace(recorder.Body.String()) != `{"error":{"message":"gone","code":"NOT_FOUND"}}` {
		t.Fatalf("error envelope %s", recorder.Body)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	response := h.do(request(http.MethodGet, "/api/v1/nope", ""))
	if response.Code != http.StatusNotFound || decodeError(t, response).Code != codeNotFound {
		t.Fatalf("got %d %s", response.Code, response.Body)
	}
}

// ---- Host check --------------------------------------------------------------

func TestHostCheck(t *testing.T) {
	tests := []struct {
		host string
		ok   bool
	}{
		{"localhost:2769", true},
		{"LOCALHOST:2769", true},
		{"localhost.:2769", true},
		{"127.0.0.1:2769", true},
		{"127.0.0.2", true}, // any loopback literal
		{"[::1]:2769", true},
		{"crow.example", true}, // configured name
		{"evil.example:2769", false},
		{"attacker.localhost.evil.com", false},
		{"192.168.1.20:2769", false}, // a LAN IP that is not bound
		{"", false},
	}
	for _, noAuth := range []bool{false, true} { // the host check applies with --no-auth too
		h := newHarness(t, harnessOptions{noAuth: noAuth})
		for _, tc := range tests {
			r := request(http.MethodGet, "/api/v1/health", "")
			r.Host = tc.host
			response := h.do(r)
			if got := response.Code == http.StatusOK; got != tc.ok {
				t.Errorf("noAuth=%v Host %q: status %d, want ok=%v", noAuth, tc.host, response.Code, tc.ok)
			}
			if !tc.ok && decodeError(t, response).Code != codeForbidden {
				t.Errorf("Host %q: wrong error body %s", tc.host, response.Body)
			}
		}
	}
}

// ---- Origin and X-Crowsnest --------------------------------------------------

func TestOriginCheckOnStateChangingRequests(t *testing.T) {
	tests := []struct {
		name    string
		origin  string // "-" means no header
		csrf    string // "-" means no header
		want    int
		message string
	}{
		{name: "same origin with header", origin: testOrigin, csrf: "1", want: http.StatusOK},
		{name: "missing Origin", origin: "-", csrf: "1", want: http.StatusForbidden, message: "Missing Origin"},
		{name: "null Origin", origin: "null", csrf: "1", want: http.StatusForbidden, message: "Missing Origin"},
		{name: "cross-origin", origin: "http://evil.example", csrf: "1", want: http.StatusForbidden, message: "not an allowed host"},
		{name: "allowed host but not this Host", origin: "http://crow.example", csrf: "1", want: http.StatusForbidden, message: "does not match Host"},
		{name: "same host different port", origin: "http://localhost:5173", csrf: "1", want: http.StatusForbidden, message: "does not match Host"},
		{name: "scheme default port mismatch", origin: "https://localhost:2769", csrf: "1", want: http.StatusOK},
		{name: "malformed Origin", origin: "javascript:alert(1)", csrf: "1", want: http.StatusForbidden, message: "Malformed"},
		{name: "missing X-Crowsnest", origin: testOrigin, csrf: "-", want: http.StatusForbidden, message: "X-Crowsnest"},
		{name: "wrong X-Crowsnest", origin: testOrigin, csrf: "true", want: http.StatusForbidden, message: "X-Crowsnest"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{noAuth: true})
			r := request(http.MethodPost, "/api/v1/auth/logout", "")
			setOrDelete(r.Header, "Origin", tc.origin)
			setOrDelete(r.Header, "X-Crowsnest", tc.csrf)
			response := h.do(r)
			if response.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.want, response.Body)
			}
			if tc.message != "" && !strings.Contains(decodeError(t, response).Message, tc.message) {
				t.Fatalf("message %q does not mention %q", decodeError(t, response).Message, tc.message)
			}
		})
	}
}

func TestOriginCheckOnWebSocketUpgrade(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	r := request(http.MethodGet, "/api/v1/health", "")
	r.Header.Set("Upgrade", "websocket")
	if response := h.do(r); response.Code != http.StatusForbidden {
		t.Fatalf("upgrade without Origin: %d", response.Code)
	}
	r.Header.Set("Origin", "http://evil.example")
	if response := h.do(r); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin upgrade: %d", response.Code)
	}
	r.Header.Set("Origin", testOrigin) // GET upgrades need Origin but not X-Crowsnest
	if response := h.do(r); response.Code != http.StatusOK {
		t.Fatalf("same-origin upgrade: %d %s", response.Code, response.Body)
	}
}

func TestPlainGetNeedsNoOrigin(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	if response := h.do(request(http.MethodGet, "/api/v1/auth/me", "")); response.Code != http.StatusOK {
		t.Fatalf("GET without Origin: %d", response.Code)
	}
}

func setOrDelete(header http.Header, key, value string) {
	if value == "-" {
		header.Del(key)
		return
	}
	header.Set(key, value)
}

// ---- Login, sessions, logout -------------------------------------------------

func TestLoginSetsAHardenedCookie(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	response := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword)))
	if response.Code != http.StatusOK {
		t.Fatalf("login %d %s", response.Code, response.Body)
	}
	cookie := sessionCookieFrom(t, response)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Secure {
		t.Fatalf("cookie attributes %+v", cookie)
	}
	if cookie.MaxAge < int((auth.SessionTTL - time.Minute).Seconds()) {
		t.Fatalf("cookie lifetime %d is not ~30 days", cookie.MaxAge)
	}
	var envelope struct{ Data MeResponse }
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	if envelope.Data.Username != testUser || !envelope.Data.Features.Auth {
		t.Fatalf("login body %s", response.Body)
	}
}

func TestCookieSecureFlag(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		forwarded  string
		wantSecure bool
	}{
		{name: "plain http", remote: "127.0.0.1:1", wantSecure: false},
		{name: "https via a local proxy", remote: "127.0.0.1:1", forwarded: "https", wantSecure: true},
		{name: "forwarded header from a remote client is ignored", remote: "192.0.2.7:1", forwarded: "https", wantSecure: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			r := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))
			r.RemoteAddr = tc.remote
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			cookie := sessionCookieFrom(t, h.do(r))
			if cookie.Secure != tc.wantSecure {
				t.Fatalf("Secure=%v, want %v", cookie.Secure, tc.wantSecure)
			}
			// R1 M2: over HTTPS the cookie is __Host- prefixed (Secure, Path=/, no Domain).
			if wantName := map[bool]string{true: SecureSessionCookie, false: SessionCookie}[tc.wantSecure]; cookie.Name != wantName || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatalf("cookie %s path %q domain %q, want %s on / with no domain", cookie.Name, cookie.Path, cookie.Domain, wantName)
			}
		})
	}
	h := newHarness(t, harnessOptions{})
	r := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))
	r.TLS = &tlsStateForTest
	if cookie := sessionCookieFrom(t, h.do(r)); !cookie.Secure || cookie.Name != SecureSessionCookie {
		t.Fatalf("cookie over direct TLS should be Secure and __Host- prefixed: %+v", cookie)
	}
}

// TestSessionCookieIsHostAndSchemeBound is R1 M2 at the HTTP layer: a session issued at one
// Host:port is refused at another (a sibling port on the same name receives the cookie too),
// and over HTTPS only the __Host- cookie is read.
func TestSessionCookieIsHostAndSchemeBound(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login() // issued at localhost:2769
	me := withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)
	if response := h.do(me); response.Code != http.StatusOK {
		t.Fatalf("at the issuing host: %d", response.Code)
	}
	sibling := withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)
	sibling.Host = "localhost:5173"
	if response := h.do(sibling); response.Code != http.StatusUnauthorized {
		t.Fatalf("a cookie presented at a sibling port was accepted: %d", response.Code)
	}
	if response := h.do(me); response.Code != http.StatusOK {
		t.Fatalf("presenting the cookie elsewhere ended the session: %d", response.Code)
	}

	// Over HTTPS, a plain-named cookie (which anything on the host could have set) is ignored.
	secure := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))
	secure.TLS = &tlsStateForTest
	issued := sessionCookieFrom(t, h.do(secure))
	plain := request(http.MethodGet, "/api/v1/auth/me", "")
	plain.TLS = &tlsStateForTest
	plain.AddCookie(&http.Cookie{Name: SessionCookie, Value: issued.Value})
	if response := h.do(plain); response.Code != http.StatusUnauthorized {
		t.Fatalf("over HTTPS a plain-named cookie was read: %d", response.Code)
	}
	prefixed := request(http.MethodGet, "/api/v1/auth/me", "")
	prefixed.TLS = &tlsStateForTest
	prefixed.AddCookie(&http.Cookie{Name: SecureSessionCookie, Value: issued.Value})
	if response := h.do(prefixed); response.Code != http.StatusOK {
		t.Fatalf("over HTTPS the __Host- cookie was refused: %d %s", response.Code, response.Body)
	}
}

func TestLoginFailures(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tests := []struct {
		name string
		body string
		want int
	}{
		{"wrong password", loginBody(testUser, "nope"), http.StatusUnauthorized},
		{"wrong user", loginBody("root", testPassword), http.StatusUnauthorized},
		{"not json", "username=jeff", http.StatusBadRequest},
		{"oversized", `{"username":"` + strings.Repeat("a", maxLoginBody) + `"}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		r := request(http.MethodPost, "/api/v1/auth/login", tc.body)
		r.RemoteAddr = "10.9.9.9:1" // its own rate-limit bucket
		response := h.do(r)
		if response.Code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, response.Code, tc.want)
		}
		if len(response.Result().Cookies()) != 0 {
			t.Errorf("%s: a failed login set a cookie", tc.name)
		}
	}
}

func TestLoginLockoutReturns429WithRetryAfter(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for i := range auth.PerClientFailures {
		response := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, "wrong")))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, response.Code)
		}
	}
	response := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword)))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out login: %d", response.Code)
	}
	if response.Header().Get("Retry-After") != "60" || decodeError(t, response).Code != codeRateLimited {
		t.Fatalf("Retry-After %q body %s", response.Header().Get("Retry-After"), response.Body)
	}
	other := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))
	other.RemoteAddr = "127.0.0.9:1"
	if code := h.do(other).Code; code != http.StatusOK {
		t.Fatalf("a different client was locked out too: %d", code)
	}
	h.clock.Advance(auth.FailureWindow + time.Second)
	if code := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))).Code; code != http.StatusOK {
		t.Fatalf("lockout did not lift: %d", code)
	}
}

func TestMe(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if response := h.do(request(http.MethodGet, "/api/v1/auth/me", "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("me without a cookie: %d", response.Code)
	}
	bogus := withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), &http.Cookie{Name: SessionCookie, Value: "forged"})
	response := h.do(bogus)
	if response.Code != http.StatusUnauthorized || sessionCookieFrom(t, response).MaxAge >= 0 {
		t.Fatalf("forged cookie: %d, and it should be cleared", response.Code)
	}
	cookie := h.login()
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("me with cookie: %d", response.Code)
	}
	var envelope struct{ Data MeResponse }
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	want := MeResponse{Username: testUser, Features: Features{Auth: true, Session: "crowsnest-test", HerdrVersion: "0.9.5-test"}}
	if envelope.Data != want {
		t.Fatalf("me %+v, want %+v", envelope.Data, want)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	h.clock.Advance(auth.SessionTTL + time.Minute)
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: %d", response.Code)
	}
	if sessionCookieFrom(t, response).MaxAge >= 0 {
		t.Fatal("expired session cookie should be cleared")
	}
}

func TestSlidingRefreshResendsCookie(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	h.clock.Advance(10 * 24 * time.Hour)
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("me: %d", response.Code)
	}
	if refreshed := sessionCookieFrom(t, response); refreshed.Value != cookie.Value {
		t.Fatal("refresh must keep the same token")
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/auth/logout", ""), cookie))
	if response.Code != http.StatusOK || sessionCookieFrom(t, response).MaxAge >= 0 {
		t.Fatalf("logout: %d %v", response.Code, response.Result().Cookies())
	}
	if code := h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)).Code; code != http.StatusUnauthorized {
		t.Fatalf("session usable after logout: %d", code)
	}
}

func TestNoAuth(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	response := h.do(request(http.MethodGet, "/api/v1/auth/me", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("me with --no-auth: %d", response.Code)
	}
	var envelope struct{ Data MeResponse }
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	if envelope.Data.Features.Auth || envelope.Data.Username != "" {
		t.Fatalf("me %+v", envelope.Data)
	}
	if code := h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody("x", "y"))).Code; code != http.StatusOK {
		t.Fatalf("login with --no-auth should just succeed: %d", code)
	}
}

// ---- Headers and logging -----------------------------------------------------

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	evilHost := request(http.MethodGet, "/", "")
	evilHost.Host = "evil.example"
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/health", ""),
		request(http.MethodGet, "/", ""),
		request(http.MethodGet, "/api/v1/auth/me", ""), // 401
		request(http.MethodGet, "/assets/missing.js", ""),
		evilHost, // rejected by the host check: still gets the headers
	} {
		response := h.do(r)
		want := map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "same-origin",
			"Content-Security-Policy": contentSecurityPolicy,
		}
		for key, value := range want {
			if got := response.Header().Get(key); got != value {
				t.Errorf("%s %s: %s = %q, want %q", r.Method, r.URL.Path, key, got, value)
			}
		}
	}
	if !strings.Contains(contentSecurityPolicy, "script-src 'self';") || strings.Contains(contentSecurityPolicy, "unsafe-eval") {
		t.Fatal("CSP must allow only same-origin script")
	}
}

func TestRequestLogNeverRecordsSecrets(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me?token=query-secret", ""), cookie))
	h.do(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, "wrong-password-secret")))
	logs := h.logs.String()
	for _, secret := range []string{testPassword, "wrong-password-secret", cookie.Value, "query-secret"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains a secret %q:\n%s", secret, logs)
		}
	}
	for _, field := range []string{"method=GET", "path=/api/v1/auth/me", "status=200", "duration_ms=", "client=127.0.0.1"} {
		if !strings.Contains(logs, field) {
			t.Errorf("log is missing %q:\n%s", field, logs)
		}
	}
}

// ---- SPA ---------------------------------------------------------------------

func TestSPARoutingAndCaching(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tests := []struct {
		path      string
		status    int
		cache     string
		bodyHas   string
		typePrefx string
	}{
		{"/", 200, cacheRevalidate, "<title>crowsnest</title>", "text/html"},
		{"/inbox", 200, cacheRevalidate, "<title>crowsnest</title>", "text/html"},
		{"/fleet/some/deep/route", 200, cacheRevalidate, "<title>crowsnest</title>", "text/html"},
		{"/index.html", 200, cacheRevalidate, "<title>crowsnest</title>", "text/html"},
		{"/assets/index-abc123.js", 200, cacheImmutable, "console.log", "text/javascript"},
		{"/assets/index-abc123.css", 200, cacheImmutable, "body{}", "text/css"},
		{"/assets/nested/chunk-1.js", 200, cacheImmutable, "1", "text/javascript"},
		{"/theme-init.js", 200, cacheRevalidate, "theme", "text/javascript"},
		{"/assets/missing-999.js", 404, "", "", ""},
		{"/favicon.ico", 404, "", "", ""},
		{"/assets", 404, "", "", ""},
		{"/etc/passwd", 200, cacheRevalidate, "<title>crowsnest</title>", "text/html"}, // SPA route, never a host file
	}
	for _, tc := range tests {
		response := h.do(request(http.MethodGet, tc.path, ""))
		if response.Code != tc.status {
			t.Errorf("%s: status %d, want %d", tc.path, response.Code, tc.status)
			continue
		}
		if tc.status != 200 {
			continue
		}
		if got := response.Header().Get("Cache-Control"); got != tc.cache {
			t.Errorf("%s: Cache-Control %q, want %q", tc.path, got, tc.cache)
		}
		if !strings.Contains(response.Body.String(), tc.bodyHas) {
			t.Errorf("%s: body %q", tc.path, response.Body)
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), tc.typePrefx) {
			t.Errorf("%s: Content-Type %q", tc.path, response.Header().Get("Content-Type"))
		}
	}
	if code := h.do(request(http.MethodPost, "/", "")).Code; code != http.StatusMethodNotAllowed {
		t.Errorf("POST to the SPA: %d", code)
	}
}

func TestWebDevPlaceholderAndMissingAssets(t *testing.T) {
	deps := Deps{Config: testConfig(true), Hub: hub.New(), Logger: slog.New(slog.DiscardHandler)}
	db, _ := store.OpenMemory(context.Background())
	defer func() { _ = db.Close() }()
	deps.Auth, _ = auth.New(context.Background(), auth.Options{NoAuth: true}, db)
	if _, err := New(deps); err == nil {
		t.Fatal("a server without web assets and without --web-dev should refuse to build")
	}
	deps.Config.WebDev = true
	server, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request(http.MethodGet, "/", ""))
	if !strings.Contains(recorder.Body.String(), "Vite") {
		t.Fatalf("placeholder: %s", recorder.Body)
	}
}

// ---- SSE ---------------------------------------------------------------------

// sseReader reads SSE frames from a live response.
type sseReader struct {
	t      *testing.T
	body   io.ReadCloser
	frames chan map[string]string
}

func openEvents(t *testing.T, base string, cookie *http.Cookie) (*http.Response, *sseReader) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/events", nil)
	if cookie != nil {
		r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	reader := &sseReader{t: t, body: response.Body, frames: make(chan map[string]string, 16)}
	go reader.pump()
	return response, reader
}

func (s *sseReader) pump() {
	defer close(s.frames)
	buffer := make([]byte, 4096)
	var pending string
	for {
		n, err := s.body.Read(buffer)
		pending += string(buffer[:n])
		for {
			index := strings.Index(pending, "\n\n")
			if index < 0 {
				break
			}
			frame := map[string]string{}
			for line := range strings.SplitSeq(pending[:index], "\n") {
				if key, value, ok := strings.Cut(line, ": "); ok {
					frame[key] = value
				}
			}
			pending = pending[index+2:]
			s.frames <- frame
		}
		if err != nil {
			return
		}
	}
}

// next returns the next frame that names an event (skipping the retry frame), or fails.
func (s *sseReader) next() map[string]string {
	s.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-s.frames:
			if !ok {
				return nil
			}
			if frame["event"] != "" {
				return frame
			}
		case <-timeout:
			s.t.Fatal("timed out waiting for an SSE event")
		}
	}
}

func liveServer(t *testing.T, h *harness) string {
	t.Helper()
	live := httptest.NewUnstartedServer(h.server.Handler())
	live.Start()
	t.Cleanup(live.Close)
	t.Cleanup(h.server.beginShutdown) // let streams end before the server closes
	return live.URL
}

func TestEventsRequireAuth(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	response, _ := openEvents(t, liveServer(t, h), nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("events without a session: %d", response.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	h := newHarness(t, harnessOptions{heartbeat: 50 * time.Millisecond})
	base := liveServer(t, h)
	cookie := h.loginAt(base)
	response, events := openEvents(t, base, cookie)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	hello := events.next()
	if hello["event"] != "hello" || !strings.Contains(hello["data"], `"version":"v9.9.9-test"`) {
		t.Fatalf("hello frame %v", hello)
	}
	waitFor(t, func() bool { return h.hub.Subscribers() == 1 })
	h.hub.Publish(hub.Event{Type: "fleet", Data: map[string]int{"workspaces": 3}})
	if frame := events.next(); frame["event"] != "fleet" || frame["data"] != `{"workspaces":3}` {
		t.Fatalf("published frame %v", frame)
	}
	if frame := events.next(); frame["event"] != "heartbeat" || !strings.Contains(frame["data"], `"at"`) {
		t.Fatalf("heartbeat frame %v", frame)
	}
}

func TestEventsEndWhenSessionIsRevoked(t *testing.T) {
	h := newHarness(t, harnessOptions{heartbeat: 50 * time.Millisecond})
	base := liveServer(t, h)
	cookie := h.loginAt(base)
	_, events := openEvents(t, base, cookie)
	if hello := events.next(); hello == nil || hello["event"] != "hello" {
		t.Fatalf("the stream did not open: %v", hello)
	}
	h.do(withCookie(request(http.MethodPost, "/api/v1/auth/logout", ""), cookie))
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-events.frames:
			if !ok {
				return // stream closed by the server
			}
		case <-timeout:
			t.Fatal("stream kept running after logout")
		}
	}
}

func TestEventsEndOnShutdown(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	base := liveServer(t, h)
	_, events := openEvents(t, base, nil)
	events.next()
	h.server.beginShutdown()
	select {
	case <-drain(events.frames):
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end on shutdown")
	}
}

func drain(frames chan map[string]string) chan struct{} {
	done := make(chan struct{})
	go func() {
		for range frames {
		}
		close(done)
	}()
	return done
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
