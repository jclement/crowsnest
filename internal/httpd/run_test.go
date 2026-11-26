// Tests for Run: binding, optional listeners, and graceful shutdown on context cancel.

package httpd

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

var tlsStateForTest = tls.ConnectionState{}

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func runServer(t *testing.T, listeners []config.Listener) (*Server, error) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, _ := auth.New(context.Background(), auth.Options{NoAuth: true}, db)
	cfg := testConfig(true)
	cfg.Listeners = listeners
	return New(Deps{Config: cfg, Auth: service, Hub: hub.New(), Logger: slog.New(slog.DiscardHandler), Version: "v1", Web: testWeb})
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	port := freePort(t)
	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)
	// The second listener is optional and unbindable (TEST-NET address): it must be skipped.
	unbindable := config.Listener{Addr: netip.MustParseAddrPort("192.0.2.1:1"), Optional: true, Source: "test"}
	server, err := runServer(t, []config.Listener{{Addr: addr, Source: "test"}, unbindable})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	url := "http://" + addr.String() + "/api/v1/health"
	var response *http.Response
	waitFor(t, func() bool {
		response, err = http.Get(url)
		return err == nil
	})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health over a real socket: %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunFailsWhenARequiredAddressIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	addr := netip.MustParseAddrPort(taken.Addr().String())
	server, err := runServer(t, []config.Listener{{Addr: addr, Source: "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	err = server.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "from --bind 127.0.0.1") {
		t.Fatalf("expected a bind error naming the bind value, got %v", err)
	}
}

func TestRunFailsWithNothingBound(t *testing.T) {
	server, err := runServer(t, []config.Listener{{Addr: netip.MustParseAddrPort("192.0.2.1:1"), Optional: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("expected an error when no listener could be bound")
	}
}
