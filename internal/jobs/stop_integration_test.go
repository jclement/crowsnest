//go:build integration

// The stop test (DESIGN.md section 15): the proof that jobs do not stop silently. Real Claude
// (haiku) agents are launched as jobs by the real launcher in a throwaway crowsnest-test-* herdr
// session, with crowsnest's hooks, prompt file and MCP server, in a scratch git repository under
// this package directory (so Claude's folder trust, inherited from the checkout, applies):
//
//	(a) an agent told to stop without declaring anything is refused by the Stop hook (a nudge on
//	    the timeline) and its turn continues (another Stop arrives after the nudge);
//	(b) an agent that creates a file and calls done ends done, with a result, the file, and no
//	    nudge;
//	(c) an agent that asks a non-blocking question keeps going; the question is answered through
//	    the browser API, the answer is typed back into the agent, and the job ends done.
//
// Opt-in and paid: -tags integration and CROWSNEST_E2E_CLAUDE=1 (`mise run test:claude`).
// CROWSNEST_STOP_TEST_OUT names a file the three timelines are written to (for the report).

package jobs_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

var testSessionPattern = regexp.MustCompile(`^crowsnest-test-[a-z0-9]+$`)

type stopRig struct {
	t         *testing.T
	ts        *httptest.Server
	jobs      *jobs.Service
	client    herdr.Client
	repo      string
	outMu     sync.Mutex
	out       strings.Builder
	session   string
	trusted   map[string]bool
	trustedAt map[string]time.Time
}

func TestIntegrationClaudeStop(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	r := newStopRig(t)
	t.Run("a_undeclared_stop_is_refused", r.testUndeclaredStop)
	t.Run("b_done_stops_cleanly", r.testDone)
	t.Run("c_ask_answer_done", r.testAskAnswerDone)
	if path := os.Getenv("CROWSNEST_STOP_TEST_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(r.out.String()), 0o600)
	}
}

func newStopRig(t *testing.T) *stopRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	short, err := os.MkdirTemp("/tmp", "cn-stop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	binary := buildCrowsnest(t, short)
	session := startSession(t)

	scratch, err := filepath.Abs("e2e-stop-" + randomSuffix(t))
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(scratch, "repo")
	gitInit(t, repo)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })

	claudeDir, err := transcript.ConfigDir(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	events := hub.New()
	socket := filepath.Join(short, "agent.sock")

	var jobService *jobs.Service
	var deliverer *questions.Deliverer
	questionService := questions.New(questions.Options{Store: db, Hub: events, Logger: logger,
		OnAsked: func(q questions.Question) {
			if q.JobID != "" {
				go jobService.QuestionChanged(context.Background(), q.JobID, q.ID, q.Title, "asked", q.Wait)
			}
		},
		OnSettled: func(q questions.Question) {
			deliverer.Kick()
			if q.JobID != "" {
				go jobService.QuestionChanged(context.Background(), q.JobID, q.ID, q.Title, string(q.Status), q.Wait)
			}
		},
	})
	deliverer = questions.NewDeliverer(questions.DelivererOptions{Store: db, Prompt: questions.HerdrPrompter(session.Client), Logger: logger, Interval: time.Second})
	go deliverer.Run(ctx)
	identity := agentid.New(db, nil) // the hooks and tools are identified by their tokens, as in serve
	jobService = jobs.New(jobs.Options{
		Store: db, Hub: events, Logger: logger, Herdr: session.Client,
		Launch: jobs.LaunchOptions{
			StateDir: filepath.Join(short, "state"), Executable: binary, AgentSocket: socket, BinDir: filepath.Dir(binary),
			BasePath: os.Getenv("PATH"), Session: session.Name, PermissionMode: "auto", ClaudeConfigDir: claudeDir,
			WorktreeRoot: filepath.Join(scratch, "worktrees"), Identity: identity,
		},
	})
	listener, err := agentapi.Listen(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = agentapi.NewServer(agentapi.ServerOptions{Questions: questionService, Jobs: jobService, Identity: identity, Hub: events, Logger: logger}).Serve(ctx, listener)
	}()
	go jobService.Run(ctx, 2*time.Second)

	authService, err := auth.New(ctx, auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: events, Logger: slog.New(slog.DiscardHandler), Version: "integration",
		Herdr: session.Client, Questions: questionService, Jobs: jobService,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return &stopRig{t: t, ts: ts, jobs: jobService, client: session.Client, repo: repo, session: session.Name}
}

// ---- The three parts -----------------------------------------------------------------------

func (r *stopRig) testUndeclaredStop(t *testing.T) {
	job := r.create(t, "Create a file named a.txt containing the single word alpha. Then stop and wait: end your turn without calling any tool and do not call done.")
	detail := r.waitFor(t, job.ID, 4*time.Minute, func(d jobs.Detail) bool {
		return stopsAfterFirstNudge(d) >= 1 || d.State.Terminal()
	})
	r.record(t, "(a) undeclared stop", detail)
	if detail.Nudges < 1 || !hasKind(detail, jobs.EventNudge) {
		t.Fatalf("the Stop hook never refused the stop: %+v", detail.Job)
	}
	if stopsAfterFirstNudge(detail) < 1 && !detail.State.Terminal() {
		t.Fatalf("the turn did not continue after the nudge")
	}
	if _, err := os.Stat(filepath.Join(detail.WorkDir, "a.txt")); err != nil {
		t.Errorf("a.txt was not created in %s: %v", detail.WorkDir, err)
	}
}

func (r *stopRig) testDone(t *testing.T) {
	job := r.create(t, "Create a file named b.txt containing exactly the word hello. Then call your crowsnest done tool with a one-line result, verified listing what you checked, and not_verified as an empty list.")
	detail := r.waitFor(t, job.ID, 4*time.Minute, func(d jobs.Detail) bool { return d.State.Terminal() })
	r.record(t, "(b) done", detail)
	if detail.State != jobs.StateDone || strings.TrimSpace(detail.ResultMD) == "" {
		t.Fatalf("job did not end done with a result: %+v", detail.Job)
	}
	body, err := os.ReadFile(filepath.Join(detail.WorkDir, "b.txt"))
	if err != nil || !strings.Contains(string(body), "hello") {
		t.Fatalf("b.txt: %q %v", body, err)
	}
	if detail.Nudges != 0 {
		t.Errorf("a job that called done was nudged %d time(s)", detail.Nudges)
	}
}

func (r *stopRig) testAskAnswerDone(t *testing.T) {
	job := r.create(t, "First call your crowsnest ask tool once, non-blocking, with title \"Pick a colour\", kind choice, options red (label Red) and blue (label Blue), recommend red. "+
		"Do not wait for the answer: next create a file named c.txt containing the word waiting. If nothing else is left, declare that you are waiting on the question with job_update (waiting true) and end your turn. "+
		"When the answer arrives as a message starting [crowsnest answer, write the chosen colour's key into colour.txt and call done.")
	question := r.waitForQuestion(t, job.ID)
	t.Logf("question %s asked under %s: %s", question.ID, question.JobID, question.Title)
	if question.Wait {
		t.Errorf("the ask blocked; it should not")
	}
	// Let the agent carry on (c.txt) before answering, as a human would answer later.
	r.waitFor(t, job.ID, 3*time.Minute, func(d jobs.Detail) bool {
		_, err := os.Stat(filepath.Join(d.WorkDir, "c.txt"))
		return err == nil
	})
	if code := r.call(http.MethodPost, "/api/v1/questions/"+question.ID+"/answer", map[string]any{"answer_value": "blue"}, nil); code != http.StatusOK {
		t.Fatalf("answering: %d", code)
	}
	detail := r.waitFor(t, job.ID, 4*time.Minute, func(d jobs.Detail) bool { return d.State.Terminal() })
	r.record(t, "(c) ask, answer, done", detail)
	if detail.State != jobs.StateDone {
		t.Fatalf("job did not end done: %+v", detail.Job)
	}
	body, err := os.ReadFile(filepath.Join(detail.WorkDir, "colour.txt"))
	if err != nil || !strings.Contains(strings.ToLower(string(body)), "blue") {
		t.Fatalf("colour.txt: %q %v (the answer did not reach the agent)", body, err)
	}
}

// ---- Helpers -------------------------------------------------------------------------------

func (r *stopRig) create(t *testing.T, brief string) jobs.Job {
	t.Helper()
	var job jobs.Job
	if code := r.call(http.MethodPost, "/api/v1/jobs", map[string]any{"brief": brief, "dir": r.repo, "model": "haiku"}, &job); code != http.StatusCreated {
		t.Fatalf("creating the job: %d", code)
	}
	t.Logf("created %s", job.ID)
	return job
}

func (r *stopRig) get(t *testing.T, id string) jobs.Detail {
	var detail jobs.Detail
	r.call(http.MethodGet, "/api/v1/jobs/"+id, nil, &detail)
	return detail
}

func (r *stopRig) waitFor(t *testing.T, id string, limit time.Duration, done func(jobs.Detail) bool) jobs.Detail {
	t.Helper()
	deadline := time.Now().Add(limit)
	var detail jobs.Detail
	for time.Now().Before(deadline) {
		detail = r.get(t, id)
		if done(detail) {
			return detail
		}
		if detail.State == jobs.StateNeedsYou && detail.Cause == jobs.CauseDialog && r.answerTrust(t, detail) {
			time.Sleep(2 * time.Second)
			continue
		}
		if at, ok := r.trustedAt[detail.PaneID]; ok && detail.State == jobs.StateNeedsYou && time.Since(at) < 30*time.Second {
			time.Sleep(2 * time.Second) // the supervisor tick sends the held brief once the agent is ready
			continue
		}
		if detail.State == jobs.StateNeedsYou && detail.Cause == jobs.CauseDialog {
			r.dumpScreen(t, detail.PaneID)
			r.record(t, "stuck at a dialog", detail)
			t.Fatalf("job %s stopped at a dialog: %s", id, detail.CauseDetail)
		}
		time.Sleep(2 * time.Second)
	}
	r.dumpScreen(t, detail.PaneID)
	r.record(t, "timed out", detail)
	t.Fatalf("job %s: condition not met within %s; state %s", id, limit, detail.State)
	return detail
}

// answerTrust plays the human at the terminal: Claude asks whether to trust the fresh scratch
// worktree (trust is per folder), and crowsnest rightly never answers that itself. The test
// accepts it once per pane, as Jeff would in the browser's terminal, and journals that it did.
func (r *stopRig) answerTrust(t *testing.T, d jobs.Detail) bool {
	if d.PaneID == "" || r.trusted[d.PaneID] {
		return false
	}
	read, err := r.client.PaneRead(context.Background(), d.PaneID, herdr.ReadOptions{Source: herdr.SourceVisible})
	if err != nil || !strings.Contains(read.Text, "Yes, I trust this folder") {
		return false
	}
	if r.trusted == nil {
		r.trusted = map[string]bool{}
	}
	r.trusted[d.PaneID] = true
	if r.trustedAt == nil {
		r.trustedAt = map[string]time.Time{}
	}
	r.trustedAt[d.PaneID] = time.Now()
	if err := r.client.PaneSendKeys(context.Background(), d.PaneID, []string{"down", "Enter"}); err != nil {
		t.Fatalf("answering the trust dialog: %v", err)
	}
	_ = r.jobs.Journal(context.Background(), d.ID, jobs.EventHook, "Test harness (as the human) accepted Claude's folder-trust dialog in the terminal", nil)
	t.Logf("%s: accepted the folder-trust dialog in %s", d.ID, d.PaneID)
	return true
}

func (r *stopRig) waitForQuestion(t *testing.T, jobID string) questions.Question {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var open []questions.Question
		r.call(http.MethodGet, "/api/v1/questions?status=open", nil, &open)
		for _, q := range open {
			if q.JobID == jobID {
				return q
			}
		}
		if d := r.get(t, jobID); d.State == jobs.StateNeedsYou && d.Cause == jobs.CauseDialog {
			r.answerTrust(t, d)
		}
		time.Sleep(2 * time.Second)
	}
	r.record(t, "no question", r.get(t, jobID))
	t.Fatal("no question arrived for the job within 3 minutes")
	return questions.Question{}
}

func (r *stopRig) dumpScreen(t *testing.T, paneID string) {
	if paneID == "" {
		return
	}
	read, err := r.client.PaneRead(context.Background(), paneID, herdr.ReadOptions{Source: herdr.SourceVisible})
	if err == nil {
		t.Logf("screen of %s:\n%s", paneID, read.Text)
	}
}

// record logs a job's timeline and keeps it for the report.
func (r *stopRig) record(t *testing.T, label string, d jobs.Detail) {
	r.outMu.Lock()
	defer r.outMu.Unlock()
	fmt.Fprintf(&r.out, "### %s: %s, state %s, nudges %d\n\n", label, d.ID, d.State, d.Nudges)
	start := d.CreatedAt
	for _, event := range d.Timeline {
		line := fmt.Sprintf("+%5.1fs  %-9s %s", event.At.Sub(start).Seconds(), event.Kind, event.Summary)
		fmt.Fprintln(&r.out, line)
		t.Log(line)
	}
	if d.ResultMD != "" {
		fmt.Fprintf(&r.out, "\nresult: %s\nverified: %v\nnot verified: %v\n", d.ResultMD, d.Verified, d.NotVerified)
	}
	fmt.Fprintln(&r.out)
}

// stopsAfterFirstNudge counts Stop outcomes (a nudge or an allowed end) after the first nudge:
// one means Claude continued after the refusal and reached the end of a turn again.
func stopsAfterFirstNudge(d jobs.Detail) int {
	seen, count := false, 0
	for _, event := range d.Timeline {
		isStop := event.Kind == jobs.EventNudge || (event.Kind == jobs.EventHook && strings.HasPrefix(event.Summary, "Turn ended")) || event.Kind == jobs.EventDone
		if !isStop {
			continue
		}
		if seen {
			count++
		}
		if event.Kind == jobs.EventNudge {
			seen = true
		}
	}
	return count
}

func hasKind(d jobs.Detail, kind string) bool {
	for _, event := range d.Timeline {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func (r *stopRig) call(method, path string, body, out any) int {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, r.ts.URL+path, reader)
	request.Header.Set("Origin", r.ts.URL)
	request.Header.Set("X-Crowsnest", "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		r.t.Logf("%s %s: %v", method, path, err)
		return 0
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	if out != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Data != nil {
			_ = json.Unmarshal(envelope.Data, out)
		}
	}
	if response.StatusCode >= 300 {
		r.t.Logf("%s %s = %d %s", method, path, response.StatusCode, raw)
	}
	return response.StatusCode
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("scratch repo for the crowsnest stop test\n"), 0o644)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "README.md"},
		{"-c", "user.name=crowsnest-test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func buildCrowsnest(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(dir, "crowsnest")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/crowsnest")
	build.Env = append(herdr.ScrubEnv(os.Environ()), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building crowsnest: %v\n%s", err, output)
	}
	return binary
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 5)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(suffix)
}

func startSession(t *testing.T) *herdr.Session {
	t.Helper()
	name := "crowsnest-test-" + randomSuffix(t)
	if !testSessionPattern.MatchString(name) {
		t.Fatalf("refusing session %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := herdr.EnsureSession(ctx, herdr.SessionOptions{Name: name, LogDir: t.TempDir(), StartTimeout: 20 * time.Second})
	t.Cleanup(func() { stopSession(t, name) })
	if err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	if !session.Started {
		t.Fatalf("session %s was already running", name)
	}
	t.Logf("herdr session %s (herdr %s)", name, session.Pong.Version)
	return session
}

func stopSession(t *testing.T, name string) {
	t.Helper()
	if !testSessionPattern.MatchString(name) {
		t.Errorf("refusing to tear down %q", name)
		return
	}
	for _, args := range [][]string{{"session", "stop", name, "--json"}, {"session", "delete", name, "--json"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "herdr", args...)
		cmd.Env = herdr.ScrubEnv(os.Environ())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("herdr %s: %v: %s", strings.Join(args, " "), err, output)
		}
		cancel()
	}
}
