// SSE tests that need a real TCP connection: a client that stops reading (R1-C04).

package httpd

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

// TestEventsDropAClientThatStopsReading is R1-C04: a client that keeps its connection open but
// never reads fills the socket buffers, and the handler must give up on the write rather than
// sit in Flush for as long as the connection lives.
func TestEventsDropAClientThatStopsReading(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true, heartbeat: time.Hour})
	h.server.sseWriteTimeout = 200 * time.Millisecond
	base := liveServer(t, h)
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.(*net.TCPConn).SetReadBuffer(4 << 10)
	if _, err := fmt.Fprintf(conn, "GET /api/v1/events HTTP/1.1\r\nHost: %s\r\n\r\n", strings.TrimPrefix(base, "http://")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.hub.Subscribers() == 1 })

	// Far more than the socket buffers hold; the hub drops the subscriber once 64 queue up.
	payload := strings.Repeat("x", 32<<10)
	for range 400 {
		h.hub.Publish(hub.Event{Type: "fleet", Data: payload})
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.server.openStreams.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the SSE handler is still blocked writing to a client that does not read")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
