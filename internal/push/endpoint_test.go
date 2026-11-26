// Tests for subscription validation and the SSRF guard.

package push

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		endpoint     string
		loopbackHTTP bool
		ok           bool
	}{
		{"https://fcm.googleapis.com/fcm/send/abc", false, true},
		{"https://updates.push.services.mozilla.com/wpush/v2/abc", false, true},
		{"https://web.push.apple.com/QGx", false, true},
		{"https://8.8.8.8/push", false, true},
		{"http://fcm.googleapis.com/fcm/send/abc", false, false},
		{"ftp://push.example.com/x", false, false},
		{"https://localhost/push", false, false},
		{"https://api.localhost/push", false, false},
		{"https://127.0.0.1/push", false, false},
		{"https://[::1]/push", false, false},
		{"https://10.0.0.1/push", false, false},
		{"https://192.168.1.1/push", false, false},
		{"https://172.16.0.9/push", false, false},
		{"https://169.254.169.254/latest/meta-data", false, false},
		{"https://100.100.1.2/push", false, false}, // tailnet / CGNAT
		{"https://[fd7a:115c:a1e0::1]/push", false, false},
		{"https://[fe80::1]/push", false, false},
		{"https://0.0.0.0/push", false, false},
		{"https://[::ffff:10.0.0.1]/push", false, false},
		{"https://[64:ff9b::a00:1]/push", false, false},
		{"https://user:pass@push.example.com/x", false, false},
		{"https://push.example.com/x#frag", false, false},
		{"/relative", false, false},
		{"", false, false},
		{"https://push.example.com/" + strings.Repeat("a", 2100), false, false},
		{"http://127.0.0.1:9999/push", true, true},  // --demo's fake push service
		{"http://10.0.0.1:9999/push", true, false},  // only loopback, even in --demo
		{"http://127.0.0.1:9999/push", false, false}, // never outside --demo
	}
	for _, c := range cases {
		err := ValidateEndpoint(c.endpoint, c.loopbackHTTP)
		if (err == nil) != c.ok {
			t.Errorf("ValidateEndpoint(%q, %v) = %v, want ok=%v", c.endpoint, c.loopbackHTTP, err, c.ok)
		}
		var invalid *ValidationError
		if err != nil && !errors.As(err, &invalid) {
			t.Errorf("%q: error is not a ValidationError: %v", c.endpoint, err)
		}
	}
}

func TestValidateSubscriptionKeys(t *testing.T) {
	good := newBrowser(t).subscription("https://push.example.com/x")
	if err := ValidateSubscription(good, false); err != nil {
		t.Fatal(err)
	}
	padded := good
	padded.Keys.Auth += "=="
	if err := ValidateSubscription(padded, false); err != nil {
		t.Fatalf("padding must be accepted: %v", err)
	}
	for name, mutate := range map[string]func(*Subscription){
		"short p256dh":      func(s *Subscription) { s.Keys.P256dh = s.Keys.P256dh[:20] },
		"compressed point":  func(s *Subscription) { s.Keys.P256dh = "A" + s.Keys.P256dh[1:] },
		"not base64":        func(s *Subscription) { s.Keys.Auth = "!!!!!!!!!!!!!!!!!!!!!!" },
		"long auth":         func(s *Subscription) { s.Keys.Auth += "AAAA" },
		"missing auth":      func(s *Subscription) { s.Keys.Auth = "" },
		"private endpoint":  func(s *Subscription) { s.Endpoint = "https://10.1.1.1/x" },
		"missing endpoint":  func(s *Subscription) { s.Endpoint = "" },
		"http endpoint":     func(s *Subscription) { s.Endpoint = "http://push.example.com/x" },
		"metadata endpoint": func(s *Subscription) { s.Endpoint = "https://169.254.169.254/x" },
	} {
		sub := good
		mutate(&sub)
		if err := ValidateSubscription(sub, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPublicAddress(t *testing.T) {
	for address, want := range map[string]bool{
		"8.8.8.8": true, "142.250.72.10": true, "2607:f8b0:4005:80c::200a": true,
		"127.0.0.1": false, "10.2.3.4": false, "172.31.255.1": false, "192.168.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "100.127.255.254": false, "0.0.0.0": false,
		"224.0.0.1": false, "255.255.255.255": false, "::1": false, "fe80::1": false, "fc00::1": false,
		"fd7a:115c:a1e0::1": false, "::ffff:192.168.1.1": false, "64:ff9b::808:808": false, "::": false,
	} {
		if got := publicAddress(netip.MustParseAddr(address)); got != want {
			t.Errorf("publicAddress(%s) = %v, want %v", address, got, want)
		}
	}
}

func TestDialGuard(t *testing.T) {
	for _, c := range []struct {
		address       string
		allowLoopback bool
		ok            bool
	}{
		{"8.8.8.8:443", false, true},
		{"127.0.0.1:443", false, false},
		{"127.0.0.1:443", true, true},
		{"10.0.0.1:443", true, false},
		{"[::1]:443", false, false},
		{"not-an-address", false, false},
	} {
		if err := dialAllowed(c.address, c.allowLoopback); (err == nil) != c.ok {
			t.Errorf("dialAllowed(%s, %v) = %v, want ok=%v", c.address, c.allowLoopback, err, c.ok)
		}
	}
}

// The guarded client refuses to connect to loopback even when the URL looks harmless: this is the
// DNS-rebinding fence (a public name that resolves inside is caught at dial time).
func TestGuardedClientRefusesLoopbackAtDialTime(t *testing.T) {
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer server.Close()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	if _, err := GuardedClient(false).Do(request); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
	if reached {
		t.Fatal("the request reached a loopback server")
	}
	// With --demo's loopback allowance the same request goes through.
	request, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	response, err := GuardedClient(true).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestGuardedClientDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	redirector := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusTemporaryRedirect))
	defer redirector.Close()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, redirector.URL, nil)
	response, err := GuardedClient(true).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status %d", response.StatusCode)
	}
}
