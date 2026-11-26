package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

type approvalRig struct {
	jobs      *jobs.Service
	approvals *approvals.Service
	db        *store.Store
	socket    string
	token     string
	other     string // a token for another pane
	jobID     string
	stop      func() // stops the agent socket's server
	// timings are this rig's hook timings (a 1 s long-poll), passed to each hook it runs.
	timings approvalTimings
}

// newApprovalRig serves the agent API with jobs and approvals, wired as serve wires them (a
// change moves the job's state), and a running job held in pane w2:p3.
func newApprovalRig(t *testing.T, ttl time.Duration) *approvalRig {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rig := &approvalRig{db: db, timings: defaultApprovalTimings}
	rig.timings.poll = time.Second
	rig.jobs = jobs.New(jobs.Options{Store: db, ApprovalPending: func(ctx context.Context, jobID string) bool {
		count, err := db.CountPendingApprovals(ctx, jobID)
		return err == nil && count > 0
	}})
	// Wired as serve wires it: the job reads what is pending itself (jobs.Options.ApprovalPending).
	rig.approvals = approvals.New(approvals.Options{Store: db, TTL: ttl, OnChange: func(ctx context.Context, a approvals.Approval, change string) {
		_ = rig.jobs.SetApprovalWaiting(ctx, a.JobID, change == "requested", a.Summary)
	}})
	identity := agentid.New(db, nil)
	rig.token, err = identity.Mint(context.Background(), agentid.Binding{PaneID: "w2:p3", Kind: agentid.KindPane})
	if err != nil {
		t.Fatal(err)
	}
	rig.other, err = identity.Mint(context.Background(), agentid.Binding{PaneID: "w9:p9", Kind: agentid.KindPane})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "cn-ap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	rig.socket = filepath.Join(dir, "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := agentapi.Listen(ctx, rig.socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = agentapi.NewServer(agentapi.ServerOptions{Questions: questions.New(questions.Options{Store: db}), Jobs: rig.jobs, Identity: identity, Approvals: rig.approvals}).Serve(ctx, listener)
		close(done)
	}()
	var once sync.Once
	rig.stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(rig.stop)
	rig.jobID = runningJob(t, rig.jobs, db)
	return rig
}

func (rig *approvalRig) env(name string) string {
	switch name {
	case agentapi.EnvSocket:
		return rig.socket
	case agentapi.EnvToken:
		return rig.token
	}
	return ""
}

const bashRequest = `{"session_id":"s1","cwd":"/tmp","hook_event_name":"PermissionRequest","tool_name":"Bash",` +
	`"tool_input":{"command":"touch made-by-claude.txt","description":"Create a file"},` +
	`"permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}]}`

// runRequestHook runs `crowsnest hook permission-request` in the background; its output arrives
// on the returned channel. The hook belongs to t: at t's cleanup (before the rig's) it is
// cancelled and waited for, so a test that fails early never leaves a hook running into the next.
func runRequestHook(t *testing.T, rig *approvalRig, getenv func(string) string) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan string, 1)
	finished := make(chan struct{})
	timings := rig.timings
	go func() {
		defer close(finished)
		var stdout bytes.Buffer
		runHook(ctx, strings.NewReader(bashRequest), &stdout, getenv, "permission-request", hookFlags{timings: &timings})
		out <- stdout.String()
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return out
}

// jobIs waits for the job to reach state and cause: the job is moved by the approvals' change
// callback, which runs after the approval is stored, so a poll can see the approval first.
func jobIs(t *testing.T, rig *approvalRig, state jobs.State, cause jobs.Cause) {
	t.Helper()
	var job jobs.Detail
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, _ = rig.jobs.Get(context.Background(), rig.jobID)
		if job.State == state && job.Cause == cause {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the job is %s/%s, want %s/%s", job.State, job.Cause, state, cause)
}

// pendingApproval waits for the hook's approval to appear.
func pendingApproval(t *testing.T, rig *approvalRig) approvals.Approval {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list, _ := rig.approvals.List(context.Background(), []approvals.Status{approvals.StatusPending}, 10)
		if len(list) > 0 {
			return list[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no approval was requested")
	return approvals.Approval{}
}

func hookResult(t *testing.T, out <-chan string, within time.Duration) string {
	t.Helper()
	select {
	case result := <-out:
		return result
	case <-time.After(within):
		t.Fatal("the hook did not return")
		return ""
	}
}

func TestPermissionRequestHoldsThenAllows(t *testing.T) {
	rig := newApprovalRig(t, 0)
	out := runRequestHook(t, rig, rig.env)
	approval := pendingApproval(t, rig)
	if approval.JobID != rig.jobID || approval.PaneID != "w2:p3" || approval.ToolName != "Bash" || approval.Cwd != "/tmp" || approval.SessionID != "s1" {
		t.Fatalf("approval %+v", approval)
	}
	if !strings.Contains(string(approval.Suggestions), "acceptEdits") {
		t.Fatalf("the suggestions are kept for the card: %s", approval.Suggestions)
	}
	jobIs(t, rig, jobs.StateNeedsYou, jobs.CauseApprovalTool) // while waiting
	// Held across several long-polls (each 1 s here).
	time.Sleep(2500 * time.Millisecond)
	select {
	case early := <-out:
		t.Fatalf("the hook returned %q before a decision", early)
	default:
	}
	if _, err := rig.approvals.Decide(context.Background(), approval.ID, approvals.Decision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	result := hookResult(t, out, 5*time.Second)
	var decoded struct {
		HookSpecificOutput struct {
			HookEventName string         `json:"hookEventName"`
			Decision      map[string]any `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil || decoded.HookSpecificOutput.HookEventName != "PermissionRequest" || decoded.HookSpecificOutput.Decision["behavior"] != "allow" {
		t.Fatalf("hook printed %q", result)
	}
	jobIs(t, rig, jobs.StateRunning, "") // after the decision
}

func TestPermissionRequestDenyCarriesTheNote(t *testing.T) {
	rig := newApprovalRig(t, 0)
	out := runRequestHook(t, rig, rig.env)
	approval := pendingApproval(t, rig)
	if _, err := rig.approvals.Decide(context.Background(), approval.ID, approvals.Decision{Note: "use printf instead"}); err != nil {
		t.Fatal(err)
	}
	result := hookResult(t, out, 5*time.Second)
	if !strings.Contains(result, `"behavior":"deny"`) || !strings.Contains(result, "use printf instead") {
		t.Fatalf("hook printed %q", result)
	}
}

func TestPermissionRequestExpiryPrintsNothing(t *testing.T) {
	rig := newApprovalRig(t, 300*time.Millisecond)
	out := runRequestHook(t, rig, rig.env)
	pendingApproval(t, rig)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				rig.approvals.Sweep(context.Background())
			}
		}
	}()
	if result := hookResult(t, out, 5*time.Second); result != "" {
		t.Fatalf("an expired approval must print nothing (Claude's own dialog), got %q", result)
	}
}

func TestPermissionRequestSupersededPrintsNothing(t *testing.T) {
	rig := newApprovalRig(t, 0)
	out := runRequestHook(t, rig, rig.env)
	pendingApproval(t, rig)
	// The agent's turn ended: a "No" in the terminal, or an interrupt.
	var stdout bytes.Buffer
	runHook(context.Background(), strings.NewReader(`{"session_id":"s1","hook_event_name":"Stop"}`), &stdout, rig.env, "stop", hookFlags{})
	if result := hookResult(t, out, 5*time.Second); result != "" {
		t.Fatalf("a superseded approval must print nothing, got %q", result)
	}
	jobIs(t, rig, jobs.StateRunning, "") // superseded: back from needs_you
}

func TestPermissionRequestServerDownPrintsNothing(t *testing.T) {
	dead := filepath.Join(t.TempDir(), "nobody.sock")
	getenv := func(name string) string {
		if name == agentapi.EnvSocket {
			return dead
		}
		return ""
	}
	start := time.Now()
	var stdout bytes.Buffer
	runHook(context.Background(), strings.NewReader(bashRequest), &stdout, getenv, "permission-request", hookFlags{})
	if stdout.Len() != 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("crowsnest down: printed %q after %s; want nothing, at once", stdout.String(), time.Since(start))
	}
}

func TestPermissionRequestServerGoneMidWaitGivesUp(t *testing.T) {
	rig := newApprovalRig(t, 0)
	rig.timings.retryFor, rig.timings.retryEvery = 300*time.Millisecond, 50*time.Millisecond
	out := runRequestHook(t, rig, rig.env)
	pendingApproval(t, rig)
	rig.stop() // crowsnest goes away while the hook waits
	if result := hookResult(t, out, 10*time.Second); result != "" {
		t.Fatalf("the server gone mid-wait: printed %q, want nothing", result)
	}
}

func TestPermissionRequestWithoutTokenPrintsNothing(t *testing.T) {
	rig := newApprovalRig(t, 0)
	getenv := func(name string) string {
		if name == agentapi.EnvSocket {
			return rig.socket
		}
		return ""
	}
	var stdout bytes.Buffer
	runHook(context.Background(), strings.NewReader(bashRequest), &stdout, getenv, "permission-request", hookFlags{})
	if stdout.Len() != 0 {
		t.Fatalf("an unidentified caller gets Claude's own dialog, printed %q", stdout.String())
	}
	if list, _ := rig.approvals.List(context.Background(), nil, 10); len(list) != 0 {
		t.Fatalf("an unidentified caller created %d approvals", len(list))
	}
}

func TestGuardHook(t *testing.T) {
	rig := newApprovalRig(t, 0)
	rules := filepath.Join(t.TempDir(), "guard.json")
	body, _ := json.Marshal(guard.Rules{Session: "crowsnest", ProtectedHosts: []string{"nyc1"}})
	if err := os.WriteFile(rules, body, 0o400); err != nil {
		t.Fatal(err)
	}
	run := func(command string) string {
		input, _ := json.Marshal(map[string]any{"session_id": "s1", "cwd": t.TempDir(), "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
		var stdout bytes.Buffer
		runHook(context.Background(), bytes.NewReader(input), &stdout, rig.env, "pre-tool-use", hookFlags{rules: rules})
		return stdout.String()
	}
	if out := run("go test ./..."); out != "" {
		t.Fatalf("an ordinary command: %q", out)
	}
	out := run("ssh nyc1 uptime")
	var decoded struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil || decoded.HookSpecificOutput.PermissionDecision != "deny" ||
		decoded.HookSpecificOutput.HookEventName != "PreToolUse" || !strings.Contains(decoded.HookSpecificOutput.PermissionDecisionReason, "nyc1") {
		t.Fatalf("refusal printed %q", out)
	}
	if out := run("herdr server stop"); !strings.Contains(out, `"deny"`) {
		t.Fatalf("herdr server stop: %q", out)
	}
	detail, err := rig.jobs.Get(context.Background(), rig.jobID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range detail.Timeline {
		if strings.Contains(event.Summary, "guard refused") {
			found = true
		}
	}
	if !found {
		t.Fatal("a refusal is journalled on the job")
	}
	// A missing rules file allows (fail open), like every hook.
	var stdout bytes.Buffer
	runHook(context.Background(), strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ssh nyc1"}}`), &stdout, rig.env, "pre-tool-use", hookFlags{rules: "/nonexistent/guard.json"})
	if stdout.Len() != 0 {
		t.Fatalf("no rules file: %q", stdout.String())
	}
}

func TestAnotherPaneCannotPollAnApproval(t *testing.T) {
	rig := newApprovalRig(t, 0)
	out := runRequestHook(t, rig, rig.env)
	approval := pendingApproval(t, rig)
	for _, token := range []string{rig.other, ""} {
		_, err := agentapi.NewClient(rig.socket, token).WaitApproval(context.Background(), approval.ID, 0)
		var apiErr *agentapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			t.Fatalf("polling another pane's approval (with a token: %v): %v, want 404", token != "", err)
		}
	}
	_, _ = rig.approvals.Decide(context.Background(), approval.ID, approvals.Decision{})
	hookResult(t, out, 5*time.Second)
}
