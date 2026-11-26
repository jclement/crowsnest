// The terminal WebSockets (DESIGN.md section 10): GET /term/tui (the herdr TUI in a PTY) and GET
// /term/pane/{pane_id} (one pane's control stream). A web terminal is a shell as the user, so
// everything about getting one fails closed:
//
//   - the session cookie is checked before the upgrade (requireAuth; --no-auth skips it), and the
//     middleware's Origin check runs on every upgrade;
//   - a pane id must name a pane in the session's fleet before anything is spawned, so no
//     arbitrary string reaches a child's argv;
//   - the cookie is re-checked on every keepalive, so a logout or password change closes open
//     terminals within one heartbeat, as it ends SSE streams.
//
// The protocol: binary frames are terminal bytes in both directions; text frames are JSON control
// messages, `{"type":"resize","cols":N,"rows":N}` from the browser and `{"type":"exit","reason":…}`
// from the server. Nothing starts until the first resize, so the process is born at the size the
// browser is showing. Output reaches the browser through a byte-bounded queue: a browser that
// stops reading (a phone in a pocket) gets its connection dropped past the bound instead of
// growing the server's memory, and reconnects to a fresh redraw when it comes back.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jclement/crowsnest/internal/term"
)

const (
	// DefaultTermQueueBytes bounds output waiting for one browser. A full redraw of a large
	// screen is tens of kilobytes; a megabyte means the browser has stopped reading.
	DefaultTermQueueBytes = 1 << 20
	// termReadLimit caps one message from the browser: a resize, keystrokes, or a paste.
	termReadLimit = 256 << 10
	// termWriteTimeout bounds one write to the browser; a stalled socket is a dead one.
	termWriteTimeout = 10 * time.Second
	// termPingTimeout is how long a keepalive ping may wait for its pong.
	termPingTimeout = 10 * time.Second
	// termStartTimeout is how long a socket may stay open without sending its first resize.
	termStartTimeout = 30 * time.Second
	// DefaultMaxTerminals caps open terminal sockets. Each spawns a herdr process (a TUI in a
	// PTY or a pane controller); a person needs a few, and a runaway reconnect loop in a
	// browser should hit a wall rather than fill the process table (R1 I5).
	DefaultMaxTerminals = 8
)

// paneIDPattern is herdr's pane id shape ("w1:p2"). The fleet check is the real guard; this only
// keeps junk out of logs and lookups.
var paneIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,63}$`)

// ---- Control messages ----------------------------------------------------------------

// ControlMessage is a text frame on a terminal socket.
type ControlMessage struct {
	Type   string `json:"type"`             // "resize" (browser) or "exit" (server)
	Cols   int    `json:"cols,omitempty"`   // resize
	Rows   int    `json:"rows,omitempty"`   // resize
	Reason string `json:"reason,omitempty"` // exit: why the terminal ended
}

// errUnknownControl marks a well-formed control message of a type this server does not know.
// It is ignored rather than fatal, so a newer web app can talk to an older server.
var errUnknownControl = errors.New("unknown control message")

// parseControl decodes a browser control message. Only resize is accepted, with a valid size.
func parseControl(data []byte) (ControlMessage, error) {
	var message ControlMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return ControlMessage{}, fmt.Errorf("control message is not JSON: %w", err)
	}
	if message.Type != "resize" {
		return ControlMessage{}, fmt.Errorf("%w %q", errUnknownControl, message.Type)
	}
	if err := term.ValidSize(message.Cols, message.Rows); err != nil {
		return ControlMessage{}, err
	}
	return message, nil
}

// ---- Handlers ------------------------------------------------------------------------

func (s *Server) handleTermTUI(w http.ResponseWriter, r *http.Request) {
	if s.deps.Terminals == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Terminals are not available on this server.")
		return
	}
	s.serveTerminal(w, r, "tui", s.deps.Terminals.TUI())
}

func (s *Server) handleTermPane(w http.ResponseWriter, r *http.Request) {
	if s.deps.Terminals == nil || s.deps.Fleet == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Terminals are not available on this server.")
		return
	}
	paneID := r.PathValue("pane_id")
	if !paneIDPattern.MatchString(paneID) {
		writeError(w, http.StatusBadRequest, codeValidation, "A pane id looks like w1:p2.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), herdrCallTimeout)
	defer cancel()
	current, err := s.deps.Fleet.Fleet(ctx)
	if err != nil {
		writeHerdrUnavailable(w)
		return
	}
	if !current.HasPane(paneID) {
		writeError(w, http.StatusNotFound, codeNotFound, "No pane called "+paneID+" in this session.")
		return
	}
	s.serveTerminal(w, r, "pane "+paneID, s.deps.Terminals.Pane(paneID))
}

// serveTerminal upgrades the request and runs one terminal session until either side ends it.
func (s *Server) serveTerminal(w http.ResponseWriter, r *http.Request, what string, bridge term.Bridge) {
	defer func() { _ = bridge.Close() }()
	conn, err := websocket.Accept(w, r, nil) // the Origin check already ran in the middleware
	if err != nil {
		s.deps.Logger.Warn("terminal upgrade failed", "terminal", what, "err", err)
		return
	}
	select {
	case s.terminalSlots <- struct{}{}:
		defer func() { <-s.terminalSlots }()
	default:
		// A policy close: the browser shows the reason and does not reconnect by itself.
		limit := cap(s.terminalSlots)
		s.deps.Logger.Warn("terminal refused: too many open", "terminal", what, "limit", limit)
		_ = conn.Close(websocket.StatusPolicyViolation, fmt.Sprintf("too many terminals open (%d); close one and reconnect", limit))
		return
	}
	conn.SetReadLimit(termReadLimit)
	session := &termSession{
		server: s, request: r, what: what, conn: conn, bridge: bridge,
		queue:   newTermQueue(s.termQueueBytes()),
		started: make(chan struct{}),
	}
	session.run()
}

func (s *Server) termQueueBytes() int {
	if s.deps.TermQueueBytes > 0 {
		return s.deps.TermQueueBytes
	}
	return DefaultTermQueueBytes
}

// ---- One terminal session ------------------------------------------------------------

// termSession is one open terminal socket and its bridge.
type termSession struct {
	server  *Server
	request *http.Request
	what    string
	conn    *websocket.Conn
	bridge  term.Bridge
	queue   *termQueue

	started   chan struct{} // closed by the first resize, once the bridge is running
	startOnce sync.Once

	closeOnce   sync.Once
	closeCode   websocket.StatusCode // why the socket is closing; set once by close
	closeReason string
}

// run starts the socket's goroutines and returns when the session is over. The close handshake
// happens here, after the writer has stopped and before the context is cancelled: cancelling
// first would make the pending Read drop the TCP connection, and the browser would see a bare
// EOF instead of the close code that tells it what happened.
func (t *termSession) run() {
	ctx, cancel := context.WithCancel(t.request.Context())
	defer cancel()
	go t.readBrowser(ctx)
	go t.keepAlive(ctx)
	go t.awaitStart(ctx)
	t.writeBrowser(ctx) // returns once close was called and the queue is drained, or a write fails
	t.close(websocket.StatusNormalClosure, "terminal ended")
	_ = t.conn.Close(t.closeCode, t.closeReason)
}

// close decides how the socket ends (the first caller wins), stops the bridge, and lets the
// writer drain and return so run can do the close handshake.
func (t *termSession) close(code websocket.StatusCode, reason string) {
	t.closeOnce.Do(func() {
		t.server.deps.Logger.Info("terminal closed", "terminal", t.what, "code", int(code), "reason", reason)
		t.closeCode, t.closeReason = code, reason
		t.queue.finish()
		_ = t.bridge.Close()
	})
}

// readBrowser handles frames from the browser until the socket closes.
func (t *termSession) readBrowser(ctx context.Context) {
	for {
		kind, data, err := t.conn.Read(ctx)
		if err != nil {
			t.close(websocket.StatusNormalClosure, "browser went away")
			return
		}
		if kind == websocket.MessageBinary {
			t.input(data)
			continue
		}
		message, err := parseControl(data)
		if err != nil {
			t.server.deps.Logger.Debug("ignoring terminal control message", "terminal", t.what, "err", err)
			continue
		}
		t.resize(message.Cols, message.Rows)
	}
}

// input forwards keystrokes once the process runs; anything typed before the first resize has
// nowhere to go and is dropped.
func (t *termSession) input(data []byte) {
	select {
	case <-t.started:
	default:
		return
	}
	if _, err := t.bridge.Write(data); err != nil {
		t.server.deps.Logger.Debug("terminal input not delivered", "terminal", t.what, "err", err)
	}
}

// resize starts the process on the first resize and resizes it afterwards.
func (t *termSession) resize(cols, rows int) {
	first := false
	t.startOnce.Do(func() {
		first = true
		if err := t.bridge.Start(cols, rows); err != nil {
			t.server.deps.Logger.Warn("terminal did not start", "terminal", t.what, "err", err)
			t.exit("could not start: " + err.Error())
			return
		}
		t.server.deps.Logger.Info("terminal started", "terminal", t.what, "cols", cols, "rows", rows)
		close(t.started)
		go t.pumpOutput()
	})
	if !first {
		if err := t.bridge.Resize(cols, rows); err != nil {
			t.server.deps.Logger.Debug("terminal resize failed", "terminal", t.what, "err", err)
		}
	}
}

// pumpOutput moves bridge output into the queue; when the process ends it sends the exit
// message and lets the writer close the socket after it.
func (t *termSession) pumpOutput() {
	for chunk := range t.bridge.Output() {
		if !t.queue.push(websocket.MessageBinary, chunk) {
			if !t.queue.finished() {
				t.close(websocket.StatusTryAgainLater, "the browser is not keeping up with the terminal")
			}
			return
		}
	}
	t.exit(t.bridge.Reason())
}

// exit tells the browser the terminal ended and why, then closes normally once that is sent.
func (t *termSession) exit(reason string) {
	payload, _ := json.Marshal(ControlMessage{Type: "exit", Reason: reason})
	t.queue.pushControl(payload)
	t.queue.finish()
}

// writeBrowser sends queued frames until the queue is finished and drained.
func (t *termSession) writeBrowser(ctx context.Context) {
	for {
		frame, ok := t.queue.next(ctx)
		if !ok {
			break
		}
		writeCtx, cancel := context.WithTimeout(ctx, termWriteTimeout)
		err := t.conn.Write(writeCtx, frame.kind, frame.data)
		cancel()
		if err != nil {
			t.close(websocket.StatusGoingAway, "write to the browser failed")
			return
		}
	}
}

// awaitStart closes a socket that never sends its first resize.
func (t *termSession) awaitStart(ctx context.Context) {
	timer := time.NewTimer(termStartTimeout)
	defer timer.Stop()
	select {
	case <-t.started:
	case <-ctx.Done():
	case <-timer.C:
		t.close(websocket.StatusPolicyViolation, "no resize received; send one to start the terminal")
	}
}

// keepAlive pings the browser and re-checks the session every heartbeat, and closes the socket
// when the server shuts down.
func (t *termSession) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(t.server.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.server.stopping:
			t.close(websocket.StatusGoingAway, "crowsnest is shutting down")
			return
		case <-ticker.C:
			if !t.server.sessionStillValid(t.request) {
				t.close(websocket.StatusPolicyViolation, "signed out")
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, termPingTimeout)
			err := t.conn.Ping(pingCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				t.close(websocket.StatusGoingAway, "keepalive failed")
				return
			}
		}
	}
}

// ---- The bounded write queue ---------------------------------------------------------

// termFrame is one message waiting to go to the browser.
type termFrame struct {
	kind websocket.MessageType
	data []byte
}

// termQueue holds output for one browser, bounded by bytes. push refuses past the bound (the
// caller then drops the connection); control messages are tiny and always accepted, so the
// browser still learns why a terminal ended.
type termQueue struct {
	mu     sync.Mutex
	frames []termFrame
	bytes  int
	limit  int
	done   bool
	wake   chan struct{} // capacity 1: "something changed"
}

func newTermQueue(limit int) *termQueue {
	return &termQueue{limit: limit, wake: make(chan struct{}, 1)}
}

// push queues terminal output. It returns false when the queue is finished, or when this
// frame would take it past its bound.
func (q *termQueue) push(kind websocket.MessageType, data []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.done || q.bytes+len(data) > q.limit {
		return false
	}
	q.frames = append(q.frames, termFrame{kind: kind, data: data})
	q.bytes += len(data)
	q.signal()
	return true
}

// pushControl queues a control message regardless of the bound (unless finished).
func (q *termQueue) pushControl(data []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.done {
		return
	}
	q.frames = append(q.frames, termFrame{kind: websocket.MessageText, data: data})
	q.signal()
}

// finish accepts nothing more; next drains what is queued and then reports false.
func (q *termQueue) finish() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.done = true
	q.signal()
}

func (q *termQueue) finished() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.done
}

// next waits for the next frame. It returns false when the queue is finished and empty, or
// when ctx ends.
func (q *termQueue) next(ctx context.Context) (termFrame, bool) {
	for {
		q.mu.Lock()
		if len(q.frames) > 0 {
			frame := q.frames[0]
			q.frames[0] = termFrame{}
			q.frames = q.frames[1:]
			if frame.kind == websocket.MessageBinary {
				q.bytes -= len(frame.data)
			}
			q.mu.Unlock()
			return frame, true
		}
		done := q.done
		q.mu.Unlock()
		if done {
			return termFrame{}, false
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return termFrame{}, false
		}
	}
}

// signal wakes next; called with mu held.
func (q *termQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
