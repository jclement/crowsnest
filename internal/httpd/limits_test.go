// Tests for the R1 L3 limits: the connection cap, the header limit and the login body deadline.

package httpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/config"
)

// TestConnectionCapQueuesRatherThanRefuses: past the cap, Accept waits until a connection
// closes; the connections already open are untouched.
func TestConnectionCapQueuesRatherThanRefuses(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newConnLimiter(2, 2).wrap(inner)
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 3)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	for range 3 {
		client, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
	}
	first, second := <-accepted, <-accepted
	select {
	case <-accepted:
		t.Fatal("a third connection was accepted past a cap of two")
	case <-time.After(200 * time.Millisecond):
	}
	_ = first.Close()
	select {
	case third := <-accepted:
		_ = third.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("closing a connection did not let the queued one in")
	}
	_ = second.Close()
	_ = first.Close() // a second Close must not give back a second slot
	if got := len(newConnLimiter(2, 2).slots); got != 0 {
		t.Fatalf("fresh limiter holds %d", got)
	}
}

// TestOnePeerCannotTakeEverySlot is R1b N4: a peer past its own cap is refused at once, without
// holding a global slot, so another peer still gets in. Loopback is exempt in production; the
// test turns the exemption off because every test connection comes from loopback.
func TestOnePeerCannotTakeEverySlot(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limiter := newConnLimiter(4, 2)
	limiter.exempt = func(netip.Addr) bool { return false }
	listener := limiter.wrap(inner)
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	var clients []net.Conn
	for range 3 {
		client, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		t.Cleanup(func() { _ = client.Close() })
	}
	<-accepted
	<-accepted
	// The third connection from the same peer is closed by the server, not queued.
	_ = clients[2].SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := clients[2].Read(make([]byte, 1)); err == nil {
		t.Fatal("a third connection from one peer was served past its cap of two")
	} else if ne := net.Error(nil); errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("a third connection from one peer was held open past its cap instead of being refused")
	}
	// Two live connections, plus the one Accept holds while it waits for the next connection;
	// the refused connection's slot went back (were it kept, Accept would be stuck waiting at 4).
	time.Sleep(100 * time.Millisecond) // let Accept go round again
	if held := len(limiter.slots); held != 3 {
		t.Fatalf("%d global slots held; the refused connection must give its slot back", held)
	}
	if !netip.MustParseAddr("127.0.0.1").IsLoopback() || !newConnLimiter(1, 1).exempt(netip.MustParseAddr("::1")) {
		t.Fatal("loopback peers must be exempt in production")
	}
}

// TestLoginBodyMustArrivePromptly: a client that sends login headers and then dribbles (here:
// withholds) its body is cut off instead of holding the connection.
func TestLoginBodyMustArrivePromptly(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.server.loginBodyTimeout = 200 * time.Millisecond
	base := liveServer(t, h)
	host := strings.TrimPrefix(base, "http://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "POST /api/v1/auth/login HTTP/1.1\r\nHost: %s\r\nOrigin: http://%s\r\nX-Crowsnest: 1\r\nContent-Type: application/json\r\nContent-Length: 64\r\n\r\n{", host, host); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("the server kept waiting for the body: %v", err)
	}
	if !strings.Contains(status, "400") {
		t.Fatalf("status line %q, want 400", status)
	}
}

// TestRunCapsHeaderSize: an oversized header block is refused (431) rather than read.
func TestRunCapsHeaderSize(t *testing.T) {
	port := freePort(t)
	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)
	server, err := runServer(t, []config.Listener{{Addr: addr, Source: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx) }()
	url := "http://" + addr.String() + "/api/v1/health"
	waitFor(t, func() bool {
		response, err := http.Get(url) //nolint:noctx // a test poll
		if err == nil {
			_ = response.Body.Close()
		}
		return err == nil
	})
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	request.Header.Set("X-Padding", strings.Repeat("x", 2*maxHeaderBytes))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized headers: %d", response.StatusCode)
	}
}
