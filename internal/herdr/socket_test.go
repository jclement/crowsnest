// Tests for SocketClient against a scripted NDJSON server on a real unix socket: framing,
// error codes, concurrent calls, the event stream, and recovery after the server restarts.

package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedRequest is what the test server received.
type scriptedRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// handler answers one request. It returns the raw lines to write back. For events.subscribe it
// may also return a channel; every line sent on it is written to the connection afterwards.
type handler func(req scriptedRequest) (lines []string, push <-chan string)

// scriptedServer is a minimal herdr stand-in.
type scriptedServer struct {
	t        *testing.T
	path     string
	listener net.Listener
	handle   handler
	wg       sync.WaitGroup
}

// shortSocketDir returns a directory short enough for a unix socket path (macOS caps them at
// 104 bytes, and t.TempDir() under /var/folders can exceed that).
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func startScripted(t *testing.T, path string, handle handler) *scriptedServer {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &scriptedServer{t: t, path: path, listener: listener, handle: handle}
	server.wg.Add(1)
	go server.accept()
	t.Cleanup(server.stop)
	return server
}

func (s *scriptedServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.serve(conn)
	}
}

func (s *scriptedServer) serve(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	var req scriptedRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		s.t.Errorf("server got a line that is not JSON: %q", line)
		return
	}
	lines, push := s.handle(req)
	for _, out := range lines {
		if _, err := conn.Write([]byte(out + "\n")); err != nil {
			return
		}
	}
	if push == nil {
		return
	}
	for out := range push {
		if _, err := conn.Write([]byte(out + "\n")); err != nil {
			return
		}
	}
}

func (s *scriptedServer) stop() {
	_ = s.listener.Close()
	s.wg.Wait()
}

// result frames body as a success answer, compacted onto one line as herdr sends it.
func result(id, body string) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(body)); err != nil {
		panic(fmt.Sprintf("bad scripted JSON: %v", err))
	}
	return fmt.Sprintf(`{"id":%q,"result":%s}`, id, compact.String())
}

func errorLine(id, code, message string) string {
	return fmt.Sprintf(`{"id":%q,"error":{"code":%q,"message":%q}}`, id, code, message)
}

func TestSocketClientDecodesResults(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	var mu sync.Mutex
	seen := map[string]json.RawMessage{}
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		mu.Lock()
		seen[req.Method] = req.Params
		mu.Unlock()
		switch req.Method {
		case "ping":
			return []string{result(req.ID, `{"type":"pong","version":"0.9.5","protocol":22,"capabilities":{"live_handoff":true}}`)}, nil
		case "session.snapshot":
			return []string{result(req.ID, `{"type":"session_snapshot","snapshot":{"version":"0.9.5","protocol":22,
				"workspaces":[{"workspace_id":"w1","number":1,"label":"crow","focused":true,"pane_count":1,"tab_count":1,"active_tab_id":"w1:t1","agent_status":"working","brand_new_field":7}],
				"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","number":1,"label":"1","focused":true,"pane_count":1,"agent_status":"working"}],
				"panes":[{"pane_id":"w1:p1","terminal_id":"t","workspace_id":"w1","tab_id":"w1:t1","focused":true,"agent_status":"working","revision":3,"agent":"claude","cwd":"/src"}],
				"layouts":[],
				"agents":[{"pane_id":"w1:p1","terminal_id":"t","workspace_id":"w1","tab_id":"w1:t1","focused":true,"revision":3,"agent_status":"working","name":"orchestrator","agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"hook","value":"abc-123"}}]}}`)}, nil
		case "pane.read":
			return []string{result(req.ID, `{"type":"pane_read","read":{"pane_id":"w1:p1","workspace_id":"w1","tab_id":"w1:t1","source":"visible","format":"ansi","text":"\u001b[1mhi\u001b[0m\n❯ ","revision":4,"truncated":false}}`)}, nil
		case "pane.send_input":
			return []string{result(req.ID, `{"type":"ok"}`)}, nil
		default:
			return []string{errorLine(req.ID, "invalid_request", "unknown method "+req.Method)}, nil
		}
	})
	client := NewSocketClient(path)
	ctx := context.Background()

	pong, err := client.Ping(ctx)
	if err != nil || pong.Version != "0.9.5" || pong.Protocol != 22 {
		t.Fatalf("ping = %+v, %v", pong, err)
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].Label != "crow" || snapshot.Agents[0].Name != "orchestrator" {
		t.Fatalf("snapshot decoded wrong: %+v", snapshot)
	}
	if snapshot.Agents[0].AgentSession == nil || snapshot.Agents[0].AgentSession.Value != "abc-123" {
		t.Fatalf("agent session not decoded: %+v", snapshot.Agents[0])
	}
	read, err := client.PaneRead(ctx, "w1:p1", ReadOptions{ANSI: true})
	if err != nil || !strings.Contains(read.Text, "\x1b[1mhi") {
		t.Fatalf("read = %+v, %v", read, err)
	}
	if err := client.PaneRun(ctx, "w1:p1", "echo hi"); err != nil {
		t.Fatalf("pane run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := string(seen["pane.read"]); got != `{"pane_id":"w1:p1","source":"visible","format":"ansi","strip_ansi":false}` {
		t.Errorf("pane.read params = %s", got)
	}
	if got := string(seen["pane.send_input"]); got != `{"pane_id":"w1:p1","text":"echo hi","keys":["Enter"]}` {
		t.Errorf("pane.send_input params = %s", got)
	}
	if got := string(seen["ping"]); got != `{}` {
		t.Errorf("ping params = %s, want {}", got)
	}
}

func TestSocketClientTypedErrors(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	codes := map[string]string{
		"agent.prompt": CodeAgentBlocked, "agent.start": CodeAgentNotReady, "agent.wait": CodeTimeout,
		"agent.get": CodeAgentNotFound, "pane.get": CodePaneNotFound, "tab.list": CodeInvalidRequest,
	}
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		if req.Method == "agent.read" {
			return []string{errorLine(req.ID, CodeAgentPromptStalled, "stalled")}, nil
		}
		return []string{errorLine(req.ID, codes[req.Method], "scripted failure")}, nil
	})
	client := NewSocketClient(path)
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"blocked", func() error { _, err := client.AgentPrompt(ctx, "a", "hi", nil); return err }, ErrAgentBlocked},
		{"not ready", func() error {
			_, err := client.AgentStart(ctx, AgentStartParams{Name: "a", Kind: "claude", PaneID: "w1:p1"})
			return err
		}, ErrAgentNotReady},
		{"timeout", func() error { _, err := client.AgentWait(ctx, "a", nil, 100); return err }, ErrTimeout},
		{"invalid", func() error { _, err := client.TabList(ctx, ""); return err }, ErrInvalidRequest},
		{"stalled", func() error { _, err := client.AgentRead(ctx, "a", ReadOptions{}); return err }, ErrAgentPromptStalled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var herdrErr *Error
			if !errors.As(err, &herdrErr) || herdrErr.Message != "scripted failure" && herdrErr.Message != "stalled" {
				t.Fatalf("err does not carry herdr's message: %#v", err)
			}
		})
	}
	if _, err := client.AgentGet(ctx, "x"); !IsNotFound(err) {
		t.Errorf("agent_not_found: IsNotFound = false for %v", err)
	}
	if _, err := client.PaneGet(ctx, "x"); !IsNotFound(err) {
		t.Errorf("pane_not_found: IsNotFound = false for %v", err)
	}
}

func TestSocketClientConcurrentCalls(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		var params struct {
			PaneID string `json:"pane_id"`
		}
		_ = json.Unmarshal(req.Params, &params)
		// Answer out of order in time so a client that mixed up responses would notice.
		if strings.HasSuffix(params.PaneID, "0") {
			time.Sleep(20 * time.Millisecond)
		}
		return []string{result(req.ID, fmt.Sprintf(`{"type":"pane_info","pane":{"pane_id":%q,"terminal_id":"t","workspace_id":"w1","tab_id":"w1:t1","focused":false,"agent_status":"idle","revision":1}}`, params.PaneID))}, nil
	})
	client := NewSocketClient(path)
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprintf("w1:p%d", i)
			pane, err := client.PaneGet(context.Background(), want)
			if err != nil {
				errs <- err
				return
			}
			if pane.ID != want {
				errs <- fmt.Errorf("asked for %s, got %s", want, pane.ID)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSocketClientRejectsMismatchedID(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	startScripted(t, path, func(scriptedRequest) ([]string, <-chan string) {
		return []string{result("999", `{"type":"pong","version":"x","protocol":22}`)}, nil
	})
	if _, err := NewSocketClient(path).Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v, want an id mismatch", err)
	}
}

func TestSocketClientHonoursContext(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	release := make(chan struct{})
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		<-release // never answers while the test runs
		return nil, nil
	})
	t.Cleanup(func() { close(release) }) // registered after the server, so it runs before stop
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewSocketClient(path).Ping(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	// The request went out and no answer came: the caller is told herdr may have acted (R1-C09).
	if !errors.Is(err, ErrNoResponse) {
		t.Fatalf("err = %v, want ErrNoResponse", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("call took %s after its context expired", elapsed)
	}
}

// TestSocketClientUnreachableIsNotNoResponse: a call that never reached herdr is a plain
// failure, safe to retry.
func TestSocketClientUnreachableIsNotNoResponse(t *testing.T) {
	_, err := NewSocketClient(filepath.Join(shortSocketDir(t), "absent.sock")).Ping(context.Background())
	if err == nil || errors.Is(err, ErrNoResponse) {
		t.Fatalf("err = %v, want a failure that is not ErrNoResponse", err)
	}
}

func TestSocketClientReconnectsAfterRestart(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	pong := func(version string) handler {
		return func(req scriptedRequest) ([]string, <-chan string) {
			return []string{result(req.ID, fmt.Sprintf(`{"type":"pong","version":%q,"protocol":22}`, version))}, nil
		}
	}
	first := startScripted(t, path, pong("one"))
	client := NewSocketClient(path)
	if got, err := client.Ping(context.Background()); err != nil || got.Version != "one" {
		t.Fatalf("first ping = %+v, %v", got, err)
	}
	first.stop()
	if _, err := client.Ping(context.Background()); err == nil {
		t.Fatal("ping succeeded with no server")
	}
	_ = os.Remove(path)
	startScripted(t, path, pong("two"))
	if got, err := client.Ping(context.Background()); err != nil || got.Version != "two" {
		t.Fatalf("ping after restart = %+v, %v", got, err)
	}
}

func TestSocketClientEventStream(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	push := make(chan string, 8)
	var subscribed json.RawMessage
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		subscribed = req.Params
		return []string{result(req.ID, `{"type":"subscription_started"}`)}, push
	})
	stream, err := NewSocketClient(path).Subscribe(context.Background(), []Subscription{
		{Type: "pane.created"}, {Type: EventAgentStatusChanged, PaneID: "w1:p1"},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := string(subscribed); got != `{"subscriptions":[{"type":"pane.created"},{"type":"pane.agent_status_changed","pane_id":"w1:p1"}]}` {
		t.Errorf("subscribe params = %s", got)
	}
	push <- `{"data":{"pane":{"pane_id":"w1:p2","workspace_id":"w1"},"type":"pane_created"},"event":"pane_created"}`
	push <- `not json at all`
	push <- `{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"blocked"}}`
	push <- `{"error":{"code":"events_lost","message":"event subscription fell behind retained history"}}`

	want := []Event{
		{Kind: "pane_created", PaneID: "w1:p2", WorkspaceID: "w1"},
		{Kind: "pane.agent_status_changed", PaneID: "w1:p1", WorkspaceID: "w1", Status: StatusBlocked},
	}
	for i, expected := range want {
		select {
		case got, ok := <-stream.Events():
			if !ok {
				t.Fatalf("stream closed before event %d: %v", i, stream.Err())
			}
			if got.Kind != expected.Kind || got.PaneID != expected.PaneID || got.WorkspaceID != expected.WorkspaceID || got.Status != expected.Status {
				t.Fatalf("event %d = %+v, want %+v", i, got, expected)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
	select {
	case _, ok := <-stream.Events():
		if ok {
			t.Fatal("expected the stream to end after events_lost")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after events_lost")
	}
	if !errors.Is(stream.Err(), ErrEventsLost) {
		t.Fatalf("stream.Err() = %v, want events_lost", stream.Err())
	}
	close(push)
}

// TestSubscribeSurvivesALateWatcher is R1-C03: the acknowledgement's cancellation watcher may
// not be scheduled until after the acknowledgement is read. Subscribe then cancels its
// acknowledgement context; a watcher that wakes to find both its cases ready must not close the
// live subscription.
func TestSubscribeSurvivesALateWatcher(t *testing.T) {
	previous := watcherStarting
	watcherStarting = func() { time.Sleep(20 * time.Millisecond) }
	t.Cleanup(func() { watcherStarting = previous })
	path := filepath.Join(shortSocketDir(t), "h.sock")
	var pushes []chan string
	var mu sync.Mutex
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		push := make(chan string, 1)
		mu.Lock()
		pushes = append(pushes, push)
		mu.Unlock()
		return []string{result(req.ID, `{"type":"subscription_started"}`)}, push
	})
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, push := range pushes {
			close(push)
		}
	})
	client := NewSocketClient(path)
	// Each run has an even chance of losing the stream without the fix; 16 runs make a pass
	// by luck one in 65536.
	for run := range 16 {
		stream, err := client.Subscribe(context.Background(), []Subscription{{Type: "tab.created"}})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Millisecond) // let a stray watcher do its damage
		mu.Lock()
		pushes[run] <- `{"event":"tab_created","data":{"tab":{"tab_id":"w1:t2","workspace_id":"w1"}}}`
		mu.Unlock()
		select {
		case _, ok := <-stream.Events():
			if !ok {
				t.Fatalf("run %d: the subscription was closed after it was acknowledged: %v", run, stream.Err())
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("run %d: no event", run)
		}
		_ = stream.Close()
	}
}

func TestSocketClientSubscribeRefused(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		return []string{errorLine(req.ID, CodeInvalidRequest, "pane_id is required")}, nil
	})
	_, err := NewSocketClient(path).Subscribe(context.Background(), []Subscription{{Type: EventAgentStatusChanged}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want invalid_request", err)
	}
}

func TestSocketStreamCloseAndDrop(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "h.sock")
	push := make(chan string)
	server := startScripted(t, path, func(req scriptedRequest) ([]string, <-chan string) {
		return []string{result(req.ID, `{"type":"subscription_started"}`)}, push
	})
	client := NewSocketClient(path)

	// Close by the caller: the stream ends without an error.
	stream, err := client.Subscribe(context.Background(), []Subscription{{Type: "tab.created"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	waitClosed(t, stream)
	if stream.Err() != nil {
		t.Fatalf("Err after Close = %v, want nil", stream.Err())
	}

	// The server going away: the stream ends with an error so the caller resubscribes.
	stream, err = client.Subscribe(context.Background(), []Subscription{{Type: "tab.created"}})
	if err != nil {
		t.Fatal(err)
	}
	close(push)
	server.stop()
	waitClosed(t, stream)
	if stream.Err() == nil {
		t.Fatal("Err after the server dropped = nil, want an error")
	}
}

func waitClosed(t *testing.T, stream EventStream) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-stream.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream did not close")
		}
	}
}

func TestReadLineLimits(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 10000)+"\nnext"), 16)
	line, err := readLine(reader)
	if err != nil || len(line) != 10000 {
		t.Fatalf("long line: len %d, err %v", len(line), err)
	}
	line, err = readLine(reader)
	if err != nil || string(line) != "next" {
		t.Fatalf("final unterminated line = %q, %v", line, err)
	}
}

func TestScrubEnvAndSessionNames(t *testing.T) {
	got := ScrubEnv([]string{"HOME=/h", "HERDR_SOCKET_PATH=/x", "HERDR_ENV=1", "PATH=/bin", "XHERDR_KEEP=1"})
	if strings.Join(got, ",") != "HOME=/h,PATH=/bin,XHERDR_KEEP=1" {
		t.Fatalf("ScrubEnv = %v", got)
	}
	for _, tc := range []struct {
		name  string
		allow bool
		ok    bool
	}{
		{"crowsnest", false, true}, {"crowsnest-test-abc", false, true}, {"default", false, false},
		{"default", true, true}, {"../etc", false, false}, {"", false, false}, {"-x", false, false},
	} {
		if err := ValidateSessionName(tc.name, tc.allow); (err == nil) != tc.ok {
			t.Errorf("ValidateSessionName(%q, %v) = %v", tc.name, tc.allow, err)
		}
	}
}

func TestEnsureSessionRefusesDefault(t *testing.T) {
	_, err := EnsureSession(context.Background(), SessionOptions{Name: "default", Binary: "definitely-not-herdr"})
	if err == nil || !strings.Contains(err.Error(), "default session") {
		t.Fatalf("err = %v, want a refusal of the default session", err)
	}
}

func TestEnsureSessionNeedsHerdr(t *testing.T) {
	_, err := EnsureSession(context.Background(), SessionOptions{Name: "crowsnest-test-x", Binary: "definitely-not-herdr"})
	if err == nil || !strings.Contains(err.Error(), "--demo") {
		t.Fatalf("err = %v, want a message pointing at --demo", err)
	}
}
