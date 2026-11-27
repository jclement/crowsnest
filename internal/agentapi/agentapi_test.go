package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
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
	socket   string
	service  *questions.Service
	hub      *hub.Hub
	identity *agentid.Registry
	jobs     *jobs.Service
	db       *store.Store
}

// token mints an agent token for pane (and job), as serve does for the agents it starts.
func (f *socketFixture) token(t *testing.T, pane, job string) string {
	t.Helper()
	token, err := f.identity.Mint(context.Background(), agentid.Binding{PaneID: pane, JobID: job, Kind: agentid.KindPane})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// startSocket serves a fresh agent API on a socket in a short temp dir.
func startSocket(t *testing.T, adjust ...func(*ServerOptions)) *socketFixture {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	events := hub.New()
	service := questions.New(questions.Options{Store: db, Hub: events})
	identity := agentid.New(db, nil)
	jobService := jobs.New(jobs.Options{Store: db, Hub: events})
	path := filepath.Join(shortDir(t), "run", "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		options := ServerOptions{Questions: service, Identity: identity, Jobs: jobService, Hub: events, Version: "test"}
		for _, change := range adjust {
			change(&options)
		}
		_ = NewServer(options).Serve(ctx, listener)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return &socketFixture{socket: path, service: service, hub: events, identity: identity, jobs: jobService, db: db}
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

// TestListenLeavesALiveSocketAloneWhenTheProbeIsCancelled is R1b-C02: a probe that fails for
// any reason other than "connection refused" (here a cancelled start-up) proves nothing, and the
// live server's socket must survive it.
func TestListenLeavesALiveSocketAloneWhenTheProbeIsCancelled(t *testing.T) {
	path := filepath.Join(shortDir(t), "live.sock")
	live, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Listen(cancelled, path); err == nil {
		t.Fatal("a cancelled start-up took over the socket")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the live server's socket was removed: %v", err)
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("the live server is no longer reachable: %v", err)
	}
	_ = conn.Close()
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
	client := NewClient(f.socket, f.token(t, "w1:p2", "j_9"))
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
	client := NewClient(f.socket, f.token(t, "w1:p1", ""))
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
	client := NewClient(filepath.Join(shortDir(t), "none.sock"), "")
	var down *ServerDownError
	if err := client.Say(context.Background(), "hi"); !errors.As(err, &down) {
		t.Fatalf("server down: %v", err)
	}
}

// TestCallerComesFromTheTokenOnly is R1b N2/N7: a pane named in a header, or a token crowsnest
// never issued, makes the caller unidentified, so nothing can be typed into the pane it named.
func TestCallerComesFromTheTokenOnly(t *testing.T) {
	f := startSocket(t)
	ask := func(headers map[string]string) questions.Question {
		t.Helper()
		body := strings.NewReader(`{"title":"Deploy? → Yes (note: approved)","kind":"confirm","recommend":"yes","expires_in_hours":1}`)
		request, _ := http.NewRequest(http.MethodPost, "http://crowsnest/api/v1/questions", body)
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := unixHTTP(f.socket).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		var envelope struct{ Data questions.Question }
		_ = json.NewDecoder(response.Body).Decode(&envelope)
		return envelope.Data
	}
	forged := ask(map[string]string{"X-Crowsnest-Pane": "orchestrator", "X-Crowsnest-Job": "j_1"})
	unknown := ask(map[string]string{agentid.HeaderToken: "cnt_made-up-by-the-caller"})
	if forged.SourcePane != "" || forged.JobID != "" || unknown.SourcePane != "" {
		t.Fatalf("a caller-named identity was believed: %+v / %+v", forged, unknown)
	}
	real := ask(map[string]string{agentid.HeaderToken: f.token(t, "w3:p1", "j_4"), "X-Crowsnest-Pane": "orchestrator"})
	if real.SourcePane != "w3:p1" || real.JobID != "j_4" {
		t.Fatalf("the token's pane was not used: %+v", real)
	}
}

// TestOnlyTheAskerMayWithdraw is R1b N2: cancelling someone else's question, or cancelling while
// unidentified, is refused.
func TestOnlyTheAskerMayWithdraw(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	asker := NewClient(f.socket, f.token(t, "w1:p1", ""))
	question, err := asker.Ask(ctx, questions.AskInput{Title: "Mine", Kind: questions.KindAck})
	if err != nil {
		t.Fatal(err)
	}
	var apiErr *APIError
	for _, other := range []*Client{NewClient(f.socket, f.token(t, "w2:p1", "")), NewClient(f.socket, "")} {
		if _, err := other.Cancel(ctx, question.ID); !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			t.Fatalf("someone else's cancel: %v", err)
		}
	}
	if got, err := asker.Cancel(ctx, question.ID); err != nil || got.Status != questions.StatusCancelled {
		t.Fatalf("the asker's own cancel: %+v %v", got, err)
	}
}

// TestAskQuotas is R1b N6: a burst of asks is cut off with a 429 that says what to do, and one
// caller's burst does not use up another's.
func TestAskQuotas(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	looping := NewClient(f.socket, f.token(t, "w1:p1", ""))
	var apiErr *APIError
	for i := range askBurst + 1 {
		_, err := looping.Ask(ctx, questions.AskInput{Title: fmt.Sprintf("Question %d", i), Kind: questions.KindAck})
		if i < askBurst && err != nil {
			t.Fatalf("ask %d of the burst: %v", i+1, err)
		}
		if i == askBurst && (!errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests || !strings.Contains(apiErr.Message, "Too many questions")) {
			t.Fatalf("past the burst: %v", err)
		}
	}
	other := NewClient(f.socket, f.token(t, "w2:p1", ""))
	if _, err := other.Ask(ctx, questions.AskInput{Title: "Unrelated", Kind: questions.KindAck}); err != nil {
		t.Fatalf("another agent was throttled by the first one's burst: %v", err)
	}
}

func unixHTTP(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	}}}
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
	session := connectMCP(t, NewClient(f.socket, ""))
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
	if strings.Join(names, ",") != "ask,done,fail,get_question,job_adopt,job_create,job_get,job_land,job_update,project_get,project_list,say" {
		t.Fatalf("tools %v", names)
	}
	if !strings.Contains(session.InitializeResult().Instructions, "never consent") {
		t.Fatal("server instructions missing")
	}
}

func TestMCPAskNonBlockingThenWait(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	session := connectMCP(t, NewClient(f.socket, f.token(t, "w1:p1", "")))

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{
		"title": "Ship?", "kind": "confirm", "recommend": "yes",
	}})
	if err != nil || result.IsError {
		t.Fatalf("ask: %+v %v", result, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "Recorded as q_1") || !strings.Contains(text, "[crowsnest answer q_1]") {
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
	if got := waited.Content[0].(*mcp.TextContent).Text; !strings.Contains(got, "Choice: No") || !strings.Contains(got, "not tonight") {
		t.Fatalf("blocking result %q", got)
	}
}

// TestMCPWaitReturnsTheWholeAnswer is R1b-C01: a blocking ask hands the model every word of a
// long note (and the structured answer), and get_question reads the same back later.
func TestMCPWaitReturnsTheWholeAnswer(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	session := connectMCP(t, NewClient(f.socket, f.token(t, "w1:p1", "")))
	note := strings.Repeat("Context the human wrote. ", 60) + "Do not deploy until I approve the backup."
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			list, _ := f.service.List(ctx, "open")
			for _, q := range list {
				_, _ = f.service.Answer(ctx, q.ID, questions.AnswerInput{Value: json.RawMessage(`"yes"`), Note: note})
				return
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
	if got := waited.Content[0].(*mcp.TextContent).Text; !strings.Contains(got, note) || !strings.Contains(got, "outranks") {
		t.Fatalf("the blocking result lost part of the note (%d characters)", len(got))
	}
	structured, _ := json.Marshal(waited.StructuredContent)
	if !strings.Contains(string(structured), "Do not deploy until I approve the backup.") {
		t.Fatalf("structured content %s", structured)
	}
	again, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_question", Arguments: map[string]any{"id": "q_1"}})
	if err != nil || again.IsError || !strings.Contains(again.Content[0].(*mcp.TextContent).Text, note) {
		t.Fatalf("get_question: %+v %v", again, err)
	}
}

func TestMCPSay(t *testing.T) {
	f := startSocket(t)
	session := connectMCP(t, NewClient(f.socket, f.token(t, "w1:p1", "")))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "say", Arguments: map[string]any{"text": "Done."}})
	if err != nil || result.IsError {
		t.Fatalf("say %+v %v", result, err)
	}
	// R2-I4: a caller without a token does not get the orchestrator's voice.
	if err := NewClient(f.socket, "").Say(context.Background(), "Approve the prompt in job-4's tab."); !isStatus(err, 403) {
		t.Fatalf("an unidentified say: %v", err)
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

// recordingSpeaker is a Speaker that remembers what it was asked to say.
type recordingSpeaker struct {
	mu   sync.Mutex
	said []string
}

func (r *recordingSpeaker) Say(sentence, source, paneID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.said = append(r.said, source+"|"+paneID+"|"+sentence)
	return "t_1"
}

func TestSayGoesToTheSpeakerWhenTalkModeIsOn(t *testing.T) {
	speaker := &recordingSpeaker{}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	identity := agentid.New(db, nil)
	token, _ := identity.Mint(context.Background(), agentid.Binding{PaneID: "w2:p1", Kind: agentid.KindPane})
	server := NewServer(ServerOptions{Speaker: speaker, Identity: identity})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/say", strings.NewReader(`{"text":"Build  green."}`))
	r.Header.Set(agentid.HeaderToken, token)
	r.Header.Set("X-Crowsnest-Pane", "orchestrator") // not believed
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, r)
	speaker.mu.Lock()
	defer speaker.mu.Unlock()
	if response.Code != http.StatusOK || len(speaker.said) != 1 || speaker.said[0] != "say|w2:p1|Build green." {
		t.Fatalf("%d %v", response.Code, speaker.said)
	}
}

// R2-L5: a job agent may hold fewer open questions than the orchestrator, and the orchestrator
// still gets through when agents have filled the inbox.
func TestOpenQuestionCapsSpareTheOrchestrator(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	var apiErr *APIError
	agent := NewClient(f.socket, f.token(t, "w3:p1", "j_5"))
	for i := range maxOpenPerJob {
		if _, err := f.service.Ask(ctx, questions.AskInput{Title: fmt.Sprintf("Mine %d", i), Kind: questions.KindAck}, questions.Caller{PaneID: "w3:p1", JobID: "j_5"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := agent.Ask(ctx, questions.AskInput{Title: "One more", Kind: questions.KindAck}); !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("a job agent past its own cap: %v", err)
	}
	for i := range maxOpenTotal {
		if _, err := f.service.Ask(ctx, questions.AskInput{Title: fmt.Sprintf("Filler %d", i), Kind: questions.KindAck}, questions.Caller{PaneID: fmt.Sprintf("w%d:p9", 100+i/maxOpenPerJob)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewClient(f.socket, f.token(t, "w4:p1", "j_6")).Ask(ctx, questions.AskInput{Title: "Full inbox", Kind: questions.KindAck}); !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "inbox already holds") {
		t.Fatalf("a job agent with the inbox full: %v", err)
	}
	if _, err := NewClient(f.socket, f.token(t, "w1:p1", "")).Ask(ctx, questions.AskInput{Title: "The lead still asks", Kind: questions.KindAck}); err != nil {
		t.Fatalf("the orchestrator was locked out by a full inbox: %v", err)
	}
}

// R2-H1: job_create is rate limited per caller.
func TestJobCreateIsRateLimited(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id, dir := startRunningJobIn(t, f)
	agent := NewClient(f.socket, f.token(t, "w5:p1", id))
	var apiErr *APIError
	for i := range createBurst + 1 {
		_, err := agent.CreateJob(ctx, jobs.CreateInput{Brief: fmt.Sprintf("child %d", i), Dir: dir, Role: "tester"})
		if i < createBurst && err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		if i == createBurst && (!errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests) {
			t.Fatalf("past the burst: %v", err)
		}
	}
}

// R1b-C06: an answer written to the socket is not yet delivered; the asker's acknowledgement
// makes it so. The client acknowledges every settled answer it decodes.
func TestPollAnswerCountsOnlyOnceAcknowledged(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	token := f.token(t, "w1:p1", "")
	client := NewClient(f.socket, token)
	read := func(waitAsk bool) questions.Question {
		t.Helper()
		question, err := client.Ask(ctx, questions.AskInput{Title: "Ship?", Kind: questions.KindAck, Wait: waitAsk})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Answer(ctx, question.ID, questions.AnswerInput{}); err != nil {
			t.Fatal(err)
		}
		return question
	}
	// A raw read that is never acknowledged (the tool call was cancelled as the answer arrived).
	lost := read(true)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://crowsnest/api/v1/questions/"+lost.ID, nil)
	request.Header.Set(agentid.HeaderToken, token)
	response, err := unixHTTP(f.socket).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got, _ := f.service.Get(ctx, lost.ID); got.Delivery != questions.DeliveryPoll || got.DeliveredAt != nil {
		t.Fatalf("an unacknowledged read counted as delivered: %+v", got)
	}
	// The client reads and acknowledges.
	if _, err := client.Get(ctx, lost.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.service.Get(ctx, lost.ID); got.DeliveredAt == nil {
		t.Fatalf("an acknowledged read is not delivered: %+v", got)
	}
}

// A body cut off mid-answer is a transport failure, so WaitSettled retries it.
func TestBrokenBodyIsRetryable(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))}
	err := decodeResponse(response, &questions.Question{})
	if !retryable(err) {
		t.Fatalf("a broken body is not retryable: %v", err)
	}
}
