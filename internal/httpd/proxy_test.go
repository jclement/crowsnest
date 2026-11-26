package httpd

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"

	"github.com/jclement/crowsnest/internal/config"
)

var loopbackProxy = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// R2-L2: through a trusted proxy, each internet client has its own login bucket, so an
// attacker's failures no longer lock the owner out; without the flag the header is ignored.
func TestTrustedProxyGivesEachClientItsOwnLoginBucket(t *testing.T) {
	loginAs := func(h *harness, client, password string) int {
		r := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, password))
		r.Header.Set("CF-Connecting-IP", client)
		return h.do(r).Code
	}
	h := newHarness(t, harnessOptions{trustedProxies: loopbackProxy, trustedProxyHeader: config.HeaderCFConnectingIP})
	for range 6 {
		loginAs(h, "203.0.113.9", "wrong")
	}
	if code := loginAs(h, "203.0.113.9", "wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("the attacker's own bucket: %d", code)
	}
	if code := loginAs(h, "198.51.100.4", testPassword); code != http.StatusOK {
		t.Fatalf("the owner, from another address, was locked out: %d", code)
	}
	// Without --trusted-proxy the header is the client's to forge, so it is ignored: one bucket.
	plain := newHarness(t, harnessOptions{})
	for range 6 {
		loginAs(plain, "203.0.113.9", "wrong")
	}
	if code := loginAs(plain, "198.51.100.4", testPassword); code != http.StatusTooManyRequests {
		t.Fatalf("an untrusted CF-Connecting-IP picked its own bucket: %d", code)
	}
}

func TestForwardedClient(t *testing.T) {
	for _, tc := range []struct{ header, cf, xff, want string }{
		{header: config.HeaderCFConnectingIP, cf: "203.0.113.9", want: "203.0.113.9"},
		{header: config.HeaderCFConnectingIP, cf: "not an ip", xff: "198.51.100.1", want: "invalid IP"},
		{header: config.HeaderXForwardedFor, xff: "10.0.0.1, 203.0.113.7", want: "203.0.113.7"},
		{header: config.HeaderXForwardedFor, cf: "203.0.113.9", xff: "198.51.100.1", want: "198.51.100.1"},
		{header: config.HeaderXForwardedFor, xff: "garbage", want: "invalid IP"},
	} {
		r := request(http.MethodGet, "/", "")
		if tc.cf != "" {
			r.Header.Set("CF-Connecting-IP", tc.cf)
		}
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := forwardedClient(r, tc.header).String(); got != tc.want {
			t.Errorf("%s: cf %q xff %q: %s, want %s", tc.header, tc.cf, tc.xff, got, tc.want)
		}
	}
}

// R3-L1: behind a proxy that passes headers through (tailscale serve), a client's own
// CF-Connecting-IP must not buy it a fresh login bucket on every attempt.
func TestSpoofedCFConnectingIPIsIgnoredBehindAnXFFProxy(t *testing.T) {
	h := newHarness(t, harnessOptions{trustedProxies: loopbackProxy})
	attempt := func(spoof, password string) int {
		r := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, password))
		r.Header.Set("CF-Connecting-IP", spoof)
		r.Header.Set("X-Forwarded-For", "100.64.0.9") // what tailscale serve appends: the real peer
		return h.do(r).Code
	}
	for i := range 6 {
		attempt(fmt.Sprintf("203.0.113.%d", i+1), "wrong")
	}
	if code := attempt("203.0.113.99", "wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("a fresh CF-Connecting-IP on every attempt escaped the per-client limit: %d", code)
	}
}

// Secure follows X-Forwarded-Proto from a trusted proxy even off loopback, and never from an
// untrusted peer.
func TestSecureCookieFromATrustedProxy(t *testing.T) {
	h := newHarness(t, harnessOptions{trustedProxies: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")}})
	for remote, want := range map[string]bool{"10.9.1.2:5000": true, "192.0.2.7:5000": false} {
		r := request(http.MethodPost, "/api/v1/auth/login", loginBody(testUser, testPassword))
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.50")
		if cookie := sessionCookieFrom(t, h.do(r)); cookie.Secure != want {
			t.Errorf("from %s: Secure=%v, want %v", remote, cookie.Secure, want)
		}
	}
}

func TestPerClientCapThroughAProxy(t *testing.T) {
	limiter := &perClient{count: map[string]int{}, limit: 2}
	for i, want := range []bool{true, true, false} {
		if got := limiter.enter("a"); got != want {
			t.Fatalf("enter %d = %v, want %v", i, got, want)
		}
	}
	if !limiter.enter("b") {
		t.Fatal("another client was capped by the first")
	}
	limiter.leave("a")
	if !limiter.enter("a") {
		t.Fatal("a slot was not given back")
	}
}
