//go:build integration

// The Codex test (DESIGN.md 20.1): what crowsnest claims for a Codex job, checked against the real
// codex CLI installed here. Codex job agents are launched by the real launcher in a throwaway
// crowsnest-test-* herdr session, with crowsnest's hooks handed to their session, in a scratch git
// repository under this package directory:
//
//	(a) an agent that writes a file and declares done ends done, with no protocol call asking for
//	    approval; its SessionStart hook reached crowsnest (the timeline says so) and told it the
//	    thread id, and its spend is measured from its rollout (tokens, no cost). (Done ends the
//	    agent mid-turn, so its Stop hook is (b)'s to prove.)
//	(b) an agent told to end its turn without declaring is refused by the Stop hook (a nudge, at
//	    once, not after the herdr backstop's idle wait) and goes on;
//	(c) a write outside the workspace needs escalation from Codex's sandbox; the request reaches
//	    the browser as a structured approval through the PermissionRequest hook; allowed through
//	    the API, it runs, and the job ends done. (Network is on in a hooked Codex job's sandbox:
//	    it is how the shell reaches crowsnest's socket, so no protocol call needs approval.)
//	(d) a command naming a protected host is refused by the PreToolUse guard, with no approval.
//
// Codex's own folder-trust prompt, if the scratch repository meets one, is answered the way the
// browser's card answers it: the screen's dialog.trust option, sent through POST /keys, which must
// report the dialog gone.
//
// Opt-in and paid (cheap): -tags integration and CROWSNEST_E2E_CODEX=1 (`mise run test:codex`).
// CROWSNEST_CODEX_MODEL picks the model (default gpt-5.6-luna); CROWSNEST_CODEX_TEST_OUT names a
// file the timelines are written to.

package jobs_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

type codexRig struct {
	*approvalRig
	codexHome string
	model     string
}

func TestIntegrationCodex(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CODEX") != "1" {
		t.Skip("real-Codex test: set CROWSNEST_E2E_CODEX=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "codex", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	r := newCodexRig(t)
	t.Run("a_done_with_hooks_and_spend", r.testDone)
	t.Run("b_undeclared_stop_is_refused", r.testUndeclaredStop)
	t.Run("c_approval_through_the_browser", r.testApproval)
	t.Run("d_guard_refuses_a_protected_host", r.testGuard)
	if path := os.Getenv("CROWSNEST_CODEX_TEST_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(r.out.String()), 0o600)
	}
}

func newCodexRig(t *testing.T) *codexRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	short, err := os.MkdirTemp("/tmp", "cn-codex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	binary := buildCrowsnest(t, short)
	session := startSession(t)
	scratch, err := filepath.Abs("e2e-codex-" + randomSuffix(t))
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(scratch, "repo")
	gitInit(t, repo)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		home, _ := os.UserHomeDir()
		codexHome = filepath.Join(home, ".codex")
	}
	claudeDir, _ := transcript.ConfigDir(os.Getenv)
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	events := hub.New()
	socket := filepath.Join(short, "agent.sock")
	identity := agentid.New(db, nil)
	rules := &guard.Rules{Session: session.Name, ProtectedHosts: []string{protectedTestHost}}

	var jobService *jobs.Service
	approvalService := approvals.New(approvals.Options{
		Store: db, Hub: events, Logger: logger,
		RiskContext: func(context.Context, string) risk.Context { return risk.Context{ProtectedHosts: rules.ProtectedHosts} },
		OnChange: func(ctx context.Context, a approvals.Approval, change string) {
			_ = jobService.SetApprovalWaiting(context.WithoutCancel(ctx), a.JobID, change == "requested", a.Summary)
		},
	})
	questionService := questions.New(questions.Options{Store: db, Hub: events, Logger: logger})
	meter := spend.NewMeter(spend.Options{Store: db, Prices: spend.Embedded(), ClaudeConfigDir: claudeDir, CodexHome: codexHome, Logger: logger, Interval: time.Second,
		Sources: func(ctx context.Context) ([]spend.Source, error) { return jobService.SpendSources(ctx) }})
	jobService = jobs.New(jobs.Options{
		Store: db, Hub: events, Logger: logger, Herdr: session.Client, Spend: meter,
		ApprovalPending: approvalService.PendingForJob,
		Launch: jobs.LaunchOptions{
			StateDir: filepath.Join(short, "state"), Executable: binary, AgentSocket: socket, BinDir: filepath.Dir(binary),
			BasePath: os.Getenv("PATH"), Session: session.Name, ClaudeConfigDir: claudeDir,
			WorktreeRoot: filepath.Join(scratch, "worktrees"), Identity: identity, Guard: rules,
		},
	})
	listener, err := agentapi.Listen(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = agentapi.NewServer(agentapi.ServerOptions{Questions: questionService, Jobs: jobService, Identity: identity, Hub: events, Logger: logger, Approvals: approvalService}).Serve(ctx, listener)
	}()
	go jobService.Run(ctx, 2*time.Second)
	go approvalService.Run(ctx, nil)
	go meter.Run(ctx)

	authService, err := auth.New(ctx, auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: events, Logger: slog.New(slog.DiscardHandler), Version: "integration",
		Herdr: session.Client, Questions: questionService, Jobs: jobService, Approvals: approvalService,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	model := os.Getenv("CROWSNEST_CODEX_MODEL")
	if model == "" {
		model = "gpt-5.6-luna"
	}
	stop := &stopRig{t: t, ts: ts, jobs: jobService, client: session.Client, repo: repo, session: session.Name, meter: meter, claudeDir: claudeDir}
	return &codexRig{approvalRig: &approvalRig{stopRig: stop, approvals: approvalService}, codexHome: codexHome, model: model}
}

// create gives a Codex job in the scratch repository.
func (r *codexRig) create(t *testing.T, brief string) jobs.Job {
	t.Helper()
	var job jobs.Job
	if code := r.call(http.MethodPost, "/api/v1/jobs", map[string]any{"brief": brief, "dir": r.repo, "engine": "codex", "model": r.model}, &job); code != http.StatusCreated {
		t.Fatalf("creating the job: %d", code)
	}
	t.Logf("created %s (codex, %s)", job.ID, r.model)
	return job
}

// waitFor is stopRig.waitFor for Codex: a trust prompt is answered through the browser's own path
// (the screen's dialog.trust, POST /keys), and pending approvals are allowed as a human would.
func (r *codexRig) waitFor(t *testing.T, id string, limit time.Duration, allow bool, done func(jobs.Detail) bool) jobs.Detail {
	t.Helper()
	deadline := time.Now().Add(limit)
	var detail jobs.Detail
	for time.Now().Before(deadline) {
		detail = r.get(t, id)
		if done(detail) {
			return detail
		}
		if detail.State == jobs.StateNeedsYou && detail.Cause == jobs.CauseDialog && r.answerTrustCard(t, detail) {
			continue
		}
		if allow {
			r.allowFollowUps(t, id, "")
		}
		time.Sleep(2 * time.Second)
	}
	r.dumpScreen(t, detail.PaneID)
	r.record(t, "timed out", detail)
	t.Fatalf("job %s: condition not met within %s; state %s/%s", id, limit, detail.State, detail.Cause)
	return detail
}

// answerTrustCard presses the trust card's "Trust this folder" through the API, once per pane.
func (r *codexRig) answerTrustCard(t *testing.T, d jobs.Detail) bool {
	if d.PaneID == "" || r.trusted[d.PaneID] {
		return false
	}
	var screen httpd.ScreenResponse
	if code := r.call(http.MethodGet, "/api/v1/agents/"+d.PaneID+"/screen", nil, &screen); code != http.StatusOK || screen.Dialog.Trust == nil {
		return false
	}
	if r.trusted == nil {
		r.trusted = map[string]bool{}
	}
	r.trusted[d.PaneID] = true
	trust := screen.Dialog.Trust
	var reply httpd.KeysResponse
	code := r.call(http.MethodPost, "/api/v1/agents/"+d.PaneID+"/keys", map[string]any{"keys": trust.Allow.Keys, "screen_hash": screen.Hash}, &reply)
	if code != http.StatusOK || reply.DialogGone == nil || !*reply.DialogGone {
		r.dumpScreen(t, d.PaneID)
		t.Fatalf("answering %s's trust card (%s, %s, keys %v): %d, dialog_gone %v", trust.Engine, trust.Folder, trust.Allow.Label, trust.Allow.Keys, code, reply.DialogGone)
	}
	_ = r.jobs.Journal(context.Background(), d.ID, jobs.EventHook, "Test harness (as the human) pressed Trust this folder on the "+trust.Engine+" trust card", nil)
	t.Logf("%s: trusted %s through the card (%v)", d.ID, trust.Folder, trust.Allow.Keys)
	return true
}

func (r *codexRig) testDone(t *testing.T) {
	job := r.create(t, "Use the shell to run exactly: echo beta > b.txt\nThen declare the job done with the crowsnest command your brief file describes, with a one-line result.")
	detail := r.waitFor(t, job.ID, 5*time.Minute, false, func(d jobs.Detail) bool {
		r.noProtocolApprovals(t, d.ID)
		return d.State.Terminal()
	})
	r.record(t, "(a) codex done", detail)
	if detail.State != jobs.StateDone {
		t.Fatalf("the job did not end done: %+v", detail.Job)
	}
	if body, err := os.ReadFile(filepath.Join(detail.WorkDir, "b.txt")); err != nil || !strings.Contains(string(body), "beta") {
		t.Errorf("b.txt: %q %v", body, err)
	}
	sessionHook := false
	for _, event := range detail.Timeline {
		if event.Kind == jobs.EventHook && strings.HasPrefix(event.Summary, "Session started") {
			sessionHook = true
		}
	}
	if !sessionHook {
		t.Errorf("Codex's SessionStart hook never reached crowsnest")
	}
	if detail.SessionID == "" {
		t.Fatalf("the job's Codex thread id was never learned from its hooks")
	}
	rollout, err := transcript.LocateCodex(r.codexHome, detail.SessionID)
	if err != nil {
		t.Fatalf("no rollout for thread %s: %v", detail.SessionID, err)
	}
	t.Logf("thread %s, rollout %s", detail.SessionID, rollout)
	deadline := time.Now().Add(30 * time.Second)
	var summary spend.Summary
	for time.Now().Before(deadline) {
		summary, _ = r.meter.SpendOf(context.Background(), spend.JobInfo{ID: job.ID, Engine: "codex", SessionKnown: true, StartedAt: detail.StartedAt, SettledAt: detail.SettledAt})
		if summary.Measured && summary.TotalTokens > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	t.Logf("spend: %d tokens (%d fresh in, %d cached, %d out), %d calls, cost %v, models %v", summary.TotalTokens, summary.InputTokens, summary.CacheReadTokens, summary.OutputTokens, summary.Turns, summary.CostUSD, summary.Models)
	if !summary.Measured || summary.TotalTokens == 0 {
		t.Errorf("the Codex job's spend was not measured: %+v", summary)
	}
	if summary.CostUSD != nil {
		t.Errorf("a cost was estimated for an OpenAI model crowsnest has no price for: %v", *summary.CostUSD)
	}
}

func (r *codexRig) testUndeclaredStop(t *testing.T) {
	job := r.create(t, "Use the shell to run exactly: echo alpha > a.txt\nThen end your turn and wait. Do not run any crowsnest command.")
	detail := r.waitFor(t, job.ID, 4*time.Minute, true, func(d jobs.Detail) bool {
		return d.Nudges >= 1 || d.State.Terminal()
	})
	r.record(t, "(b) codex undeclared stop", detail)
	if detail.Nudges < 1 || detail.Turns < 1 {
		t.Fatalf("the Stop hook did not refuse the stop (nudges %d, turns %d): %+v", detail.Nudges, detail.Turns, detail.Job)
	}
	// The refusal makes Codex continue: another turn ends after the nudge.
	detail = r.waitFor(t, job.ID, 3*time.Minute, true, func(d jobs.Detail) bool { return d.Turns >= 2 || d.State.Terminal() })
	r.record(t, "(b) codex after the nudge", detail)
	_, _ = r.jobs.Cancel(context.Background(), job.ID)
}

func (r *codexRig) testApproval(t *testing.T) {
	outside := filepath.Join(filepath.Dir(r.repo), "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "escalated.txt")
	job := r.create(t, "Use the shell to run exactly: echo escalated > "+target+"\nThat file is outside your workspace, so your sandbox will refuse the write: request escalated permission for exactly that command. "+
		"When it has run, declare the job done with the crowsnest command your brief file describes, with a one-line result.")
	var approval approvals.Approval
	r.waitFor(t, job.ID, 4*time.Minute, false, func(d jobs.Detail) bool {
		var pending []approvals.Approval
		r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
		for _, a := range pending {
			if a.JobID == job.ID && strings.Contains(string(a.ToolInput), "escalated.txt") {
				approval = a
				return true
			}
			if a.JobID == job.ID {
				t.Logf("other approval %s: %s (allowed)", a.ID, a.Summary)
				r.decide(t, a.ID, "allow", "")
			}
		}
		if d.State.Terminal() {
			t.Fatalf("the job ended without asking: %+v", d.Job)
		}
		return false
	})
	t.Logf("approval %s: %s (%s)", approval.ID, approval.Summary, approval.ToolName)
	if approval.ToolName != "Bash" {
		t.Errorf("tool = %q, want Bash", approval.ToolName)
	}
	r.decide(t, approval.ID, "allow", "")
	detail := r.waitFor(t, job.ID, 4*time.Minute, true, func(d jobs.Detail) bool { return d.State.Terminal() })
	r.record(t, "(c) codex approval", detail)
	if detail.State != jobs.StateDone {
		t.Fatalf("the job did not end done after the approval: %+v", detail.Job)
	}
	if settled, _ := r.approvals.Get(context.Background(), approval.ID); settled.Status != approvals.StatusAllowed {
		t.Errorf("approval ended %s", settled.Status)
	}
	if body, err := os.ReadFile(target); err != nil || !strings.Contains(string(body), "escalated") {
		t.Errorf("the allowed command did not run: %q %v", body, err)
	}
}

func (r *codexRig) testGuard(t *testing.T) {
	job := r.create(t, "Use the shell to run exactly: ssh "+protectedTestHost+" true\nIf it is refused, do not retry it: declare the job done with the crowsnest command your brief file describes, with the refusal message as the result.")
	detail := r.waitFor(t, job.ID, 4*time.Minute, false, func(d jobs.Detail) bool {
		for _, event := range d.Timeline {
			if strings.Contains(strings.ToLower(event.Summary), "guard") {
				return true
			}
		}
		return d.State.Terminal()
	})
	r.record(t, "(d) codex guard", detail)
	found := false
	for _, event := range detail.Timeline {
		if strings.Contains(strings.ToLower(event.Summary), "guard") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the guard's refusal is not on the timeline: %+v", detail.Job)
	}
	var pending []approvals.Approval
	r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
	for _, a := range pending {
		if a.JobID == job.ID && strings.Contains(string(a.ToolInput), protectedTestHost) {
			t.Errorf("an approval was asked for a command the guard refuses: %+v", a)
		}
	}
	_, _ = r.jobs.Cancel(context.Background(), job.ID)
}

// noProtocolApprovals fails if a crowsnest protocol call asked for approval: a hooked Codex job's
// shell reaches the agent socket without escalation.
func (r *codexRig) noProtocolApprovals(t *testing.T, jobID string) {
	t.Helper()
	var pending []approvals.Approval
	r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
	for _, a := range pending {
		if a.JobID == jobID {
			r.decide(t, a.ID, "allow", "")
			var input struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(a.ToolInput, &input)
			if strings.HasPrefix(strings.TrimSpace(input.Command), "crowsnest ") && !strings.ContainsAny(input.Command, "&|;") {
				t.Errorf("a crowsnest protocol call needed approval: %s", a.Summary)
			} else {
				t.Logf("approval %s allowed: %s", a.ID, a.Summary)
			}
		}
	}
}
