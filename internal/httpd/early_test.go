package httpd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/config"
)

// TestEarlyListenersAnswerWhileStarting: the port serve binds before its slow start answers at
// once with 503 and Retry-After (nothing waits on a connection nobody answers), and the real
// handler once Run takes it over.
func TestEarlyListenersAnswerWhileStarting(t *testing.T) {
	cfg := config.Config{Listeners: []config.Listener{{Addr: netip.MustParseAddrPort("127.0.0.1:0"), Source: "test"}}}
	early, err := ServeEarly(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer early.Close()
	base := "http://" + early.listeners[0].Addr().String()
	client := &http.Client{Timeout: 3 * time.Second}

	for _, path := range []string{"/api/v1/auth/me", "/api/v1/events", "/jobs"} {
		response, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("%s while starting: %v (a request must be answered, not held)", path, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") == "" {
			t.Fatalf("%s while starting: %d %q", path, response.StatusCode, response.Header.Get("Retry-After"))
		}
		if strings.HasPrefix(path, "/api/") && !strings.Contains(string(body), "STARTING") {
			t.Fatalf("%s body %s", path, body)
		}
	}

	var ready http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ready") })
	early.handler.Store(&ready)
	response, err := client.Get(base + "/api/v1/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ready" {
		t.Fatalf("after the hand-over: %d %q", response.StatusCode, body)
	}
}
