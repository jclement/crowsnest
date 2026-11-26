// SocketClient: the real Client, speaking herdr's NDJSON protocol over the session's unix socket.
//
// Framing (verified against herdr 0.9.5): one JSON object per line. A request is
// {"id","method","params"}; the answer is {"id","result":{"type",…}} or {"id","error":{"code",
// "message"}}. Each call uses its own short-lived connection (DESIGN.md section 5), so calls are
// independent, safe to make concurrently, and a restarted server needs no reconnect logic: the
// next call simply dials again. Events use one long-lived connection per subscription.

package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultCallTimeout bounds a call whose context has no deadline. herdr answers ordinary
	// calls in milliseconds; this only stops a wedged server from hanging a request forever.
	DefaultCallTimeout = 15 * time.Second
	// waitGrace is added to a call's own herdr-side timeout (agent.wait, agent.prompt with
	// wait, agent.start) so herdr's timeout error arrives before ours.
	waitGrace = 5 * time.Second
	// maxLineBytes caps one response line. A long scrollback read is the biggest answer; 32 MiB
	// is far beyond any real one and still stops a runaway peer from exhausting memory.
	maxLineBytes = 32 << 20
	// eventBuffer is how many events may queue before the reader waits for the consumer. If the
	// consumer stalls long enough, herdr itself reports events_lost, which is the right outcome.
	eventBuffer = 256
)

// SocketClient talks to one herdr server. The zero value is not usable; call NewSocketClient.
type SocketClient struct {
	path   string
	nextID atomic.Uint64
}

// NewSocketClient returns a client for the server listening on the unix socket at path.
func NewSocketClient(path string) *SocketClient {
	return &SocketClient{path: path}
}

// SocketPath is the unix socket this client dials.
func (c *SocketClient) SocketPath() string { return c.path }

// ---- Wire --------------------------------------------------------------------------

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// emptyParams is sent for methods that take none; herdr wants an object, never null.
var emptyParams = struct{}{}

// call sends one request on a fresh connection and decodes the result into out (which may be
// nil). timeout overrides DefaultCallTimeout when ctx carries no deadline.
func (c *SocketClient) call(ctx context.Context, method string, params, out any, timeout time.Duration) error {
	if _, ok := ctx.Deadline(); !ok {
		if timeout <= 0 {
			timeout = DefaultCallTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	defer func() { _ = conn.Close() }()
	stop := closeOnDone(ctx, conn)
	defer stop()

	id := strconv.FormatUint(c.nextID.Add(1), 10)
	if err := writeLine(conn, request{ID: id, Method: method, Params: params}); err != nil {
		return wrapIO(ctx, method, "sending request", err)
	}
	line, err := readLine(bufio.NewReader(conn))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoResponse, wrapIO(ctx, method, "reading response", err))
	}
	return decodeResponse(method, id, line, out)
}

func (c *SocketClient) dial(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.path)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", c.path, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	return conn, nil
}

// watcherStarting runs as the closeOnDone watcher begins. Tests replace it to delay the
// watcher; it is a no-op otherwise.
var watcherStarting = func() {}

// closeOnDone closes conn when ctx ends, unblocking any read or write. The returned func stops
// the watcher and waits for it to exit, so once it returns the watcher can no longer close
// conn. That matters for Subscribe, which hands the connection on after the acknowledgement and
// then cancels its acknowledgement context: a watcher still running at that moment would see
// both cases ready and could close the live subscription (R1-C03).
func closeOnDone(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		watcherStarting()
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

func writeLine(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(append(payload, '\n'))
	return err
}

// readLine reads one newline-terminated line, refusing lines over maxLineBytes.
func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLineBytes {
			return nil, fmt.Errorf("response line exceeds %d bytes", maxLineBytes)
		}
		switch {
		case err == nil:
			return bytes.TrimRight(line, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(bytes.TrimSpace(line)) > 0:
			return bytes.TrimSpace(line), nil // a final line without a newline still counts
		default:
			return nil, err
		}
	}
}

// wrapIO prefers the context's reason (timeout, cancellation) over the raw I/O error it caused.
func wrapIO(ctx context.Context, method, doing string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("herdr %s: %s: %w", method, doing, ctxErr)
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		// The connection deadline mirrors the context deadline and can fire a hair before it.
		return fmt.Errorf("herdr %s: %s: %w", method, doing, context.DeadlineExceeded)
	}
	return fmt.Errorf("herdr %s: %s: %w", method, doing, err)
}

func decodeResponse(method, id string, line []byte, out any) error {
	var res response
	if err := json.Unmarshal(line, &res); err != nil {
		return fmt.Errorf("herdr %s: decoding response: %w", method, err)
	}
	if res.ID != id {
		return fmt.Errorf("herdr %s: response id %q does not match request id %q", method, res.ID, id)
	}
	if res.Error != nil {
		return &Error{Code: res.Error.Code, Message: res.Error.Message, Method: method}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(res.Result, out); err != nil {
		return fmt.Errorf("herdr %s: decoding result: %w", method, err)
	}
	return nil
}

// waitTimeout is the client-side bound for a call whose herdr-side wait is timeoutMS.
func waitTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return 0
	}
	return time.Duration(timeoutMS)*time.Millisecond + waitGrace
}

// ---- Methods -----------------------------------------------------------------------

// Ping implements Client.
func (c *SocketClient) Ping(ctx context.Context) (Pong, error) {
	var pong Pong
	err := c.call(ctx, "ping", emptyParams, &pong, 0)
	return pong, err
}

// Snapshot implements Client.
func (c *SocketClient) Snapshot(ctx context.Context) (Snapshot, error) {
	var result struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	err := c.call(ctx, "session.snapshot", emptyParams, &result, 0)
	return result.Snapshot, err
}

// WorkspaceCreate implements Client.
func (c *SocketClient) WorkspaceCreate(ctx context.Context, params WorkspaceCreateParams) (WorkspaceCreated, error) {
	var result WorkspaceCreated
	err := c.call(ctx, "workspace.create", params, &result, 0)
	return result, err
}

// WorkspaceList implements Client.
func (c *SocketClient) WorkspaceList(ctx context.Context) ([]Workspace, error) {
	var result struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	err := c.call(ctx, "workspace.list", emptyParams, &result, 0)
	return result.Workspaces, err
}

// TabCreate implements Client.
func (c *SocketClient) TabCreate(ctx context.Context, params TabCreateParams) (TabCreated, error) {
	var result TabCreated
	err := c.call(ctx, "tab.create", params, &result, 0)
	return result, err
}

type workspaceFilter struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// TabList implements Client.
func (c *SocketClient) TabList(ctx context.Context, workspaceID string) ([]Tab, error) {
	var result struct {
		Tabs []Tab `json:"tabs"`
	}
	err := c.call(ctx, "tab.list", workspaceFilter{WorkspaceID: workspaceID}, &result, 0)
	return result.Tabs, err
}

// PaneList implements Client.
func (c *SocketClient) PaneList(ctx context.Context, workspaceID string) ([]Pane, error) {
	var result struct {
		Panes []Pane `json:"panes"`
	}
	err := c.call(ctx, "pane.list", workspaceFilter{WorkspaceID: workspaceID}, &result, 0)
	return result.Panes, err
}

type paneTarget struct {
	PaneID string `json:"pane_id"`
}

// PaneGet implements Client.
func (c *SocketClient) PaneGet(ctx context.Context, paneID string) (Pane, error) {
	var result struct {
		Pane Pane `json:"pane"`
	}
	err := c.call(ctx, "pane.get", paneTarget{PaneID: paneID}, &result, 0)
	return result.Pane, err
}

type readParams struct {
	PaneID    string     `json:"pane_id,omitempty"`
	Target    string     `json:"target,omitempty"`
	Source    ReadSource `json:"source"`
	Lines     int        `json:"lines,omitempty"`
	Format    ReadFormat `json:"format"`
	StripANSI bool       `json:"strip_ansi"`
}

func newReadParams(opts ReadOptions) readParams {
	params := readParams{Source: opts.Source, Lines: opts.Lines, Format: FormatText, StripANSI: true}
	if params.Source == "" {
		params.Source = SourceVisible
	}
	if opts.ANSI {
		params.Format, params.StripANSI = FormatANSI, false
	}
	return params
}

// PaneRead implements Client.
func (c *SocketClient) PaneRead(ctx context.Context, paneID string, opts ReadOptions) (ReadResult, error) {
	params := newReadParams(opts)
	params.PaneID = paneID
	var result struct {
		Read ReadResult `json:"read"`
	}
	err := c.call(ctx, "pane.read", params, &result, 0)
	return result.Read, err
}

// PaneProcessInfo implements Client.
func (c *SocketClient) PaneProcessInfo(ctx context.Context, paneID string) (ProcessInfo, error) {
	var result struct {
		ProcessInfo ProcessInfo `json:"process_info"`
	}
	err := c.call(ctx, "pane.process_info", paneTarget{PaneID: paneID}, &result, 0)
	return result.ProcessInfo, err
}

// PaneRun implements Client. The socket API has no pane.run (that is a CLI convenience); it is
// pane.send_input with the command text followed by Enter, written as one ordered submission.
func (c *SocketClient) PaneRun(ctx context.Context, paneID, command string) error {
	params := struct {
		PaneID string   `json:"pane_id"`
		Text   string   `json:"text"`
		Keys   []string `json:"keys"`
	}{PaneID: paneID, Text: command, Keys: []string{"Enter"}}
	return c.call(ctx, "pane.send_input", params, nil, 0)
}

// PaneSendKeys implements Client.
func (c *SocketClient) PaneSendKeys(ctx context.Context, paneID string, keys []string) error {
	params := struct {
		PaneID string   `json:"pane_id"`
		Keys   []string `json:"keys"`
	}{PaneID: paneID, Keys: keys}
	return c.call(ctx, "pane.send_keys", params, nil, 0)
}

type agentResult struct {
	Agent Agent `json:"agent"`
}

type agentTarget struct {
	Target string `json:"target"`
}

// AgentStart implements Client.
func (c *SocketClient) AgentStart(ctx context.Context, params AgentStartParams) (Agent, error) {
	var result agentResult
	timeout := waitTimeout(params.TimeoutMS)
	if timeout == 0 {
		timeout = 30*time.Second + waitGrace // herdr's own default start-up timeout is 30 s
	}
	err := c.call(ctx, "agent.start", params, &result, timeout)
	return result.Agent, err
}

// AgentGet implements Client.
func (c *SocketClient) AgentGet(ctx context.Context, target string) (Agent, error) {
	var result agentResult
	err := c.call(ctx, "agent.get", agentTarget{Target: target}, &result, 0)
	return result.Agent, err
}

// AgentList implements Client.
func (c *SocketClient) AgentList(ctx context.Context) ([]Agent, error) {
	var result struct {
		Agents []Agent `json:"agents"`
	}
	err := c.call(ctx, "agent.list", emptyParams, &result, 0)
	return result.Agents, err
}

// AgentPrompt implements Client.
func (c *SocketClient) AgentPrompt(ctx context.Context, target, text string, wait *PromptWait) (Agent, error) {
	params := struct {
		Target string      `json:"target"`
		Text   string      `json:"text"`
		Wait   *PromptWait `json:"wait,omitempty"`
	}{Target: target, Text: text, Wait: wait}
	var timeout time.Duration
	if wait != nil {
		timeout = waitTimeout(wait.TimeoutMS)
	}
	var result agentResult
	err := c.call(ctx, "agent.prompt", params, &result, timeout)
	return result.Agent, err
}

// AgentRead implements Client.
func (c *SocketClient) AgentRead(ctx context.Context, target string, opts ReadOptions) (ReadResult, error) {
	params := newReadParams(opts)
	params.Target = target
	var result struct {
		Read ReadResult `json:"read"`
	}
	err := c.call(ctx, "agent.read", params, &result, 0)
	return result.Read, err
}

// AgentSendKeys implements Client.
func (c *SocketClient) AgentSendKeys(ctx context.Context, target string, keys []string) error {
	params := struct {
		Target string   `json:"target"`
		Keys   []string `json:"keys"`
	}{Target: target, Keys: keys}
	return c.call(ctx, "agent.send_keys", params, nil, 0)
}

// AgentWait implements Client.
func (c *SocketClient) AgentWait(ctx context.Context, target string, until []AgentStatus, timeoutMS int) (Agent, error) {
	params := struct {
		Target    string        `json:"target"`
		Until     []AgentStatus `json:"until,omitempty"`
		TimeoutMS int           `json:"timeout_ms,omitempty"`
	}{Target: target, Until: until, TimeoutMS: timeoutMS}
	timeout := waitTimeout(timeoutMS)
	if timeout == 0 {
		timeout = 24 * time.Hour // no herdr-side timeout: the caller's ctx is the only bound
	}
	var result agentResult
	err := c.call(ctx, "agent.wait", params, &result, timeout)
	return result.Agent, err
}

// AgentRename implements Client.
func (c *SocketClient) AgentRename(ctx context.Context, target, name string) (Agent, error) {
	params := struct {
		Target string  `json:"target"`
		Name   *string `json:"name"`
	}{Target: target}
	if name != "" {
		params.Name = &name
	}
	var result agentResult
	err := c.call(ctx, "agent.rename", params, &result, 0)
	return result.Agent, err
}

// ---- Events ------------------------------------------------------------------------

// Subscribe implements Client. It returns once herdr has acknowledged the subscription, so an
// invalid subscription (a per-pane type without a pane id, say) fails here, not later.
func (c *SocketClient) Subscribe(ctx context.Context, subscriptions []Subscription) (EventStream, error) {
	ackCtx, cancel := context.WithTimeout(ctx, DefaultCallTimeout)
	defer cancel()
	conn, err := c.dial(ackCtx)
	if err != nil {
		return nil, fmt.Errorf("herdr events.subscribe: %w", err)
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	params := struct {
		Subscriptions []Subscription `json:"subscriptions"`
	}{Subscriptions: subscriptions}
	reader := bufio.NewReader(conn)
	if err := c.acknowledge(ackCtx, conn, reader, id, params); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{}) // the stream lives as long as the caller wants it
	stream := &socketStream{conn: conn, events: make(chan Event, eventBuffer), done: make(chan struct{})}
	go stream.read(ctx, reader)
	return stream, nil
}

func (c *SocketClient) acknowledge(ctx context.Context, conn net.Conn, reader *bufio.Reader, id string, params any) error {
	stop := closeOnDone(ctx, conn)
	defer stop()
	if err := writeLine(conn, request{ID: id, Method: "events.subscribe", Params: params}); err != nil {
		return wrapIO(ctx, "events.subscribe", "sending request", err)
	}
	line, err := readLine(reader)
	if err != nil {
		return wrapIO(ctx, "events.subscribe", "reading acknowledgement", err)
	}
	return decodeResponse("events.subscribe", id, line, nil)
}

// socketStream is one live subscription connection.
type socketStream struct {
	conn      net.Conn
	events    chan Event
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	err       error
	closed    bool // Close was called: the read error that follows is ours, not a failure
}

func (s *socketStream) Events() <-chan Event { return s.events }

func (s *socketStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *socketStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.done) })
	return s.conn.Close()
}

func (s *socketStream) finish(err error) {
	s.mu.Lock()
	if !s.closed {
		s.err = err
	}
	s.mu.Unlock()
	close(s.events)
	s.closeOnce.Do(func() { close(s.done) }) // releases the context watcher
	_ = s.conn.Close()
}

// read pumps events until the connection ends. An error frame ends the stream with that error
// (events_lost is the one herdr sends); EOF ends it with io.ErrUnexpectedEOF, because a healthy
// server never closes a subscription by itself.
func (s *socketStream) read(ctx context.Context, reader *bufio.Reader) {
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	for {
		line, err := readLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			s.finish(fmt.Errorf("herdr event stream: %w", err))
			return
		}
		event, streamErr := parseEventLine(line)
		if streamErr != nil {
			s.finish(streamErr)
			return
		}
		if event.Kind == "" {
			continue // a frame we do not understand; events are only invalidations anyway
		}
		select {
		case s.events <- event:
		case <-s.done:
			s.finish(nil)
			return
		}
	}
}

// eventFrame is either a pushed event or an error frame on a subscription connection.
type eventFrame struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// eventIDs pulls the ids out of an event payload. Per-pane events carry them at the top level;
// lifecycle events nest them in the object they describe.
type eventIDs struct {
	PaneID      string      `json:"pane_id"`
	WorkspaceID string      `json:"workspace_id"`
	Status      AgentStatus `json:"agent_status"`
	Pane        *struct {
		PaneID      string `json:"pane_id"`
		WorkspaceID string `json:"workspace_id"`
	} `json:"pane"`
	Workspace *struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"workspace"`
	Tab *struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"tab"`
}

// parseEventLine turns one frame into an Event, or returns the error an error frame carries.
// A frame that does not decode is skipped (empty Kind) rather than killing the stream.
func parseEventLine(line []byte) (Event, error) {
	var frame eventFrame
	if decoded := json.Unmarshal(line, &frame) == nil; !decoded {
		return Event{}, nil // skipped, not fatal: see the doc comment
	}
	if frame.Error != nil {
		return Event{}, &Error{Code: frame.Error.Code, Message: frame.Error.Message, Method: "events.subscribe"}
	}
	event := Event{Kind: frame.Event, Data: frame.Data}
	var ids eventIDs
	if json.Unmarshal(frame.Data, &ids) == nil {
		event.PaneID, event.WorkspaceID, event.Status = ids.PaneID, ids.WorkspaceID, ids.Status
		if ids.Pane != nil {
			event.PaneID, event.WorkspaceID = firstNonEmpty(event.PaneID, ids.Pane.PaneID), firstNonEmpty(event.WorkspaceID, ids.Pane.WorkspaceID)
		}
		if ids.Workspace != nil {
			event.WorkspaceID = firstNonEmpty(event.WorkspaceID, ids.Workspace.WorkspaceID)
		}
		if ids.Tab != nil {
			event.WorkspaceID = firstNonEmpty(event.WorkspaceID, ids.Tab.WorkspaceID)
		}
	}
	return event, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ Client = (*SocketClient)(nil)
