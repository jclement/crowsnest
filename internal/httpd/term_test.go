// Tests for the terminal WebSockets: who may open one (auth, origin, a real pane), the protocol
// (nothing starts before the first resize; bytes both ways; the exit message), and teardown
// (browser leaves, process ends, session revoked, browser too slow). The bridge is a fake; the
// real bridges have their own tests in internal/term.

package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/term"
)

// ---- A fake bridge -------------------------------------------------------------------

type fakeBridge struct {
	mu      sync.Mutex
	size    [2]int
	resizes [][2]int
	input   bytes.Buffer
	reason  string

	started   chan struct{}
	gotInput  chan struct{} // capacity 1: "input arrived"
	closed    chan struct{}
	out       chan []byte
	closeOnce sync.Once
	endOnce   sync.Once
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{
		started: make(chan struct{}), gotInput: make(chan struct{}, 1), closed: make(chan struct{}),
		out: make(chan []byte, 4),
	}
}

func (b *fakeBridge) Start(cols, rows int) error {
	b.mu.Lock()
	b.size = [2]int{cols, rows}
	b.mu.Unlock()
	close(b.started)
	return nil
}

func (b *fakeBridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.input.Write(p)
	b.mu.Unlock()
	select {
	case b.gotInput <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (b *fakeBridge) Resize(cols, rows int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resizes = append(b.resizes, [2]int{cols, rows})
	return nil
}

func (b *fakeBridge) Output() <-chan []byte { return b.out }

func (b *fakeBridge) Reason() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reason
}

// emit sends output unless the bridge was closed.
func (b *fakeBridge) emit(p []byte) bool {
	select {
	case b.out <- p:
		return true
	case <-b.closed:
		return false
	}
}

// end is the process ending by itself.
func (b *fakeBridge) end(reason string) {
	b.mu.Lock()
	b.reason = reason
	b.mu.Unlock()
	b.endOnce.Do(func() { close(b.out) })
}

func (b *fakeBridge) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func (b *fakeBridge) typed() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.input.String()
}

// fakeLauncher hands out one fake bridge per call and records what was asked for.
type fakeLauncher struct {
	mu      sync.Mutex
	asked   []string
	bridges chan *fakeBridge
}

func newFakeLauncher() *fakeLauncher { return &fakeLauncher{bridges: make(chan *fakeBridge, 8)} }

func (l *fakeLauncher) TUI() term.Bridge { return l.make("tui") }

func (l *fakeLauncher) Pane(paneID string) term.Bridge { return l.make("pane " + paneID) }

func (l *fakeLauncher) make(what string) term.Bridge {
	l.mu.Lock()
	l.asked = append(l.asked, what)
	l.mu.Unlock()
	bridge := newFakeBridge()
	l.bridges <- bridge
	return bridge
}

func (l *fakeLauncher) calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.asked...)
}

// next returns the bridge the server built for the latest socket.
func (l *fakeLauncher) next(t *testing.T) *fakeBridge {
	t.Helper()
	select {
	case bridge := <-l.bridges:
		return bridge
	case <-time.After(2 * time.Second):
		t.Fatal("the server built no bridge")
		return nil
	}
}

// ---- Harness -------------------------------------------------------------------------

// termHarness serves a harness over a real listener, with a fake herdr holding one pane (w1:p1).
func termHarness(t *testing.T, opts harnessOptions) (*harness, *fakeLauncher, *httptest.Server) {
	t.Helper()
	launcher := newFakeLauncher()
	f := fake.New()
	f.AddWorkspace("crowsnest", "/src")
	opts.herdr, opts.fleet, opts.terminals = f, fleet.New(fleet.Options{Client: f, Session: "crowsnest-test"}), launcher
	h := newHarness(t, opts)
	server := httptest.NewServer(h.server.Handler())
	t.Cleanup(server.Close)
	return h, launcher, server
}

type dialOptions struct {
	origin string // "" means the server's own origin; "-" means no Origin header
	cookie *http.Cookie
}

func dialTerm(t *testing.T, server *httptest.Server, path string, opts dialOptions) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	switch opts.origin {
	case "":
		header.Set("Origin", server.URL)
	case "-":
	default:
		header.Set("Origin", opts.origin)
	}
	if opts.cookie != nil {
		header.Set("Cookie", opts.cookie.Name+"="+opts.cookie.Value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+path, &websocket.DialOptions{HTTPHeader: header})
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, response, err
}

func mustDial(t *testing.T, server *httptest.Server, path string, opts dialOptions) *websocket.Conn {
	t.Helper()
	conn, response, err := dialTerm(t, server, path, opts)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", path, err, status)
	}
	return conn
}

func sendResize(t *testing.T, conn *websocket.Conn, cols, rows int) {
	t.Helper()
	payload, _ := json.Marshal(ControlMessage{Type: "resize", Cols: cols, Rows: rows})
	if err := conn.Write(context.Background(), websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return conn.Read(ctx)
}

func waitClosedChan(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// ---- Who may open a terminal ---------------------------------------------------------

func TestTermRejectsUnauthenticated(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{})
	for _, path := range []string{"/api/v1/term/tui", "/api/v1/term/pane/w1:p1"} {
		_, response, err := dialTerm(t, server, path, dialOptions{})
		if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a session: err %v, response %+v; want 401", path, err, response)
		}
	}
	if calls := launcher.calls(); len(calls) != 0 {
		t.Errorf("bridges built for unauthenticated sockets: %v", calls)
	}
}

func TestTermAcceptsAValidSession(t *testing.T) {
	h, launcher, server := termHarness(t, harnessOptions{})
	conn := mustDial(t, server, "/api/v1/term/tui", dialOptions{cookie: h.loginAt(server.URL)})
	sendResize(t, conn, 80, 24)
	waitClosedChan(t, launcher.next(t).started, "start")
}

func TestTermRejectsCrossOriginAndMissingOrigin(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{noAuth: true})
	for _, origin := range []string{"http://evil.example", "-", "null", "http://localhost:1"} {
		_, response, err := dialTerm(t, server, "/api/v1/term/tui", dialOptions{origin: origin})
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q: err %v, response %+v; want 403", origin, err, response)
		}
	}
	if calls := launcher.calls(); len(calls) != 0 {
		t.Errorf("bridges built for rejected origins: %v", calls)
	}
}

func TestTermPaneMustBeInTheFleet(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{noAuth: true})
	for path, want := range map[string]int{
		"/api/v1/term/pane/w9:p9":       http.StatusNotFound,
		"/api/v1/term/pane/w1:p1;rm%20": http.StatusBadRequest,
		"/api/v1/term/pane/--help":      http.StatusBadRequest,
	} {
		_, response, err := dialTerm(t, server, path, dialOptions{})
		if err == nil || response == nil || response.StatusCode != want {
			t.Errorf("%s: err %v, response %+v; want %d", path, err, response, want)
		}
	}
	if calls := launcher.calls(); len(calls) != 0 {
		t.Errorf("bridges built for bad panes: %v", calls)
	}
	mustDial(t, server, "/api/v1/term/pane/w1:p1", dialOptions{})
	launcher.next(t)
	if calls := launcher.calls(); len(calls) != 1 || calls[0] != "pane w1:p1" {
		t.Errorf("calls = %v, want one pane bridge for w1:p1", calls)
	}
}

func TestTermUnavailableWithoutALauncher(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	r := request(http.MethodGet, "/api/v1/term/tui", "")
	r.Header.Set("Origin", testOrigin)
	if response := h.do(r); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("tui without terminals: %d", response.Code)
	}
}

// ---- Protocol ------------------------------------------------------------------------

func TestTermStartsOnFirstResizeAndCarriesBytes(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{noAuth: true})
	conn := mustDial(t, server, "/api/v1/term/pane/w1:p1", dialOptions{})
	bridge := launcher.next(t)
	ctx := context.Background()

	// Input before the first resize has nowhere to go, and nothing has started.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("early")); err != nil {
		t.Fatal(err)
	}
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":0,"rows":24}`)) // invalid: ignored
	_ = conn.Write(ctx, websocket.MessageText, []byte(`not json`))                             // ignored
	select {
	case <-bridge.started:
		t.Fatal("the bridge started before a valid resize")
	case <-time.After(150 * time.Millisecond):
	}

	sendResize(t, conn, 100, 30)
	waitClosedChan(t, bridge.started, "start")
	bridge.mu.Lock()
	size := bridge.size
	bridge.mu.Unlock()
	if size != [2]int{100, 30} {
		t.Errorf("started at %v, want 100x30", size)
	}

	bridge.emit([]byte("hello \x1b[1mworld\x1b[0m"))
	kind, data, err := readFrame(t, conn)
	if err != nil || kind != websocket.MessageBinary || string(data) != "hello \x1b[1mworld\x1b[0m" {
		t.Fatalf("output frame = %v %q %v", kind, data, err)
	}

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	waitClosedChan(t, bridge.gotInput, "input")
	if typed := bridge.typed(); typed != "ls\r" {
		t.Errorf("typed %q, want only what came after the resize", typed)
	}

	sendResize(t, conn, 120, 40)
	deadline := time.Now().Add(2 * time.Second)
	for {
		bridge.mu.Lock()
		resizes := append([][2]int(nil), bridge.resizes...)
		bridge.mu.Unlock()
		if len(resizes) == 1 && resizes[0] == [2]int{120, 40} {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resizes = %v, want [120x40]", resizes)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTermExitMessageThenNormalClose(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{noAuth: true})
	conn := mustDial(t, server, "/api/v1/term/tui", dialOptions{})
	bridge := launcher.next(t)
	sendResize(t, conn, 80, 24)
	waitClosedChan(t, bridge.started, "start")
	bridge.emit([]byte("bye"))
	bridge.end("terminal attach taken over")

	if _, data, err := readFrame(t, conn); err != nil || string(data) != "bye" {
		t.Fatalf("last output = %q, %v", data, err)
	}
	kind, data, err := readFrame(t, conn)
	if err != nil || kind != websocket.MessageText {
		t.Fatalf("exit frame = %v %q %v", kind, data, err)
	}
	var exit ControlMessage
	if json.Unmarshal(data, &exit) != nil || exit.Type != "exit" || exit.Reason != "terminal attach taken over" {
		t.Errorf("exit message = %s", data)
	}
	if _, _, err := readFrame(t, conn); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Errorf("after exit: %v, want a normal close", err)
	}
	waitClosedChan(t, bridge.closed, "bridge close")
}

func TestTermBrowserLeavingClosesTheBridge(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{noAuth: true})
	conn := mustDial(t, server, "/api/v1/term/pane/w1:p1", dialOptions{})
	bridge := launcher.next(t)
	sendResize(t, conn, 80, 24)
	waitClosedChan(t, bridge.started, "start")
	_ = conn.Close(websocket.StatusNormalClosure, "tab closed")
	waitClosedChan(t, bridge.closed, "bridge close after the browser left")
}

// TestTermSocketsAreCapped is R1 I5: past the cap a socket is closed with a policy code (the
// browser does not reconnect by itself) and a reason saying what to do; closing one frees a slot.
func TestTermSocketsAreCapped(t *testing.T) {
	_, launcher, server := termHarness(t, harnessOptions{noAuth: true, maxTerminals: 2})
	first := mustDial(t, server, "/api/v1/term/tui", dialOptions{})
	second := mustDial(t, server, "/api/v1/term/tui", dialOptions{})
	launcher.next(t)
	launcher.next(t)
	third := mustDial(t, server, "/api/v1/term/tui", dialOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := third.Read(ctx)
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), "too many terminals open (2)") {
		t.Fatalf("third socket: %v (status %d)", err, status)
	}
	_ = first.Close(websocket.StatusNormalClosure, "done")
	waitFor(t, func() bool {
		conn, _, err := dialTerm(t, server, "/api/v1/term/tui", dialOptions{})
		if err != nil {
			return false
		}
		readCtx, cancelRead := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancelRead()
		_, _, err = conn.Read(readCtx)
		return websocket.CloseStatus(err) != websocket.StatusPolicyViolation
	})
	_ = second
}

func TestTermLogoutClosesOpenTerminals(t *testing.T) {
	h, launcher, server := termHarness(t, harnessOptions{heartbeat: 50 * time.Millisecond})
	cookie := h.loginAt(server.URL)
	conn := mustDial(t, server, "/api/v1/term/tui", dialOptions{cookie: cookie})
	bridge := launcher.next(t)
	sendResize(t, conn, 80, 24)
	waitClosedChan(t, bridge.started, "start")

	if code := h.do(withCookie(request(http.MethodPost, "/api/v1/auth/logout", ""), cookie)).Code; code != http.StatusOK {
		t.Fatalf("logout: %d", code)
	}
	waitClosedChan(t, bridge.closed, "bridge close after logout")
	_, _, err := readFrame(t, conn)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("socket after logout: %v, want policy-violation close", err)
	}
}

func TestTermSlowBrowserIsDropped(t *testing.T) {
	h, launcher, server := termHarness(t, harnessOptions{noAuth: true, termQueue: 64 << 10})
	conn := mustDial(t, server, "/api/v1/term/tui", dialOptions{})
	bridge := launcher.next(t)
	sendResize(t, conn, 80, 24)
	waitClosedChan(t, bridge.started, "start")

	// The browser reads nothing. Once the socket's kernel buffers are full, writes stall, the
	// queue reaches its bound, and the server must give up on this browser rather than buffer.
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	go func() {
		for range 4096 { // 128 MB at most; far more than any socket buffer
			if !bridge.emit(chunk) {
				return
			}
		}
	}()
	waitClosedChan(t, bridge.closed, "bridge close for a slow browser")
	if !strings.Contains(h.logs.String(), "not keeping up") {
		t.Errorf("log does not say why the browser was dropped:\n%s", h.logs.String())
	}
	_ = conn.CloseNow()
}

func TestTermDemoLauncherEndToEnd(t *testing.T) {
	f := fake.New()
	f.AddWorkspace("demo", "/src")
	h := newHarness(t, harnessOptions{noAuth: true, herdr: f, fleet: fleet.New(fleet.Options{Client: f, Session: "demo"}), terminals: term.DemoLauncher{}})
	server := httptest.NewServer(h.server.Handler())
	t.Cleanup(server.Close)
	conn := mustDial(t, server, "/api/v1/term/pane/w1:p1", dialOptions{})
	sendResize(t, conn, 80, 24)
	var seen bytes.Buffer
	for !strings.Contains(seen.String(), "\x1b[2m$\x1b[0m ") {
		_, data, err := readFrame(t, conn)
		if err != nil {
			t.Fatalf("reading the banner: %v (saw %q)", err, seen.String())
		}
		seen.Write(data)
	}
	_ = conn.Write(context.Background(), websocket.MessageBinary, []byte("crow\r"))
	for !strings.Contains(seen.String(), "you typed") {
		_, data, err := readFrame(t, conn)
		if err != nil {
			t.Fatalf("reading the echo: %v (saw %q)", err, seen.String())
		}
		seen.Write(data)
	}
}

// ---- Units ---------------------------------------------------------------------------

func TestParseControl(t *testing.T) {
	if message, err := parseControl([]byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil || message.Cols != 80 || message.Rows != 24 {
		t.Errorf("resize = %+v, %v", message, err)
	}
	for _, bad := range []string{`{`, `{"type":"resize","cols":0,"rows":24}`, `{"type":"resize","cols":80,"rows":9999}`, `{"type":"resize"}`} {
		if _, err := parseControl([]byte(bad)); err == nil || errors.Is(err, errUnknownControl) {
			t.Errorf("%s: err = %v, want a validation error", bad, err)
		}
	}
	if _, err := parseControl([]byte(`{"type":"scroll","lines":3}`)); !errors.Is(err, errUnknownControl) {
		t.Errorf("unknown type: err = %v, want errUnknownControl", err)
	}
}

func TestTermQueueBound(t *testing.T) {
	queue := newTermQueue(10)
	if !queue.push(websocket.MessageBinary, []byte("12345")) || !queue.push(websocket.MessageBinary, []byte("67890")) {
		t.Fatal("pushes within the bound were refused")
	}
	if queue.push(websocket.MessageBinary, []byte("x")) {
		t.Fatal("a push past the bound was accepted")
	}
	queue.pushControl([]byte(`{"type":"exit"}`)) // control messages ignore the bound
	ctx := context.Background()
	if frame, ok := queue.next(ctx); !ok || string(frame.data) != "12345" {
		t.Fatalf("first = %q %v", frame.data, ok)
	}
	if !queue.push(websocket.MessageBinary, []byte("abcde")) {
		t.Fatal("draining should make room again")
	}
	queue.finish()
	if queue.push(websocket.MessageBinary, []byte("z")) {
		t.Fatal("a push after finish was accepted")
	}
	var rest []string
	for {
		frame, ok := queue.next(ctx)
		if !ok {
			break
		}
		rest = append(rest, string(frame.data))
	}
	if strings.Join(rest, ",") != `67890,{"type":"exit"},abcde` {
		t.Errorf("drained %v", rest)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := newTermQueue(10).next(cancelled); ok {
		t.Error("next on a cancelled context returned a frame")
	}
}

var _ herdr.Client = (*fake.Fake)(nil)
