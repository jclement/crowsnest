// The publish-to-tailnet routes: anyone signed in may read the card; only a password session may
// publish or stop (not one signed in with Tailscale, through the card or through Settings);
// POST and DELETE need Origin and X-Crowsnest like every change; --no-auth is refused; a plan
// that cannot publish is a 409 with its reason, and nothing is turned on. Also: a request through
// serve on a non-443 port passes the host and origin checks once the name is allowed.

package httpd

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/system"
	"github.com/jclement/crowsnest/internal/tsserve"
)

// fakePublisher is a TailscaleServe whose plan is fixed.
type fakePublisher struct {
	mu      sync.Mutex
	enabled bool
	plan    tsserve.View
}

func (f *fakePublisher) View(context.Context) tsserve.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	view := f.plan
	view.Enabled = f.enabled
	if f.enabled && view.State == tsserve.StateOff {
		view.State = tsserve.StatePublishing
	}
	return view
}

func (f *fakePublisher) SetEnabled(on bool) { f.mu.Lock(); f.enabled = on; f.mu.Unlock() }
func (f *fakePublisher) Enabled() bool      { f.mu.Lock(); defer f.mu.Unlock(); return f.enabled }

func offer() *fakePublisher {
	return &fakePublisher{plan: tsserve.View{Show: true, State: tsserve.StateOff, URL: "https://" + magicDNS + ":8443", Port: 8443, Name: magicDNS}}
}

func TestTailscaleServePublishAndStop(t *testing.T) {
	publisher := offer()
	h := newHarness(t, harnessOptions{tailscaleServe: publisher})
	cookie := h.login()
	if response := h.do(request(http.MethodGet, "/api/v1/tailscale/serve", "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %d", response.Code)
	}
	card := h.do(withCookie(request(http.MethodGet, "/api/v1/tailscale/serve", ""), cookie))
	if card.Code != http.StatusOK || !strings.Contains(card.Body.String(), `"state":"off"`) || !strings.Contains(card.Body.String(), `:8443"`) || strings.Contains(card.Body.String(), `"qr"`) {
		t.Fatalf("offer: %d %s", card.Code, card.Body)
	}

	// CSRF: no X-Crowsnest, or another Origin, is refused before anything changes.
	forged := withCookie(request(http.MethodPost, "/api/v1/tailscale/serve", "{}"), cookie)
	forged.Header.Del("X-Crowsnest")
	if response := h.do(forged); response.Code != http.StatusForbidden || publisher.Enabled() {
		t.Fatalf("without X-Crowsnest: %d", response.Code)
	}
	forged = withCookie(request(http.MethodPost, "/api/v1/tailscale/serve", "{}"), cookie)
	forged.Header.Set("Origin", "https://evil.example")
	if response := h.do(forged); response.Code != http.StatusForbidden || publisher.Enabled() {
		t.Fatalf("cross-origin: %d", response.Code)
	}

	published := h.do(withCookie(request(http.MethodPost, "/api/v1/tailscale/serve", "{}"), cookie))
	if published.Code != http.StatusOK || !publisher.Enabled() || !strings.Contains(published.Body.String(), `"qr":"data:image/png;base64,`) {
		t.Fatalf("publish: %d %.300s", published.Code, published.Body)
	}
	stopped := h.do(withCookie(request(http.MethodDelete, "/api/v1/tailscale/serve", ""), cookie))
	if stopped.Code != http.StatusOK || publisher.Enabled() {
		t.Fatalf("stop: %d %s", stopped.Code, stopped.Body)
	}
}

func TestTailscaleServeRefusesATailscaleSession(t *testing.T) {
	publisher := offer()
	h := newHarness(t, harnessOptions{hostNames: []string{magicDNS}, tailscale: true, serveStatus: serveStatusForTest, tailscaleServe: publisher})
	allow(t, h, "friend@github")
	tailnet := sessionCookieFrom(t, h.do(tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "friend@github")))
	if read := h.do(withCookie(tailnetRequest(http.MethodGet, "/api/v1/tailscale/serve", "", "friend@github"), tailnet)); read.Code != http.StatusOK {
		t.Fatalf("a Tailscale session may read the card: %d", read.Code)
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		response := h.do(withCookie(tailnetRequest(method, "/api/v1/tailscale/serve", "{}", "friend@github"), tailnet))
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s from a Tailscale session: %d %s", method, response.Code, response.Body)
		}
	}
	if publisher.Enabled() {
		t.Fatal("a Tailscale session turned publishing on")
	}
}

func TestTailscaleServeRefusedWithNoAuth(t *testing.T) {
	publisher := offer()
	h := newHarness(t, harnessOptions{noAuth: true, tailscaleServe: publisher})
	response := h.do(request(http.MethodPost, "/api/v1/tailscale/serve", "{}"))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "--no-auth") || publisher.Enabled() {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
}

func TestTailscaleServeRefusedPlanIsNotTurnedOn(t *testing.T) {
	publisher := &fakePublisher{plan: tsserve.View{Show: true, State: tsserve.StateRefused, Reason: "HTTPS certificates are off for your tailnet.", Fix: "Admin console → DNS → HTTPS Certificates."}}
	h := newHarness(t, harnessOptions{tailscaleServe: publisher})
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/tailscale/serve", "{}"), h.login()))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "HTTPS certificates are off") || publisher.Enabled() {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
}

// tailscale serve on 8443 keeps the port in Host and Origin; the host check ignores the port and
// the origin check compares host and port, so a request through it is accepted once the name is
// allowed, including a name added after start (AllowHost).
func TestThroughServeOnAnotherPort(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	through := func() *http.Request {
		r := withCookie(request(http.MethodPost, "/api/v1/auth/logout", "{}"), cookie)
		r.Host, r.RemoteAddr = magicDNS+":8443", "127.0.0.1:41000"
		r.Header.Set("Origin", "https://"+magicDNS+":8443")
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	if response := h.do(through()); response.Code != http.StatusForbidden {
		t.Fatalf("before the name is allowed: %d", response.Code)
	}
	h.server.AllowHost(magicDNS + ".")
	if !h.server.HostAllowed(magicDNS) {
		t.Fatal("AllowHost did not take")
	}
	if response := h.do(through()); response.Code == http.StatusForbidden {
		t.Fatalf("through serve on :8443: %d %s", response.Code, response.Body)
	}
	mismatched := through()
	mismatched.Header.Set("Origin", "https://"+magicDNS)
	if response := h.do(mismatched); response.Code != http.StatusForbidden {
		t.Fatalf("an Origin without the port against Host :8443: %d", response.Code)
	}
}

// Publishing is remembered: the card saves the tailscale-serve setting (kv settings.*), whose
// Apply turns the publisher on; stopping saves it off. A Tailscale session cannot flip it
// through Settings either.
func TestTailscaleServeIsASavedSetting(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	load := func(stored map[string]string) (config.Config, error) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.RegisterFlags(flags)
		return config.Load(flags, config.Options{
			Stored: stored, Getenv: func(string) (string, bool) { return "", false },
			HomeDir: func() (string, error) { return home, nil }, Interfaces: func() ([]config.Interface, error) { return nil, nil },
		})
	}
	cfg, err := load(nil)
	if err != nil {
		t.Fatal(err)
	}
	publisher := offer()
	service := system.New(system.Options{Config: cfg, Version: "test", KV: db, Reload: load, Env: doctor.DemoEnv("ready"), Logger: slog.New(slog.DiscardHandler),
		Apply: func(_, next config.Config) { publisher.SetEnabled(next.TailscaleServe) }})
	h := newHarness(t, harnessOptions{system: service, tailscaleServe: publisher, hostNames: []string{magicDNS}, tailscale: true, serveStatus: serveStatusForTest})
	cookie := h.login()
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/tailscale/serve", "{}"), cookie)); response.Code != http.StatusOK || !publisher.Enabled() {
		t.Fatalf("publish: %d %s", response.Code, response.Body)
	}
	if saved, err := db.GetKV(context.Background(), "settings.tailscale-serve"); err != nil || saved != "true" {
		t.Fatalf("saved %q %v", saved, err)
	}
	if response := h.do(withCookie(request(http.MethodDelete, "/api/v1/tailscale/serve", ""), cookie)); response.Code != http.StatusOK || publisher.Enabled() {
		t.Fatalf("stop: %d %s", response.Code, response.Body)
	}
	if saved, _ := db.GetKV(context.Background(), "settings.tailscale-serve"); saved != "false" {
		t.Fatalf("saved %q after stop", saved)
	}
	allow(t, h, "friend@github")
	tailnet := sessionCookieFrom(t, h.do(tailnetRequest(http.MethodGet, "/api/v1/auth/me", "", "friend@github")))
	response := h.do(withCookie(tailnetRequest(http.MethodPut, "/api/v1/settings/tailscale-serve", `{"value":"true"}`, "friend@github"), tailnet))
	if response.Code != http.StatusForbidden || publisher.Enabled() {
		t.Fatalf("Settings from a Tailscale session: %d %s", response.Code, response.Body)
	}
}
