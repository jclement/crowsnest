package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// shortDir is a temp dir under /tmp: t.TempDir on macOS is too deep for a 104-byte socket path.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cn-agentapi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type socketFixture struct {
	socket  string
	service *questions.Service
	hub     *hub.Hub
}

// startSocket serves a fresh agent API on a socket in a short temp dir.
func startSocket(t *testing.T) *socketFixture {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	events := hub.New()
	service := questions.New(questions.Options{Store: db, Hub: events})
	path := filepath.Join(shortDir(t), "run", "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = NewServer(ServerOptions{Questions: service, Hub: events, Version: "test"}).Serve(ctx, listener)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return &socketFixture{socket: path, service: service, hub: events}
}

func TestSocketPermissions(t *testing.T) {
	f := startSocket(t)
	info, err := os.Stat(f.socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Type() != fs.ModeSocket || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, want a 0600 socket", info.Mode())
	}
	dir, _ := os.Stat(filepath.Dir(f.socket))
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir mode %v, want 0700", dir.Mode().Perm())
	}
}

func TestListenTightensAnExistingDirectory(t *testing.T) {
	dir := filepath.Join(shortDir(t), "loose")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(context.Background(), filepath.Join(dir, "a.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, want 0700", info.Mode().Perm())
	}
}

func TestListenRemovesAStaleSocketButNotALiveOneOrAFile(t *testing.T) {
	dir := shortDir(t)
	stale := filepath.Join(dir, "stale.sock")
	// A socket file nobody listens on: listen, then close without removing it.
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	again, err := Listen(context.Background(), stale)
	if err != nil {
		t.Fatalf("a stale socket should be replaced: %v", err)
	}
	if _, err := Listen(context.Background(), stale); err == nil || !strings.Contains(err.Error(), "already serving") {
		t.Fatalf("a live socket must not be taken over: %v", err)
	}
	_ = again.Close()

	file := filepath.Join(dir, "file.sock")
	if err := os.WriteFile(file, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(context.Background(), file); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("a regular file must be left alone: %v", err)
	}
	if body, _ := os.ReadFile(file); string(body) != "not a socket" {
		t.Fatal("the regular file was touched")
	}
	if _, err := Listen(context.Background(), "/tmp/"+strings.Repeat("x", 120)+".sock"); err == nil {
		t.Fatal("an over-long socket path should be refused with a message")
	}
}

func TestSocketPath(t *testing.T) {
	if got := socketPath("/run/user/1000", "/state", "crow", "/tmp", 1000); got != "/run/user/1000/crowsnest/crow.sock" {
		t.Fatalf("runtime dir: %s", got)
	}
	if got := socketPath("", "/state/crowsnest/crow", "crow", "/tmp", 1000); got != "/state/crowsnest/crow/agent.sock" {
		t.Fatalf("state dir: %s", got)
	}
	deep := "/var/folders/zt/5mndvdys68v75kz57f0nm_c00000gn/T/crowsnest-e2e-phgpaO/state/crowsnest/crowsnest-demo"
	got := socketPath("", deep, "crowsnest-demo", "/var/folders/zt/5mndvdys68v75kz57f0nm_c00000gn/T", 501)
	if len(got) > maxSocketPath || !strings.HasPrefix(got, "/var/folders/zt/5mndvdys68v75kz57f0nm_c00000gn/T/crowsnest-501/") {
		t.Fatalf("too-long state dir should fall back to the temp dir: %s (%d bytes)", got, len(got))
	}
	if other := socketPath("", deep+"x", "crowsnest-demo", "/tmp", 501); other == socketPath("", deep, "crowsnest-demo", "/tmp", 501) {
		t.Fatal("two state dirs must not share a fallback socket")
	}
}

func TestListenRefusesASymlinkedDirectory(t *testing.T) {
	dir := shortDir(t)
	target := filepath.Join(dir, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(context.Background(), filepath.Join(link, "a.sock")); err == nil || !strings.Contains(err.Error(), "not a plain directory") {
		t.Fatalf("a symlinked socket directory must be refused: %v", err)
	}
}

func TestClientRoundTripRecordsCaller(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	client := NewClient(f.socket, questions.Caller{PaneID: "w1:p2", JobID: "j_9"})
	if health, err := client.Health(ctx); err != nil || health.Status != "ok" {
		t.Fatalf("health %+v %v", health, err)
	}
	asked, err := client.Ask(ctx, questions.AskInput{Title: "Ship?", Kind: questions.KindConfirm, Recommend: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if asked.SourcePane != "w1:p2" || asked.JobID != "j_9" {
		t.Fatalf("caller not recorded: %+v", asked)
	}
	_, err = client.Ask(ctx, questions.AskInput{Title: "Ship?", Kind: questions.KindConfirm})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(apiErr.Message, "recommend") {
		t.Fatalf("validation error %v", err)
	}

	// A long-poll wakes on the answer.
	result := make(chan questions.Question, 1)
	go func() {
		q, _ := client.WaitSettled(ctx, asked.ID)
		result <- q
	}()
	time.Sleep(100 * time.Millisecond)
	answered, err := f.service.Answer(ctx, asked.ID, questions.AnswerInput{Value: json.RawMessage(`"yes"`)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case q := <-result:
		if q.Status != questions.StatusAnswered {
			t.Fatalf("waited %+v", q)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not return")
	}
	if answered.Delivery != questions.DeliveryPoll {
		t.Fatalf("delivery %q while the client polled", answered.Delivery)
	}

	second, _ := client.Ask(ctx, questions.AskInput{Title: "Withdraw me", Kind: questions.KindAck})
	cancelled, err := client.Cancel(ctx, second.ID)
	if err != nil || cancelled.Status != questions.StatusCancelled || cancelled.Delivery != questions.DeliveryNone {
		t.Fatalf("cancel %+v %v", cancelled, err)
	}
	if _, err := client.Cancel(ctx, second.ID); !errors.As(err, &apiErr) || apiErr.Status != 409 {
		t.Fatalf("second cancel %v", err)
	}
	if _, err := client.Get(ctx, "q_404", 0); !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("missing question %v", err)
	}
}

func TestSayPublishesAnEvent(t *testing.T) {
	f := startSocket(t)
	sub := f.hub.Subscribe(4)
	defer sub.Close()
	client := NewClient(f.socket, questions.Caller{PaneID: "w1:p1"})
	if err := client.Say(context.Background(), "  Tests are\ngreen.  "); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.Events():
		say, ok := event.Data.(SayEvent)
		if event.Type != "say" || !ok || say.Text != "Tests are green." || say.PaneID != "w1:p1" {
			t.Fatalf("event %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("no say event")
	}
	if err := client.Say(context.Background(), strings.Repeat("a", MaxSayRunes+1)); err == nil {
		t.Fatal("an over-long sentence should be refused")
	}
}

func TestClientErrors(t *testing.T) {
	if _, err := NewClientFromEnv(func(string) string { return "" }); !errors.Is(err, ErrNoSocket) {
		t.Fatalf("no socket: %v", err)
	}
	client := NewClient(filepath.Join(shortDir(t), "none.sock"), questions.Caller{})
	var down *ServerDownError
	if err := client.Say(context.Background(), "hi"); !errors.As(err, &down) {
		t.Fatalf("server down: %v", err)
	}
}

func TestIdentityHeadersAreSanitised(t *testing.T) {
	if identity("w1:p1") != "w1:p1" || identity("bad\x1bvalue") != "" || identity(strings.Repeat("x", 200)) != "" {
		t.Fatal("identity sanitising")
	}
}

// ---- MCP -----------------------------------------------------------------------------------

func connectMCP(t *testing.T, client *Client) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := NewMCPServer(client, "test").Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPToolList(t *testing.T) {
	f := startSocket(t)
	session := connectMCP(t, NewClient(f.socket, questions.Caller{}))
	var names []string
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
		if len(tool.Description) < 80 {
			t.Errorf("tool %s has a thin description; it is the agent's instruction", tool.Name)
		}
	}
	if strings.Join(names, ",") != "ask,say" {
		t.Fatalf("tools %v", names)
	}
	if !strings.Contains(session.InitializeResult().Instructions, "never consent") {
		t.Fatal("server instructions missing")
	}
}

func TestMCPAskNonBlockingThenWait(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	session := connectMCP(t, NewClient(f.socket, questions.Caller{PaneID: "w1:p1"}))

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{
		"title": "Ship?", "kind": "confirm", "recommend": "yes",
	}})
	if err != nil || result.IsError {
		t.Fatalf("ask: %+v %v", result, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "q_1 posted") || !strings.Contains(text, "[crowsnest answer q_1]") {
		t.Fatalf("non-blocking result %q", text)
	}
	open, _ := f.service.List(ctx, "open")
	if len(open) != 1 || open[0].SourcePane != "w1:p1" || open[0].Wait {
		t.Fatalf("stored %+v", open)
	}

	bad, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{"title": "No rec", "kind": "choice",
		"options": []map[string]string{{"key": "a", "label": "A"}, {"key": "b", "label": "B"}}}})
	if err != nil || !bad.IsError || !strings.Contains(bad.Content[0].(*mcp.TextContent).Text, "recommend") {
		t.Fatalf("validation should come back as a tool error: %+v %v", bad, err)
	}

	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			list, _ := f.service.List(ctx, "open")
			for _, q := range list {
				if q.Wait {
					_, _ = f.service.Answer(ctx, q.ID, questions.AnswerInput{Value: json.RawMessage(`"no"`), Note: "not tonight"})
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	waited, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{
		"title": "Deploy?", "kind": "confirm", "recommend": "yes", "wait": true,
	}})
	if err != nil || waited.IsError {
		t.Fatalf("wait: %+v %v", waited, err)
	}
	if got := waited.Content[0].(*mcp.TextContent).Text; !strings.Contains(got, "answered: Deploy? → No (note: not tonight)") {
		t.Fatalf("blocking result %q", got)
	}
}

func TestMCPSay(t *testing.T) {
	f := startSocket(t)
	session := connectMCP(t, NewClient(f.socket, questions.Caller{}))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "say", Arguments: map[string]any{"text": "Done."}})
	if err != nil || result.IsError {
		t.Fatalf("say %+v %v", result, err)
	}
}

func TestSettledText(t *testing.T) {
	if got := SettledText(questions.Question{ID: "q_1", Status: questions.StatusExpired}); !strings.Contains(got, "not consent") {
		t.Fatal(got)
	}
	if got := SettledText(questions.Question{ID: "q_1", Status: questions.StatusCancelled}); !strings.Contains(got, "not consent") {
		t.Fatal(got)
	}
}
