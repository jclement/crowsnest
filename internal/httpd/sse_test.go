// SSE tests that need a real connection: a client that stops reading (R1-C04), and an idle
// HTTP/2 stream that must outlive the write deadline (R1b-C03).

package httpd

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

// TestIdleHTTP2StreamOutlivesTheWriteDeadline is R1b-C03. Over HTTP/2 a response write deadline
// is a timer that resets the stream when it fires, even with nothing being written. The stream
// below stays idle for several write timeouts (no events, heartbeat an hour away) and must still
// deliver the next event.
func TestIdleHTTP2StreamOutlivesTheWriteDeadline(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true, heartbeat: time.Hour})
	h.server.sseWriteTimeout = 150 * time.Millisecond
	live := httptest.NewUnstartedServer(h.server.Handler())
	live.EnableHTTP2 = true
	live.StartTLS()
	t.Cleanup(live.Close)
	t.Cleanup(h.server.beginShutdown)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, live.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := live.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.ProtoMajor != 2 {
		t.Fatalf("the test server spoke %s, not HTTP/2", response.Proto)
	}
	events := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if name, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
				events <- name
			}
		}
		close(events)
	}()
	if name := <-events; name != "hello" {
		t.Fatalf("first event %q, want hello", name)
	}
	time.Sleep(6 * h.server.sseWriteTimeout) // idle well past the deadline
	h.hub.Publish(hub.Event{Type: "fleet", Data: "after the idle spell"})
	select {
	case name, ok := <-events:
		if !ok || name != "fleet" {
			t.Fatalf("after an idle spell the stream gave %q (open %v), want the fleet event", name, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the event published after an idle spell never arrived")
	}
}
