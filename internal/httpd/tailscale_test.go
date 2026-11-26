// Requests shaped like `tailscale serve`'s (DESIGN.md 3.5): the peer is loopback, Host is the
// machine's MagicDNS name, X-Forwarded-Proto is https. The name is allowed because start detects
// it; the login gets the Secure __Host- cookie; a WebSocket upgrade from the https:// origin
// passes the origin check. A non-loopback peer sending the same headers is treated as plain HTTP,
// and without --trusted-proxy the forwarded client address is not believed.

package httpd

import (
	"fmt"
	"net/http"
	"testing"
)

const magicDNS = "quark-2.tail1234.ts.net"

func viaServe(r *http.Request) *http.Request {
	r.Host = magicDNS
	r.RemoteAddr = "127.0.0.1:41000"
	r.Header.Set("X-Forwarded-Proto", "https")
	if r.Header.Get("Origin") != "" {
		r.Header.Set("Origin", "https://"+magicDNS)
	}
	return r
}

func TestThroughTailscaleServe(t *testing.T) {
	h := newHarness(t, harnessOptions{hostNames: []string{magicDNS}})
	login := h.do(viaServe(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))))
	if login.Code != http.StatusOK {
		t.Fatalf("login through serve: %d %s", login.Code, login.Body)
	}
	cookie := sessionCookieFrom(t, login)
	if cookie.Name != SecureSessionCookie || !cookie.Secure {
		t.Fatalf("cookie %+v, want the Secure __Host- one", cookie)
	}
	me := h.do(withCookie(viaServe(request(http.MethodGet, "/api/v1/auth/me", "")), cookie))
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
	// A WebSocket upgrade from the https origin passes the origin check (503 here only because
	// this harness has no terminals); another origin does not.
	upgrade := withCookie(viaServe(request(http.MethodGet, "/api/v1/term/tui", "")), cookie)
	upgrade.Header.Set("Upgrade", "websocket")
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Origin", "https://"+magicDNS)
	if response := h.do(upgrade); response.Code == http.StatusForbidden {
		t.Fatalf("an upgrade from https://%s was refused: %s", magicDNS, response.Body)
	}
	upgrade.Header.Set("Origin", "https://evil.example")
	if response := h.do(upgrade); response.Code != http.StatusForbidden {
		t.Fatalf("an upgrade from another origin: %d", response.Code)
	}
}

func TestMagicDNSNameIsRefusedUnlessDetected(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	response := h.do(viaServe(request(http.MethodGet, "/api/v1/health", "")))
	if response.Code != http.StatusForbidden {
		t.Fatalf("Host %s without detection: %d", magicDNS, response.Code)
	}
}

func TestServeHeadersFromANonLoopbackPeerArePlain(t *testing.T) {
	h := newHarness(t, harnessOptions{hostNames: []string{magicDNS}})
	r := viaServe(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword)))
	r.RemoteAddr = "192.0.2.7:41000"
	cookie := sessionCookieFrom(t, h.do(r))
	if cookie.Secure || cookie.Name != SessionCookie {
		t.Fatalf("a remote peer's X-Forwarded-Proto was believed: %+v", cookie)
	}
}

// Without --trusted-proxy, every client behind serve is one bucket for the login limiter: a
// fresh X-Forwarded-For on each attempt does not buy fresh attempts (doctor and the README say
// to set CROWSNEST_TRUSTED_PROXY=loopback with serve).
func TestForwardedForIsNotBelievedWithoutTrustedProxy(t *testing.T) {
	h := newHarness(t, harnessOptions{hostNames: []string{magicDNS}})
	var last int
	for i := range 7 {
		r := viaServe(request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, "wrong")))
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		last = h.do(r).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("fresh X-Forwarded-For values escaped the limiter: %d", last)
	}
}
