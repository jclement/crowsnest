// Tests for the /push routes: they need a session and the CSRF header like every other route,
// validate subscriptions, cap devices, never return an endpoint, and limit test notifications.

package httpd

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/store"
)

func newPushHarness(t *testing.T, noAuth bool) *harness {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := push.New(context.Background(), push.Options{Store: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return newHarness(t, harnessOptions{noAuth: noAuth, push: service})
}

// subscriptionJSON is a browser's PushSubscription.toJSON() with real-shaped keys.
func subscriptionJSON(t *testing.T, endpoint string) string {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	body, _ := json.Marshal(map[string]any{
		"endpoint":       endpoint,
		"expirationTime": nil,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
	})
	return string(body)
}

func TestPushRoutesNeedASession(t *testing.T) {
	h := newPushHarness(t, false)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/push/key"}, {http.MethodGet, "/api/v1/push/settings"}, {http.MethodPut, "/api/v1/push/settings"},
		{http.MethodPost, "/api/v1/push/subscriptions"}, {http.MethodDelete, "/api/v1/push/subscriptions"},
		{http.MethodDelete, "/api/v1/push/subscriptions/1"}, {http.MethodPost, "/api/v1/push/test"},
	} {
		if response := h.do(request(route.method, route.path, "{}")); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", route.method, route.path, response.Code)
		}
	}
	cookie := h.login()
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/push/key", ""), cookie)); response.Code != http.StatusOK {
		t.Fatalf("with a session: %d %s", response.Code, response.Body)
	}
}

func TestPushRoutesNeedTheCSRFHeader(t *testing.T) {
	h := newPushHarness(t, true)
	r := request(http.MethodPost, "/api/v1/push/subscriptions", subscriptionJSON(t, "https://push.example.com/a"))
	r.Header.Del("X-Crowsnest")
	if response := h.do(r); response.Code != http.StatusForbidden {
		t.Fatalf("without X-Crowsnest: %d", response.Code)
	}
	r = request(http.MethodPost, "/api/v1/push/subscriptions", subscriptionJSON(t, "https://push.example.com/a"))
	r.Header.Set("Origin", "http://evil.example")
	if response := h.do(r); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", response.Code)
	}
}

func TestPushKeyIsThePublicKeyOnly(t *testing.T) {
	h := newPushHarness(t, true)
	response := h.do(request(http.MethodGet, "/api/v1/push/key", ""))
	var body struct {
		Data map[string]string `json:"data"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if len(body.Data) != 1 || body.Data["public_key"] == "" {
		t.Fatalf("body %s", response.Body)
	}
}

func TestPushSubscribeListRemove(t *testing.T) {
	h := newPushHarness(t, true)
	endpoint := "https://fcm.googleapis.com/fcm/send/device-secret-token"
	r := request(http.MethodPost, "/api/v1/push/subscriptions", subscriptionJSON(t, endpoint))
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone)")
	response := h.do(r)
	if response.Code != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "device-secret-token") {
		t.Fatal("a subscribe response must not echo the endpoint")
	}
	listed := h.do(request(http.MethodGet, "/api/v1/push/settings", ""))
	var settings struct {
		Data PushSettingsResponse `json:"data"`
	}
	_ = json.Unmarshal(listed.Body.Bytes(), &settings)
	if len(settings.Data.Devices) != 1 || settings.Data.Devices[0].UserAgent != "Mozilla/5.0 (iPhone)" ||
		settings.Data.Devices[0].Service != "fcm.googleapis.com" || settings.Data.Devices[0].EndpointHash != push.EndpointHash(endpoint) {
		t.Fatalf("settings %+v", settings.Data)
	}
	if strings.Contains(listed.Body.String(), "device-secret-token") {
		t.Fatal("the device list must not carry endpoints")
	}
	if len(settings.Data.Order) != len(push.AllKinds) || !settings.Data.Kinds[push.KindDone] {
		t.Fatalf("kinds %+v", settings.Data)
	}
	// This browser turning notifications off.
	if response := h.do(request(http.MethodDelete, "/api/v1/push/subscriptions", fmt.Sprintf(`{"endpoint":%q}`, endpoint))); !strings.Contains(response.Body.String(), `"removed":true`) {
		t.Fatalf("unsubscribe: %d %s", response.Code, response.Body)
	}
	// Removing by id; a second removal is a 404.
	h.do(request(http.MethodPost, "/api/v1/push/subscriptions", subscriptionJSON(t, endpoint)))
	listed = h.do(request(http.MethodGet, "/api/v1/push/settings", ""))
	_ = json.Unmarshal(listed.Body.Bytes(), &settings)
	path := fmt.Sprintf("/api/v1/push/subscriptions/%d", settings.Data.Devices[0].ID)
	if response := h.do(request(http.MethodDelete, path, "")); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body)
	}
	if response := h.do(request(http.MethodDelete, path, "")); response.Code != http.StatusNotFound {
		t.Fatalf("second remove: %d", response.Code)
	}
	if response := h.do(request(http.MethodDelete, "/api/v1/push/subscriptions/abc", "")); response.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", response.Code)
	}
}

func TestPushSubscribeRefusesBadSubscriptions(t *testing.T) {
	h := newPushHarness(t, true)
	for name, body := range map[string]string{
		"private address": subscriptionJSON(t, "https://192.168.1.10/push"),
		"metadata":        subscriptionJSON(t, "https://169.254.169.254/latest"),
		"plain http":      subscriptionJSON(t, "http://push.example.com/a"),
		"loopback http":   subscriptionJSON(t, "http://127.0.0.1:9/a"), // only --demo accepts this
		"no keys":         `{"endpoint":"https://push.example.com/a"}`,
		"not json":        `endpoint=https://push.example.com/a`,
	} {
		response := h.do(request(http.MethodPost, "/api/v1/push/subscriptions", body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, response.Code, response.Body)
		}
	}
}

func TestPushSubscribeCap(t *testing.T) {
	h := newPushHarness(t, true)
	for i := range push.MaxSubscriptions {
		if response := h.do(request(http.MethodPost, "/api/v1/push/subscriptions", subscriptionJSON(t, fmt.Sprintf("https://push.example.com/%d", i)))); response.Code != http.StatusCreated {
			t.Fatalf("device %d: %d", i, response.Code)
		}
	}
	response := h.do(request(http.MethodPost, "/api/v1/push/subscriptions", subscriptionJSON(t, "https://push.example.com/extra")))
	if response.Code != http.StatusConflict || !strings.Contains(decodeError(t, response).Message, "remove one") {
		t.Fatalf("past the cap: %d %s", response.Code, response.Body)
	}
}

func TestPushSettingsUpdate(t *testing.T) {
	h := newPushHarness(t, true)
	response := h.do(request(http.MethodPut, "/api/v1/push/settings", `{"kinds":{"done":false}}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"done":false`) || !strings.Contains(response.Body.String(), `"question":true`) {
		t.Fatalf("update: %d %s", response.Code, response.Body)
	}
	if response := h.do(request(http.MethodPut, "/api/v1/push/settings", `{"kinds":{"everything":true}}`)); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind: %d", response.Code)
	}
}

func TestPushTestIsRateLimitedAndChecksTheEndpoint(t *testing.T) {
	h := newPushHarness(t, true)
	if response := h.do(request(http.MethodPost, "/api/v1/push/test", `{"endpoint":"https://push.example.com/not-subscribed"}`)); response.Code != http.StatusNotFound {
		t.Fatalf("unknown endpoint: %d %s", response.Code, response.Body)
	}
	for i := 1; i < 5; i++ { // the 404 above used one of the five
		if response := h.do(request(http.MethodPost, "/api/v1/push/test", "")); response.Code != http.StatusOK {
			t.Fatalf("test %d: %d %s", i, response.Code, response.Body)
		}
	}
	response := h.do(request(http.MethodPost, "/api/v1/push/test", ""))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("sixth test: %d", response.Code)
	}
}

func TestPushRoutesWithoutTheServiceAre503(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	if response := h.do(request(http.MethodGet, "/api/v1/push/key", "")); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", response.Code)
	}
}

func TestServiceWorkerIsServedFromTheRootUncached(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	response := h.do(request(http.MethodGet, "/sw.js", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != cacheRevalidate {
		t.Fatalf("Cache-Control %q: a cached service worker would pin an old version", got)
	}
	if got := response.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Fatalf("Content-Type %q", got)
	}
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "worker-src 'self'") {
		t.Fatalf("CSP %q", csp)
	}
	// R7-S14: the directives that do not fall back to default-src are named.
	for _, directive := range []string{"base-uri 'self'", "form-action 'self'", "object-src 'none'"} {
		if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, directive) {
			t.Fatalf("CSP %q lacks %s", csp, directive)
		}
	}
}
