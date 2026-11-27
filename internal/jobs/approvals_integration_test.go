//go:build integration

// The approvals test (DESIGN.md 8.7): structured approvals and the guard with real Claude (haiku)
// job agents, started by the real launcher in a throwaway crowsnest-test-* herdr session, in the
// `default` permission mode (so a shell command that writes asks for permission):
//
//	(d) the agent runs a harmless Bash command; the PermissionRequest hook posts an approval, the
//	    job goes needs_you/approval-tool, the test allows it through the browser API (never
//	    touching the terminal), the command runs and the job ends done;
//	(e) a second command is denied through the API with a note; the agent receives the note (it
//	    quotes it in its result);
//	(f) a command naming a protected host is refused by the PreToolUse guard with no approval
//	    asked, and the refusal is journalled on the job.
//
// Opt-in and paid, like the stop test: -tags integration and CROWSNEST_E2E_CLAUDE=1 (`mise run
// test:claude`). CROWSNEST_APPROVAL_TEST_OUT names a file the timelines are written to.

package jobs_test

import (
	"context"
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
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// protectedTestHost is the guard's protected host in this test: an invalid name, so a guard that
// failed would only reach nowhere.
const protectedTestHost = "nyc-guard-test.invalid"

type approvalRig struct {
	*stopRig
	approvals *approvals.Service
}

func TestIntegrationClaudeApprovals(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	r := newApprovalRig(t)
	t.Run("d_allowed_through_the_api", r.testAllowed)
	t.Run("e_denied_with_a_note", r.testDenied)
	t.Run("f_guard_refuses", r.testGuard)
	if path := os.Getenv("CROWSNEST_APPROVAL_TEST_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(r.out.String()), 0o600)
	}
}

func newApprovalRig(t *testing.T) *approvalRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	short, err := os.MkdirTemp("/tmp", "cn-appr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	binary := buildCrowsnest(t, short)
	session := startSession(t)
	scratch, err := filepath.Abs("e2e-appr-" + randomSuffix(t))
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
	jobService = jobs.New(jobs.Options{
		Store: db, Hub: events, Logger: logger, Herdr: session.Client,
		ApprovalPending: approvalService.PendingForJob,
		Launch: jobs.LaunchOptions{
			StateDir: filepath.Join(short, "state"), Executable: binary, AgentSocket: socket, BinDir: filepath.Dir(binary),
			BasePath: os.Getenv("PATH"), Session: session.Name, PermissionMode: "default", ClaudeConfigDir: claudeDir,
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
	rig := &stopRig{t: t, ts: ts, jobs: jobService, client: session.Client, repo: repo, session: session.Name, claudeDir: claudeDir}
	return &approvalRig{stopRig: rig, approvals: approvalService}
}

// pendingFor waits for the job's next pending approval, accepting folder trust on the way (as the
// stop test does), and fails if the job ends or stops at a terminal dialog with no approval.
func (r *approvalRig) pendingFor(t *testing.T, jobID string, limit time.Duration) approvals.Approval {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		var pending []approvals.Approval
		r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
		for _, approval := range pending {
			if approval.JobID == jobID {
				return approval
			}
		}
		detail := r.get(t, jobID)
		if detail.State.Terminal() {
			r.record(t, "ended with no approval", detail)
			t.Fatalf("job %s ended before asking for an approval", jobID)
		}
		if detail.State == jobs.StateNeedsYou && detail.Cause == jobs.CauseDialog {
			r.answerTrust(t, detail)
		}
		time.Sleep(time.Second)
	}
	detail := r.get(t, jobID)
	r.dumpScreen(t, detail.PaneID)
	r.record(t, "no approval", detail)
	t.Fatalf("no approval for %s within %s", jobID, limit)
	return approvals.Approval{}
}

func (r *approvalRig) decide(t *testing.T, id, decision, note string) {
	t.Helper()
	if code := r.call(http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]any{"decision": decision, "note": note}, nil); code != http.StatusOK {
		t.Fatalf("deciding %s: %d", id, code)
	}
}

// allowFollowUps allows every pending approval of the job other than first, as the human would.
func (r *approvalRig) allowFollowUps(t *testing.T, jobID, first string) {
	t.Helper()
	var pending []approvals.Approval
	r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
	for _, approval := range pending {
		if approval.JobID == jobID && approval.ID != first {
			t.Logf("follow-up approval %s: %s (allowed)", approval.ID, approval.Summary)
			r.decide(t, approval.ID, "allow", "")
		}
	}
}

// screenHasDialog reports whether the pane shows a permission dialog right now.
func (r *approvalRig) screenHasDialog(paneID string) bool {
	read, err := r.client.PaneRead(context.Background(), paneID, herdr.ReadOptions{Source: herdr.SourceVisible})
	return err == nil && strings.Contains(read.Text, "Do you want to proceed?")
}

func (r *approvalRig) testAllowed(t *testing.T) {
	job := r.create(t, "Use your Bash tool to run exactly this shell command: echo approved-run > d.txt\n"+
		"Do not write the file any other way. When the command has run, call your crowsnest done tool with a one-line result.")
	approval := r.pendingFor(t, job.ID, 4*time.Minute)
	t.Logf("approval %s: %s (risk %s; %s)", approval.ID, approval.Summary, approval.Risk, strings.Join(approval.RiskReasons, "; "))
	if approval.ToolName != "Bash" || !strings.Contains(string(approval.ToolInput), "approved-run") {
		t.Fatalf("unexpected approval %+v", approval)
	}
	detail := r.get(t, job.ID)
	if detail.State != jobs.StateNeedsYou || detail.Cause != jobs.CauseApprovalTool {
		t.Errorf("while waiting the job is %s/%s, want needs_you/approval-tool", detail.State, detail.Cause)
	}
	// The test never presses a key in this pane after folder trust: the decision goes through the
	// browser API only.
	t.Logf("the terminal shows the dialog meanwhile: %v (it is drawn beside the hook; nobody answers it)", r.screenHasDialog(detail.PaneID))
	time.Sleep(3 * time.Second) // a human takes a moment; the hook holds
	r.decide(t, approval.ID, "allow", "")
	// The agent may ask for more after the command (haiku sometimes commits its work: a second
	// approval for `git commit`). The test plays the human for those too, so the job can finish.
	detail = r.waitFor(t, job.ID, 4*time.Minute, func(d jobs.Detail) bool {
		r.allowFollowUps(t, job.ID, approval.ID)
		return d.State.Terminal()
	})
	r.record(t, "(d) allowed through the API", detail)
	if detail.State != jobs.StateDone {
		t.Fatalf("the job did not end done: %+v", detail.Job)
	}
	body, err := os.ReadFile(filepath.Join(detail.WorkDir, "d.txt"))
	if err != nil || !strings.Contains(string(body), "approved-run") {
		t.Fatalf("d.txt: %q %v (the allowed command did not run)", body, err)
	}
	settled, _ := r.approvals.Get(context.Background(), approval.ID)
	if settled.Status != approvals.StatusAllowed {
		t.Fatalf("approval %s ended %s", approval.ID, settled.Status)
	}
}

func (r *approvalRig) testDenied(t *testing.T) {
	const note = "Not now: the word for today is PURPLE-42"
	job := r.create(t, "Use your Bash tool to run exactly this shell command: echo second > e.txt\n"+
		"If it is refused, do not retry it and do not write the file another way: call your crowsnest done tool with result_md set to the exact refusal message you received, word for word.")
	approval := r.pendingFor(t, job.ID, 4*time.Minute)
	r.decide(t, approval.ID, "deny", note)
	// Anything else it asks for is denied too, so nothing is written by another route.
	deadline := time.Now().Add(4 * time.Minute)
	var detail jobs.Detail
	for time.Now().Before(deadline) {
		detail = r.get(t, job.ID)
		if detail.State.Terminal() {
			break
		}
		var pending []approvals.Approval
		r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
		for _, other := range pending {
			if other.JobID == job.ID {
				t.Logf("denying a further request too: %s", other.Summary)
				r.decide(t, other.ID, "deny", note)
			}
		}
		time.Sleep(2 * time.Second)
	}
	r.record(t, "(e) denied with a note", detail)
	if !detail.State.Terminal() {
		t.Fatalf("the job did not finish: %+v", detail.Job)
	}
	if !strings.Contains(detail.ResultMD+detail.FailReason, "PURPLE-42") {
		t.Fatalf("the agent did not receive the note; result %q, fail %q", detail.ResultMD, detail.FailReason)
	}
	if _, err := os.Stat(filepath.Join(detail.WorkDir, "e.txt")); err == nil {
		t.Errorf("e.txt exists: the denied command ran")
	}
}

func (r *approvalRig) testGuard(t *testing.T) {
	job := r.create(t, "Use your Bash tool to run exactly this shell command: ssh "+protectedTestHost+" uptime\n"+
		"It may be refused. Do not retry it in any form. Then call your crowsnest done tool with result_md set to the exact refusal message you received, word for word.")
	deadline := time.Now().Add(4 * time.Minute)
	var detail jobs.Detail
	for time.Now().Before(deadline) {
		detail = r.get(t, job.ID)
		if detail.State.Terminal() {
			break
		}
		if detail.State == jobs.StateNeedsYou && detail.Cause == jobs.CauseDialog {
			r.answerTrust(t, detail)
		}
		var pending []approvals.Approval
		r.call(http.MethodGet, "/api/v1/approvals?status=pending", nil, &pending)
		for _, other := range pending {
			if other.JobID == job.ID {
				if strings.Contains(string(other.ToolInput), protectedTestHost) {
					t.Errorf("an approval was asked for the guarded command: %s", other.Summary)
				}
				r.decide(t, other.ID, "deny", "not part of this test")
			}
		}
		time.Sleep(2 * time.Second)
	}
	r.record(t, "(f) guard refuses", detail)
	refused := false
	for _, event := range detail.Timeline {
		if strings.Contains(event.Summary, "guard refused a command (protected-host)") {
			refused = true
			t.Logf("journalled: %s %v", event.Summary, event.Detail["command"])
		}
	}
	if !refused {
		t.Fatalf("the guard's refusal is not on the job's timeline")
	}
	if !strings.Contains(strings.ToLower(detail.ResultMD+detail.FailReason), "protected") {
		t.Errorf("the agent's result does not carry the guard's reason: %q", detail.ResultMD)
	}
}
