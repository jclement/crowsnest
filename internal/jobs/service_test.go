package jobs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/store"
)

// ---- Rig -----------------------------------------------------------------------------------

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type rig struct {
	t       *testing.T
	db      *store.Store
	fake    *fake.Fake
	svc     *Service
	clock   *clock
	notices []string
	mu      sync.Mutex
	orch    OrchestratorInfo
}

func newRig(t *testing.T, change func(*Options)) *rig {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &rig{t: t, db: db, fake: fake.New(), clock: &clock{now: time.Unix(1_800_000_000, 0)}}
	opts := Options{
		Store: db, Herdr: r.fake, Now: r.clock.Now, Synchronous: true,
		Launch: LaunchOptions{StateDir: t.TempDir(), Executable: "/opt/crowsnest/bin/crowsnest", AgentSocket: "/run/crowsnest/test.sock",
			Session: "crowsnest-test", PermissionMode: "auto", ShellWait: time.Second, LaunchTimeout: 2 * time.Second},
		Notify: func(text string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.notices = append(r.notices, text)
		},
		Orchestrator: func() OrchestratorInfo {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.orch
		},
	}
	if change != nil {
		change(&opts)
	}
	r.svc = New(opts)
	return r
}

func (r *rig) Notices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.notices...)
}

func (r *rig) get(id string) Detail {
	r.t.Helper()
	detail, err := r.svc.Get(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return detail
}

// create makes a job in dir (a plain directory unless git is true) and returns it after launch.
func (r *rig) create(input CreateInput) Detail {
	r.t.Helper()
	job, err := r.svc.Create(context.Background(), input, "")
	if err != nil {
		r.t.Fatal(err)
	}
	return r.get(job.ID)
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git not available: %v %s", err, out)
	}
	return dir
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func hasEvent(d Detail, kind, contains string) bool {
	for _, event := range d.Timeline {
		if event.Kind == kind && strings.Contains(event.Summary, contains) {
			return true
		}
	}
	return false
}

// ---- Launcher ------------------------------------------------------------------------------

func TestLaunchBuilderInAGitRepoGetsAWorktree(t *testing.T) {
	r := newRig(t, nil)
	repo := gitRepo(t)
	job := r.create(CreateInput{Brief: "Add a widget\n\nWith tests.", Dir: repo, DoneWhen: "tests pass"})
	if job.State != StateRunning || job.AgentName != "job-1" || job.PaneID == "" {
		t.Fatalf("job %+v", job.Job)
	}
	worktrees := r.fake.Worktrees()
	if len(worktrees) != 1 || worktrees[0].Branch != "job/1-add-a-widget" || worktrees[0].Cwd != repo {
		t.Fatalf("worktrees %+v", worktrees)
	}
	if job.WorktreeBranch != "job/1-add-a-widget" || job.WorkDir == repo {
		t.Fatalf("branch %q workdir %q", job.WorktreeBranch, job.WorkDir)
	}
	env := r.fake.PaneEnv(job.PaneID)
	if env["CROWSNEST_JOB"] != "j_1" || env["CROWSNEST_SOCKET"] != "/run/crowsnest/test.sock" {
		t.Fatalf("pane env %v", env)
	}
	start := r.fake.Starts()[0]
	if start.Name != "job-1" || start.Kind != "claude" || start.PaneID != job.PaneID {
		t.Fatalf("start %+v", start)
	}
	if argValue(start.Args, "--session-id") != job.SessionID || argValue(start.Args, "--permission-mode") != "auto" {
		t.Fatalf("args %v (session %s)", start.Args, job.SessionID)
	}
	assertJobFiles(t, start.Args, "j_1")
	prompts := r.fake.Prompts()
	if len(prompts) != 1 || !strings.HasPrefix(prompts[0].Text, "[crowsnest job j_1] Add a widget") || !strings.Contains(prompts[0].Text, "Done when: tests pass") {
		t.Fatalf("prompts %+v", prompts)
	}
}

// assertJobFiles checks the prompt, settings and MCP config a job agent is started with.
func assertJobFiles(t *testing.T, args []string, jobID string) {
	t.Helper()
	prompt, err := os.ReadFile(argValue(args, "--append-system-prompt-file"))
	if err != nil || !strings.Contains(string(prompt), "Role: builder") || !strings.Contains(string(prompt), "crowsnest job **"+jobID+"**") {
		t.Fatalf("prompt file: %v %.200s", err, prompt)
	}
	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
		Hooks       map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	body, _ := os.ReadFile(argValue(args, "--settings"))
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(settings.Permissions.Allow, []string{"mcp__crowsnest__*"}) {
		t.Fatalf("allow %v", settings.Permissions.Allow)
	}
	for _, event := range []string{"Stop", "SessionStart", "Notification", "StopFailure", "PermissionDenied", "SessionEnd", "PostToolUse"} {
		hooks := settings.Hooks[event]
		if len(hooks) != 1 || !strings.Contains(hooks[0].Hooks[0].Command, "--job '"+jobID+"'") || !strings.Contains(hooks[0].Hooks[0].Command, "--socket '/run/crowsnest/test.sock'") {
			t.Fatalf("hook %s: %+v", event, hooks)
		}
	}
	var mcp struct {
		Servers map[string]struct{ Env map[string]string } `json:"mcpServers"`
	}
	body, _ = os.ReadFile(argValue(args, "--mcp-config"))
	_ = json.Unmarshal(body, &mcp)
	if mcp.Servers["crowsnest"].Env["CROWSNEST_JOB"] != jobID {
		t.Fatalf("mcp.json %s", body)
	}
}

// TestJobAgentsCarryTheirTokens is R1b N2 for job agents: the pane's environment, the MCP
// server's and the hooks' token file each hold a token the registry maps to the job and its
// pane, so the socket can tell the job's own calls from anyone naming it.
func TestJobAgentsCarryTheirTokens(t *testing.T) {
	var registry *agentid.Registry
	r := newRig(t, func(o *Options) {
		registry = agentid.New(o.Store, nil)
		o.Launch.Identity = registry
	})
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir(), Role: "reviewer"})
	ctx := context.Background()
	check := func(where, token, kind string) {
		t.Helper()
		binding, ok, err := registry.Resolve(ctx, token)
		if err != nil || !ok || binding.JobID != job.ID || binding.PaneID != job.PaneID || binding.Kind != kind {
			t.Fatalf("%s token %q resolves to %+v (%v %v)", where, token, binding, ok, err)
		}
	}
	check("pane", r.fake.PaneEnv(job.PaneID)[agentid.EnvToken], agentid.KindPane)
	args := r.fake.Starts()[0].Args
	var mcp struct {
		Servers map[string]struct{ Env map[string]string } `json:"mcpServers"`
	}
	body, _ := os.ReadFile(argValue(args, "--mcp-config"))
	_ = json.Unmarshal(body, &mcp)
	check("mcp.json", mcp.Servers["crowsnest"].Env[agentid.EnvToken], agentid.KindStart)
	settings, _ := os.ReadFile(argValue(args, "--settings"))
	tokenFile := filepath.Join(filepath.Dir(argValue(args, "--settings")), "agent-token")
	if !strings.Contains(string(settings), "--token-file '"+tokenFile+"'") {
		t.Fatalf("the hooks do not name the token file: %s", settings)
	}
	fromFile, err := agentid.ReadTokenFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	check("hook token file", fromFile, agentid.KindStart)
	if info, _ := os.Stat(tokenFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v", info.Mode().Perm())
	}
}

func TestLaunchInAnOpenWorkspaceUsesANewTab(t *testing.T) {
	r := newRig(t, nil)
	dir := t.TempDir()
	workspace, _, _ := r.fake.AddWorkspace("project", dir)
	job := r.create(CreateInput{Brief: "Review it", Dir: dir, Role: "reviewer"})
	if len(r.fake.Worktrees()) != 0 {
		t.Fatal("a reviewer must not get a worktree")
	}
	pane, err := r.fake.PaneGet(context.Background(), job.PaneID)
	if err != nil || pane.WorkspaceID != workspace || job.WorkDir != dir {
		t.Fatalf("pane %+v err %v", pane, err)
	}
	if r.fake.PaneEnv(job.PaneID)["CROWSNEST_JOB"] != job.ID {
		t.Fatal("tab env missing")
	}
}

func TestLaunchInAPlainDirectoryOpensAWorkspace(t *testing.T) {
	r := newRig(t, nil)
	dir := t.TempDir() // not a git repo, so even a builder gets a plain workspace
	job := r.create(CreateInput{Brief: "Do it", Dir: dir})
	workspaces, _ := r.fake.WorkspaceList(context.Background())
	if len(workspaces) != 1 || workspaces[0].Label != filepath.Base(dir) || job.State != StateRunning {
		t.Fatalf("workspaces %+v job %+v", workspaces, job.Job)
	}
}

func TestLaunchStoppedAtTrustDialogWaitsForTheHuman(t *testing.T) {
	r := newRig(t, nil)
	r.fake.StartNotReadyNext()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	if job.State != StateNeedsYou || job.Cause != CauseDialog || len(r.fake.Prompts()) != 0 {
		t.Fatalf("job %+v prompts %v", job.Job, r.fake.Prompts())
	}
	for _, key := range r.fake.SentKeys() {
		t.Fatalf("crowsnest must not answer a start-up dialog: %+v", key)
	}
	r.svc.Tick(context.Background()) // still at the dialog: nothing happens
	if len(r.fake.Prompts()) != 0 {
		t.Fatal("brief sent into a dialog")
	}
	if err := r.fake.ClearDialog(job.PaneID); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(context.Background())
	job = r.get(job.ID)
	if job.State != StateRunning || len(r.fake.Prompts()) != 1 {
		t.Fatalf("after the dialog: %+v prompts %v", job.Job, r.fake.Prompts())
	}
}

func TestJobsWithoutADirGoToTheOrchestratorOneAtATime(t *testing.T) {
	r := newRig(t, nil)
	_, _, pane := r.fake.AddWorkspace("orchestrator", "/tmp/orch")
	if err := r.fake.AddAgent(pane, "orchestrator", "claude", herdr.StatusIdle, "s-orch"); err != nil {
		t.Fatal(err)
	}
	r.orch = OrchestratorInfo{PaneID: pane, Ready: true}
	first := r.create(CreateInput{Brief: "Plan the release"})
	if first.State != StateRunning || first.AgentName != OrchestratorName || first.Role != "lead" {
		t.Fatalf("first %+v", first.Job)
	}
	if prompts := r.fake.Prompts(); len(prompts) != 1 || !strings.HasPrefix(prompts[0].Text, "[crowsnest job j_1]") || !strings.Contains(prompts[0].Text, `job_id "j_1"`) {
		t.Fatalf("prompts %+v", prompts)
	}
	second := r.create(CreateInput{Brief: "Write the changelog"})
	if second.State != StateQueued {
		t.Fatalf("second should wait: %+v", second.Job)
	}
	if _, err := r.svc.Done(context.Background(), first.ID, DoneInput{ResultMD: "ok", Verified: []string{}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	r.svc.Tick(context.Background())
	if second = r.get(second.ID); second.State != StateRunning || second.AgentName != OrchestratorName {
		t.Fatalf("second after the first finished: %+v", second.Job)
	}
	// A hook from the orchestrator's pane (no --job) belongs to the job it holds now.
	reply, err := r.svc.HandleHook(context.Background(), HookStop, "", pane, HookInput{SessionID: "s-orch"})
	if err != nil || !strings.Contains(reply.Stdout, "job j_2") {
		t.Fatalf("orchestrator stop: %q %v", reply.Stdout, err)
	}
	// No job held: the stop is allowed.
	reply, _ = r.svc.HandleHook(context.Background(), HookStop, "", "w9:p9", HookInput{})
	if reply.Stdout != "" {
		t.Fatalf("no job should allow: %q", reply.Stdout)
	}
}

func TestCreateValidation(t *testing.T) {
	r := newRig(t, func(o *Options) { o.Herdr = nil })
	for _, input := range []CreateInput{
		{Brief: ""},
		{Brief: "x", Role: "wizard"},
		{Brief: "x", Engine: "gpt"},
		{Brief: "x", Dir: "relative/path"},
		{Brief: "x", Dir: "/definitely/not/here"},
		{Brief: "x", Engine: "codex"}, // no dir → orchestrator, which is Claude
		{Brief: strings.Repeat("x", MaxBriefBytes+1)},
	} {
		if _, err := r.svc.Create(context.Background(), input, ""); !IsValidation(err) {
			t.Errorf("%+v: err %v", input.Role+input.Engine+input.Dir, err)
		}
	}
}

func TestCodexGetsTheProtocolInItsFirstPrompt(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Port it", Dir: t.TempDir(), Engine: "codex", Model: "gpt-5"})
	start := r.fake.Starts()[0]
	if start.Kind != "codex" || !slices.Contains(start.Args, "never") || argValue(start.Args, "--model") != "gpt-5" || argValue(start.Args, "--settings") != "" {
		t.Fatalf("codex start %+v", start)
	}
	text := r.fake.Prompts()[0].Text
	if !strings.Contains(text, "crowsnest job update") || !strings.Contains(text, "[crowsnest job "+job.ID+"] Port it") {
		t.Fatalf("codex prompt %.300s", text)
	}
}

// ---- Hooks ---------------------------------------------------------------------------------

func TestStopHookNudgesUntilDeclaredAndProgressResetsTheBudget(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	stop := func(active bool) string {
		r.clock.Advance(time.Second)
		reply, err := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{SessionID: job.SessionID, StopHookActive: active})
		if err != nil {
			t.Fatal(err)
		}
		return reply.Stdout
	}
	out := stop(false)
	var block struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(out), &block); err != nil || block.Decision != "block" || !strings.Contains(block.Reason, "job "+job.ID) {
		t.Fatalf("first stop: %q", out)
	}
	stop(true)
	if d := r.get(job.ID); d.Nudges != 2 {
		t.Fatalf("nudges %d", d.Nudges)
	}
	r.clock.Advance(time.Second)
	if _, err := r.svc.HandleHook(ctx, HookPostToolUse, job.ID, "", HookInput{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)}); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.LastTool != "Bash: go test ./..." {
		t.Fatalf("last tool %q", d.LastTool)
	}
	// Progress since the last nudge: the streak restarts, so three more nudges are allowed.
	for i := range 3 {
		if out := stop(true); out == "" {
			t.Fatalf("nudge %d after progress was not given", i+1)
		}
	}
	if out := stop(true); out != "" {
		t.Fatalf("the fourth stop without progress must be allowed: %q", out)
	}
	d := r.get(job.ID)
	if !d.Stuck || d.Cause != CauseUnresponsive || len(r.Notices()) != 1 || !hasEvent(d, EventStuck, "unresponsive") {
		t.Fatalf("escalation: %+v notices %v", d.Job, r.Notices())
	}
	if out := stop(true); out != "" {
		t.Fatal("an unresponsive job with no new progress is let rest")
	}
	if _, err := r.svc.Done(ctx, job.ID, DoneInput{ResultMD: "did it", Verified: []string{"ran it"}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	if out := stop(true); out != "" {
		t.Fatalf("a done job may stop: %q", out)
	}
}

func TestWaitingWithoutQuestionsIsRefusedAndRepeatsAreNotProgress(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	waiting := true
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "waiting", Waiting: &waiting}); !IsValidation(err) || !strings.Contains(err.Error(), "call ask") {
		t.Fatalf("waiting with no questions: %v", err)
	}
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "same line"}); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Second)
	_, _ = r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{})
	r.clock.Advance(time.Second)
	_, _ = r.svc.Update(ctx, job.ID, UpdateInput{Progress: "same line"})
	r.clock.Advance(time.Second)
	reply, _ := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{StopHookActive: true})
	if d := r.get(job.ID); !strings.Contains(reply.Stdout, "block") || d.Nudges != 2 || !hasEvent(d, EventNudge, "(2 of 3)") {
		t.Fatalf("a repeated line reset the budget: %+v", d.Job)
	}
}

func TestAnswerTypedBackLetsTheTurnEnd(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	id := insertQuestion(t, r.db, job.ID, false)
	r.clock.Advance(time.Minute)
	if err := r.db.SettleQuestion(ctx, id, "answered", json.RawMessage(`"ack"`), "", "type", r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Second)
	if reply, _ := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{}); reply.Stdout != "" {
		t.Fatalf("stop right after an answer was typed back: %q", reply.Stdout)
	}
}

func TestStopAllowedWhenWaitingOnQuestions(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	questionID := insertQuestion(t, r.db, job.ID, false)
	waiting := true
	updated, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "waiting for the answer", Waiting: &waiting})
	if err != nil || updated.State != StateNeedsYou || updated.Cause != CauseQuestions {
		t.Fatalf("update %+v %v", updated, err)
	}
	if reply, _ := r.svc.HandleHook(ctx, HookStop, job.ID, "", HookInput{}); reply.Stdout != "" {
		t.Fatalf("stop while waiting: %q", reply.Stdout)
	}
	settle(t, r.db, questionID)
	r.svc.QuestionChanged(ctx, job.ID, "q_1", "Ship?", "answered", false)
	if d := r.get(job.ID); d.State != StateRunning || d.Waiting || !hasEvent(d, EventQuestion, "answered") {
		t.Fatalf("after the answer: %+v", d.Job)
	}
}

func TestBlockingAskPutsTheJobInNeedsYou(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	questionID := insertQuestion(t, r.db, job.ID, true)
	r.svc.QuestionChanged(ctx, job.ID, "q_1", "Delete prod?", "asked", true)
	if d := r.get(job.ID); d.State != StateNeedsYou || d.OpenQuestions != 1 || !hasEvent(d, EventQuestion, "(blocking)") {
		t.Fatalf("blocking ask: %+v", d.Job)
	}
	settle(t, r.db, questionID)
	r.svc.QuestionChanged(ctx, job.ID, "q_1", "Delete prod?", "answered", true)
	if d := r.get(job.ID); d.State != StateRunning {
		t.Fatalf("answered: %+v", d.Job)
	}
}

func insertQuestion(t *testing.T, db *store.Store, jobID string, wait bool) int64 {
	t.Helper()
	id, err := db.InsertQuestion(context.Background(), store.QuestionRecord{Title: "Ship?", Kind: "ack", Urgency: "normal", JobID: jobID, Wait: wait,
		CreatedAt: time.Unix(1_800_000_000, 0), ExpiresAt: time.Unix(1_900_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func settle(t *testing.T, db *store.Store, id int64) {
	t.Helper()
	if err := db.SettleQuestion(context.Background(), id, "answered", json.RawMessage(`"ack"`), "", "type", time.Unix(1_800_000_100, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStartReinjectsTheJob(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Rename the flag\n\nEverywhere.", Dir: t.TempDir()})
	_, _ = r.svc.Update(context.Background(), job.ID, UpdateInput{Progress: "renaming in cmd/"})
	for _, source := range []string{"startup", "resume", "compact", "clear"} {
		reply, err := r.svc.HandleHook(context.Background(), HookSessionStart, job.ID, "", HookInput{Source: source, SessionID: job.SessionID})
		if err != nil || !strings.Contains(reply.Stdout, "Rename the flag") || !strings.Contains(reply.Stdout, "renaming in cmd/") {
			t.Fatalf("%s: %q %v", source, reply.Stdout, err)
		}
	}
}

func TestSessionIDChangeIsAdopted(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(context.Background(), HookSessionStart, job.ID, "", HookInput{Source: "clear", SessionID: "11111111-2222-3333-4444-555555555555"})
	if d := r.get(job.ID); d.SessionID != "11111111-2222-3333-4444-555555555555" || !hasEvent(d, EventSession, "changed") {
		t.Fatalf("session %q", d.SessionID)
	}
}

func TestNotificationPermissionPromptNeedsYou(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(context.Background(), HookNotification, job.ID, "", HookInput{NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"})
	if d := r.get(job.ID); d.State != StateNeedsYou || d.Cause != CauseDialog {
		t.Fatalf("permission prompt: %+v", d.Job)
	}
	// Work after the dialog means it was answered.
	_, _ = r.svc.HandleHook(context.Background(), HookPostToolUse, job.ID, "", HookInput{ToolName: "Bash"})
	if d := r.get(job.ID); d.State != StateRunning {
		t.Fatalf("after the dialog: %+v", d.Job)
	}
}

func TestPermissionDeniedThreeTimesIsStuck(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	for range 3 {
		_, _ = r.svc.HandleHook(context.Background(), HookPermissionDenied, job.ID, "", HookInput{ToolName: "Bash", Reason: "push to main"})
	}
	if d := r.get(job.ID); !d.Stuck || d.Cause != CausePermissions || !strings.Contains(d.CauseDetail, "push to main") {
		t.Fatalf("denials: %+v", d.Job)
	}
}

func TestStopFailureAuthNeedsYouWithoutRetries(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(context.Background(), HookStopFailure, job.ID, "", HookInput{Error: "billing_error"})
	d := r.get(job.ID)
	if d.State != StateNeedsYou || d.Cause != CauseAuth || d.RetryAt != nil || len(r.Notices()) != 1 {
		t.Fatalf("billing: %+v", d.Job)
	}
}

// ---- Recovery ------------------------------------------------------------------------------

func TestRateLimitRetriesOnSchedule(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "rate_limit"})
	d := r.get(job.ID)
	if !d.Stuck || d.Cause != CauseLimit || d.RetryAt == nil || !d.RetryAt.Equal(r.clock.Now().Add(5*time.Minute).UTC()) {
		t.Fatalf("limit: %+v", d.Job)
	}
	prompts := len(r.fake.Prompts())
	r.clock.Advance(4 * time.Minute)
	r.svc.Tick(ctx)
	if len(r.fake.Prompts()) != prompts {
		t.Fatal("retried early")
	}
	wantGaps := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour}
	for i, gap := range wantGaps {
		r.clock.Advance(nextGap(r, job.ID))
		_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
		r.svc.Tick(ctx)
		all := r.fake.Prompts()
		if all[len(all)-1].Text != "continue" {
			for _, event := range r.get(job.ID).Timeline {
				t.Log(event.Kind, event.Summary)
			}
			t.Fatalf("retry %d typed %q", i+1, all[len(all)-1].Text)
		}
		d = r.get(job.ID)
		if d.RetryCount != i+1 || !d.RetryAt.Equal(r.clock.Now().Add(gap).UTC()) {
			t.Fatalf("retry %d: count %d next %v (want +%s)", i+1, d.RetryCount, d.RetryAt, gap)
		}
	}
	// The agent working again clears the stuck flag.
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	r.svc.Tick(ctx)
	if d = r.get(job.ID); d.Stuck {
		t.Fatalf("still stuck while working: %+v", d.Job)
	}
}

// nextGap is how far the clock must move for the job's retry to be due.
func nextGap(r *rig, id string) time.Duration {
	d := r.get(id)
	return d.RetryAt.Sub(r.clock.Now())
}

func TestServerErrorRetriesThreeTimesThenTellsJeff(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_, _ = r.svc.HandleHook(ctx, HookStopFailure, job.ID, "", HookInput{Error: "server_error"})
	if d := r.get(job.ID); !d.RetryAt.Equal(r.clock.Now().Add(time.Minute).UTC()) {
		t.Fatalf("first retry at %v", d.RetryAt)
	}
	for range 3 {
		r.clock.Advance(nextGap(r, job.ID))
		_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
		r.svc.Tick(ctx)
	}
	d := r.get(job.ID)
	if d.RetryCount != 3 || d.RetryAt != nil || len(r.Notices()) != 1 {
		t.Fatalf("after three retries: %+v notices %v", d.Job, r.Notices())
	}
}

func TestAgentThatExitsIsRestartedWithinBudget(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	for i := 1; i <= maxRestartsPerHour; i++ {
		if err := r.fake.ExitAgent(job.PaneID); err != nil {
			t.Fatal(err)
		}
		r.clock.Advance(time.Minute)
		r.svc.Tick(ctx)
		d := r.get(job.ID)
		if d.Restarts != i || len(r.fake.Starts()) != i+1 || d.Stuck {
			t.Fatalf("restart %d: %+v starts %d", i, d.Job, len(r.fake.Starts()))
		}
	}
	// No transcript to resume in a test, so each restart is a fresh session with the brief again.
	if last := r.fake.Starts()[maxRestartsPerHour]; argValue(last.Args, "--session-id") == "" {
		t.Fatalf("restart args %v", last.Args)
	}
	_ = r.fake.ExitAgent(job.PaneID)
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateFailed || len(r.Notices()) != 1 {
		t.Fatalf("over budget: %+v notices %v", d.Job, r.Notices())
	}
}

func TestSilentAndIdleDetection(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	working := r.create(CreateInput{Brief: "Working one", Dir: t.TempDir()})
	idle := r.create(CreateInput{Brief: "Idle one", Dir: t.TempDir()})
	_ = r.fake.SetStatus(working.PaneID, herdr.StatusWorking)
	_ = r.fake.SetStatus(idle.PaneID, herdr.StatusIdle)
	r.clock.Advance(11 * time.Minute)
	r.svc.Tick(ctx)
	if d := r.get(idle.ID); !d.Stuck || d.Cause != CauseIdle {
		t.Fatalf("idle: %+v", d.Job)
	}
	if d := r.get(working.ID); d.Stuck {
		t.Fatalf("working flagged at 11 minutes: %+v", d.Job)
	}
	r.clock.Advance(10 * time.Minute)
	r.svc.Tick(ctx)
	if d := r.get(working.ID); !d.Stuck || d.Cause != CauseSilent {
		t.Fatalf("silent: %+v", d.Job)
	}
	// Progress clears it.
	_, _ = r.svc.Update(ctx, working.ID, UpdateInput{Progress: "still here"})
	if d := r.get(working.ID); d.Stuck {
		t.Fatalf("progress did not clear silent: %+v", d.Job)
	}
}

func TestTranscriptActivityIsTheHeartbeat(t *testing.T) {
	var activityAt time.Time
	r := newRig(t, func(o *Options) {
		o.Activity = func(context.Context, Job) (time.Time, string) { return activityAt, "Edit: main.go" }
	})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	r.clock.Advance(25 * time.Minute)
	activityAt = r.clock.Now().Add(-time.Minute)
	r.svc.Tick(ctx)
	d := r.get(job.ID)
	if d.Stuck || d.LastActivityAt == nil || !d.LastActivityAt.Equal(activityAt.UTC()) || d.DoingNow != "Edit: main.go" {
		t.Fatalf("heartbeat: %+v", d.Job)
	}
}

func TestOldQuestionsAreRenotifiedOnce(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	insertQuestion(t, r.db, job.ID, false)
	r.clock.Advance(5 * time.Hour)
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusWorking)
	r.svc.Tick(ctx)
	r.svc.Tick(ctx)
	count := 0
	for _, notice := range r.Notices() {
		if strings.Contains(notice, "q_1 is still waiting") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("notices %v", r.Notices())
	}
}

// ---- Actions -------------------------------------------------------------------------------

func TestCancelSendsEscAndKeepsThePane(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	cancelled, err := r.svc.Cancel(context.Background(), job.ID)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("cancel %+v %v", cancelled, err)
	}
	keys := r.fake.SentKeys()
	if len(keys) != 1 || keys[0].Keys[0] != "esc" {
		t.Fatalf("keys %+v", keys)
	}
	if _, err := r.fake.AgentGet(context.Background(), job.PaneID); err != nil {
		t.Fatal("the agent must not be killed")
	}
	if _, err := r.svc.Cancel(context.Background(), job.ID); !IsValidation(err) {
		t.Fatalf("second cancel: %v", err)
	}
	if _, err := r.svc.Update(context.Background(), job.ID, UpdateInput{Progress: "x"}); !IsValidation(err) {
		t.Fatalf("update after cancel: %v", err)
	}
}

func TestMessageAndNudge(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
	if _, err := r.svc.Message(ctx, job.ID, MessageInput{Text: "use the v2 API"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Nudge(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	prompts := r.fake.Prompts()
	if !strings.HasPrefix(prompts[len(prompts)-2].Text, "[crowsnest message j_1] use the v2 API") || !strings.HasPrefix(prompts[len(prompts)-1].Text, "[crowsnest nudge]") {
		t.Fatalf("prompts %+v", prompts)
	}
	d := r.get(job.ID)
	if d.Nudges != 1 || !hasEvent(d, EventMessage, "use the v2 API") {
		t.Fatalf("detail %+v", d.Job)
	}
}

func TestNudgeIfDueUsesTheSameBudget(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Do it", Engine: "codex", Dir: t.TempDir()})
	for i := range 3 {
		_ = r.fake.SetStatus(job.PaneID, herdr.StatusIdle)
		r.clock.Advance(time.Minute)
		nudged, err := r.svc.NudgeIfDue(ctx, job.ID)
		if err != nil || !nudged {
			t.Fatalf("backstop nudge %d: %v %v", i+1, nudged, err)
		}
	}
	nudged, _ := r.svc.NudgeIfDue(ctx, job.ID)
	if d := r.get(job.ID); nudged || d.Cause != CauseUnresponsive {
		t.Fatalf("fourth: nudged=%v %+v", nudged, d.Job)
	}
}

func TestDoneRequiresEvidence(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	for _, input := range []DoneInput{
		{ResultMD: "", Verified: []string{}, NotVerified: []string{}},
		{ResultMD: "ok", Verified: nil, NotVerified: []string{}},
		{ResultMD: "ok", Verified: []string{}, NotVerified: nil},
	} {
		if _, err := r.svc.Done(context.Background(), job.ID, input); !IsValidation(err) {
			t.Fatalf("%+v: %v", input, err)
		}
	}
	done, err := r.svc.Done(context.Background(), job.ID, DoneInput{ResultMD: "ok", Verified: []string{"go test", " "}, NotVerified: []string{"safari"}})
	if err != nil || done.State != StateDone || len(done.Verified) != 1 || done.NotVerified[0] != "safari" || done.SettledAt == nil {
		t.Fatalf("done %+v %v", done, err)
	}
}

// The job detail carries each question's options, so the job view can show an answer by its
// label rather than its key (found by the M9 e2e suite: "→ fri" instead of "→ Friday").
func TestJobQuestionsCarryTheirOptions(t *testing.T) {
	r := newRig(t, nil)
	job := r.create(CreateInput{Brief: "Do it", Dir: t.TempDir()})
	options := json.RawMessage(`[{"key":"fri","label":"Friday"},{"key":"mon","label":"Next Monday","detail":"after the docs pass"}]`)
	id, err := r.db.InsertQuestion(context.Background(), store.QuestionRecord{Title: "Ship on Friday?", Kind: "choice", Options: options, Recommend: "fri",
		Urgency: "normal", JobID: job.ID, CreatedAt: time.Unix(1_800_000_000, 0), ExpiresAt: time.Unix(1_900_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	insertQuestion(t, r.db, job.ID, false) // an ack: no options, and an empty list rather than null
	if err := r.db.SettleQuestion(context.Background(), id, "answered", json.RawMessage(`"fri"`), "", "type", time.Unix(1_800_000_100, 0)); err != nil {
		t.Fatal(err)
	}
	questions := r.get(job.ID).Questions
	if len(questions) != 2 {
		t.Fatalf("questions: %+v", questions)
	}
	byKind := map[string]Question{}
	for _, question := range questions {
		byKind[question.Kind] = question
	}
	want := []QuestionOption{{Key: "fri", Label: "Friday"}, {Key: "mon", Label: "Next Monday"}}
	if got := byKind["choice"].Options; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("choice options: %+v", got)
	}
	if got := byKind["ack"].Options; got == nil || len(got) != 0 {
		t.Fatalf("ack options should be an empty list: %#v", got)
	}
}
