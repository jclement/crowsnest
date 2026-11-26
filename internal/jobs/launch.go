// The launcher (DESIGN.md 6.2): giving a job an agent.
//
//   - With a directory that is a git repository and a role that isolates (builders by default):
//     herdr's worktree.create on a branch job/<n>-<slug>, then a tab in that workspace with the
//     job's pane env (worktree.create takes no env).
//   - With any other directory: a tab in a workspace already open on that directory, or a new
//     workspace for it.
//   - Either way: wait for the shell, write prompt.md, settings.json and mcp.json under
//     <state>/jobs/<id>/, agent.start (name job-<n>), then type the brief as the first prompt.
//   - Without a directory: the orchestrator holds the job. It is sent the brief under a
//     `[crowsnest job j_N]` header when it holds no other running job; otherwise the job waits
//     queued and the supervisor tick dispatches it later.
//
// An agent that stops at a start-up dialog (folder trust) puts the job in needs_you with cause
// dialog; crowsnest never answers it. The tick sends the brief once someone has.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jclement/crowsnest/internal/agentcfg"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// LaunchOptions configure how job agents are started.
type LaunchOptions struct {
	StateDir        string // <state>; job files go in <state>/jobs/<id>/
	Executable      string // the crowsnest binary, for hooks and mcp.json
	AgentSocket     string // CROWSNEST_SOCKET
	BinDir          string // prepended to PATH in the pane env
	BasePath        string // the PATH BinDir is prepended to
	Session         string // CROWSNEST_SESSION
	PermissionMode  string // Claude's --permission-mode
	ClaudeConfigDir string // where transcripts live (resume checks)
	// WorktreeRoot, when set, is the parent directory of job worktrees (tests keep them in a
	// scratch dir); empty lets herdr choose (~/.herdr/worktrees/<repo>/<slug>).
	WorktreeRoot  string
	ShellWait     time.Duration // default 15 s
	LaunchTimeout time.Duration // default 60 s
}

func (o LaunchOptions) shellWait() time.Duration {
	if o.ShellWait > 0 {
		return o.ShellWait
	}
	return 15 * time.Second
}

func (o LaunchOptions) launchTimeout() time.Duration {
	if o.LaunchTimeout > 0 {
		return o.LaunchTimeout
	}
	return 60 * time.Second
}

// OrchestratorName is the agent name of the orchestrator (orch.Name).
const OrchestratorName = orch.Name

// AgentName is the herdr agent name of a job's agent.
func AgentName(jobID string) string { return "job-" + strings.TrimPrefix(jobID, "j_") }

var zeroTime time.Time

func newUUID() string { return uuid.NewString() }

// launch gives a newly created job its agent. It runs in the background.
func (s *Service) launch(ctx context.Context, id string) {
	if s.opts.Herdr == nil {
		return
	}
	detail, err := s.Get(ctx, id)
	if err != nil || detail.State != StateQueued {
		return
	}
	if detail.Dir == "" {
		s.DispatchOrchestrator(ctx)
		return
	}
	if err := s.startJobAgent(ctx, detail.Job); err != nil {
		s.launchFailed(ctx, id, err)
	}
}

// launchFailed marks a job whose agent could not be started: needs_you, so Jeff sees it.
func (s *Service) launchFailed(ctx context.Context, id string, err error) {
	s.opts.Logger.Warn("launching a job agent", "job", id, "err", err)
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return nil
		}
		r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseLaunch), oneLine(err.Error(), 500)
		c.event(EventLaunch, "Could not start the agent: "+oneLine(err.Error(), 200), nil)
		c.notify = fmt.Sprintf("crowsnest: job %s could not start: %s", FormatID(r.ID), oneLine(err.Error(), 120))
		return nil
	})
}

// startJobAgent prepares a pane, starts the agent in it and sends the brief.
func (s *Service) startJobAgent(ctx context.Context, job Job) error {
	pane, err := s.preparePane(ctx, job)
	if err != nil {
		return err
	}
	sessionID := ""
	if job.Engine == EngineClaude {
		sessionID = uuid.NewString()
	}
	if _, err := s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		r.PaneID, r.WorkDir, r.WorktreeBranch, r.AgentName, r.ClaudeSessionID = pane.paneID, pane.workDir, pane.branch, AgentName(job.ID), sessionID
		summary := "Opened pane " + pane.paneID + " in " + pane.workDir
		if pane.branch != "" {
			summary += " (worktree, branch " + pane.branch + ")"
		}
		c.event(EventLaunch, summary, map[string]any{"pane": pane.paneID, "dir": pane.workDir, "branch": pane.branch})
		return nil
	}); err != nil {
		return err
	}
	job.PaneID, job.WorkDir, job.SessionID = pane.paneID, pane.workDir, sessionID
	return s.startInPane(ctx, job, false)
}

// startInPane writes the job's files and starts its agent in job.PaneID (a bare shell). resume
// restarts Claude on job.SessionID with --resume. On success the brief (or, on resume, a nudge)
// is sent.
func (s *Service) startInPane(ctx context.Context, job Job, resume bool) error {
	files, err := s.writeJobFiles(job)
	if err != nil {
		return err
	}
	params := herdr.AgentStartParams{Name: AgentName(job.ID), Kind: job.Engine, PaneID: job.PaneID, Args: s.agentArgs(job, files, resume), TimeoutMS: startTimeoutMS}
	agent, err := s.startAgent(ctx, params)
	if errors.Is(err, herdr.ErrAgentNotReady) {
		s.markDialog(ctx, job.ID, "The agent stopped at a start-up prompt (folder trust or login). Open its terminal and answer it; crowsnest sends the brief after that.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("starting %s in pane %s: %w", job.Engine, job.PaneID, err)
	}
	if session := sessionOf(agent); session != "" && job.Engine == EngineClaude && !resume {
		job.SessionID = session
	}
	if resume {
		return s.sendText(ctx, job.PaneID, "[crowsnest restart] You were restarted. "+NudgeText(job.ID))
	}
	return s.sendBrief(ctx, job)
}

// markDialog puts a job in needs_you because of a terminal dialog.
func (s *Service) markDialog(ctx context.Context, id, detail string) {
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() || (State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseDialog) {
			return nil
		}
		r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseDialog), detail
		c.event(EventStuck, "Needs you: a dialog in the terminal", map[string]any{"pane": r.PaneID})
		c.notify = fmt.Sprintf("crowsnest: job %s needs you at a terminal dialog", FormatID(r.ID))
		return nil
	})
}

// sendBrief types the job's first prompt and marks it running.
func (s *Service) sendBrief(ctx context.Context, job Job) error {
	text, err := s.firstPrompt(job)
	if err != nil {
		return err
	}
	result, err := orch.Prompt(ctx, s.opts.Herdr, job.PaneID, orch.PromptRequest{Text: text, ForceOverDraft: true})
	var blocked *orch.BlockedError
	if errors.As(err, &blocked) {
		s.markDialog(ctx, job.ID, "A dialog is up in the agent's terminal, so the brief was not sent. Answer it in the terminal.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("sending the brief: %w", err)
	}
	_, err = s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return nil
		}
		now := s.opts.Now()
		r.BriefSent, r.StartedAt, r.LastActivityAt = true, now, now
		r.State, r.Stuck, r.Cause, r.CauseDetail = string(StateRunning), false, "", ""
		summary := "Brief sent to " + r.AgentName
		if !result.Started {
			summary += " (typed; the turn had not visibly started yet)"
		}
		c.event(EventLaunch, summary, nil)
		return nil
	})
	return err
}

// sendText types a message into a job's agent.
func (s *Service) sendText(ctx context.Context, paneID, text string) error {
	_, err := orch.Prompt(ctx, s.opts.Herdr, paneID, orch.PromptRequest{Text: text, ForceOverDraft: true})
	return err
}

// firstPrompt is what the agent reads first: the brief, under the job header. Claude has the
// protocol in its system prompt; Codex gets it here.
func (s *Service) firstPrompt(job Job) (string, error) {
	var b strings.Builder
	if job.Engine == EngineCodex {
		protocol, err := s.opts.Roles.Prompt(job.Role, job.ID, false)
		if err != nil {
			return "", err
		}
		b.WriteString(protocol + "\n\n---\n\n")
	}
	fmt.Fprintf(&b, "[crowsnest job %s] %s", job.ID, job.BriefMD)
	if job.DoneWhen != "" {
		fmt.Fprintf(&b, "\n\nDone when: %s", job.DoneWhen)
	}
	return b.String(), nil
}

// ---- Panes ---------------------------------------------------------------------------------

type preparedPane struct {
	paneID, workDir, branch string
}

// preparePane finds or makes the pane a job's agent starts in.
func (s *Service) preparePane(ctx context.Context, job Job) (preparedPane, error) {
	callCtx, cancel := context.WithTimeout(ctx, 2*herdrCallTimeout)
	defer cancel()
	var pane preparedPane
	var err error
	if job.Isolated() && isGitRepo(ctx, job.Dir) {
		pane, err = s.worktreePane(callCtx, job)
	} else {
		pane, err = s.tabPane(callCtx, job)
	}
	if err != nil {
		return preparedPane{}, err
	}
	return pane, s.waitForShell(ctx, pane.paneID)
}

// worktreePane: worktree.create, then a tab with the job env in the new workspace.
func (s *Service) worktreePane(ctx context.Context, job Job) (preparedPane, error) {
	branch := branchName(job)
	params := herdr.WorktreeCreateParams{Cwd: job.Dir, Branch: branch, Label: AgentName(job.ID)}
	if s.opts.Launch.WorktreeRoot != "" {
		params.Path = filepath.Join(s.opts.Launch.WorktreeRoot, strings.ReplaceAll(branch, "/", "-"))
	}
	created, err := s.opts.Herdr.WorktreeCreate(ctx, params)
	if err != nil {
		return preparedPane{}, fmt.Errorf("creating a worktree of %s on branch %s: %w", job.Dir, branch, err)
	}
	workDir := created.Worktree.Path
	if workDir == "" {
		workDir = created.RootPane.Cwd
	}
	tab, err := s.opts.Herdr.TabCreate(ctx, herdr.TabCreateParams{WorkspaceID: created.Workspace.ID, Label: AgentName(job.ID), Cwd: workDir, Env: s.paneEnv(job.ID)})
	if err != nil {
		return preparedPane{}, fmt.Errorf("opening a tab in the job's worktree: %w", err)
	}
	return preparedPane{paneID: tab.RootPane.ID, workDir: workDir, branch: branch}, nil
}

// tabPane: a tab in a workspace open on job.Dir, else a new workspace there.
func (s *Service) tabPane(ctx context.Context, job Job) (preparedPane, error) {
	env := s.paneEnv(job.ID)
	if workspaceID := s.workspaceFor(ctx, job.Dir); workspaceID != "" {
		tab, err := s.opts.Herdr.TabCreate(ctx, herdr.TabCreateParams{WorkspaceID: workspaceID, Label: AgentName(job.ID), Cwd: job.Dir, Env: env})
		if err != nil {
			return preparedPane{}, fmt.Errorf("opening a tab for the job: %w", err)
		}
		return preparedPane{paneID: tab.RootPane.ID, workDir: job.Dir}, nil
	}
	created, err := s.opts.Herdr.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{Label: filepath.Base(job.Dir), Cwd: job.Dir, Env: env})
	if err != nil {
		return preparedPane{}, fmt.Errorf("opening a workspace for %s: %w", job.Dir, err)
	}
	return preparedPane{paneID: created.RootPane.ID, workDir: job.Dir}, nil
}

// workspaceFor returns a workspace that has a pane whose cwd is dir, if any.
func (s *Service) workspaceFor(ctx context.Context, dir string) string {
	panes, err := s.opts.Herdr.PaneList(ctx, "")
	if err != nil {
		return ""
	}
	for _, pane := range panes {
		if filepath.Clean(pane.Cwd) == dir {
			return pane.WorkspaceID
		}
	}
	return ""
}

// paneEnv is the environment of a job's pane (6.2).
func (s *Service) paneEnv(jobID string) map[string]string {
	env := map[string]string{"CROWSNEST": "1", "CROWSNEST_JOB": jobID}
	if s.opts.Launch.Session != "" {
		env["CROWSNEST_SESSION"] = s.opts.Launch.Session
	}
	if s.opts.Launch.AgentSocket != "" {
		env["CROWSNEST_SOCKET"] = s.opts.Launch.AgentSocket
	}
	if s.opts.Launch.BinDir != "" {
		env["PATH"] = s.opts.Launch.BinDir + string(os.PathListSeparator) + s.opts.Launch.BasePath
	}
	return env
}

// Isolated reports whether the job asked for a worktree.
func (j Job) Isolated() bool { return j.WorktreeBranch != "" || j.isolate }

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// branchName is job/<n>-<slug of the title>.
func branchName(job Job) string {
	slug := strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(job.Title), "-"), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	branch := "job/" + strings.TrimPrefix(job.ID, "j_")
	if slug != "" {
		branch += "-" + slug
	}
	return branch
}

// isGitRepo reports whether dir is inside a git work tree.
func isGitRepo(ctx context.Context, dir string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// ---- Files and arguments -------------------------------------------------------------------

type jobFiles struct {
	prompt, settings, mcpConfig string
}

// writeJobFiles writes <state>/jobs/<id>/{prompt.md,settings.json,mcp.json}, all 0600.
func (s *Service) writeJobFiles(job Job) (jobFiles, error) {
	dir := filepath.Join(s.opts.Launch.StateDir, "jobs", job.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return jobFiles{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	files := jobFiles{prompt: filepath.Join(dir, "prompt.md"), settings: filepath.Join(dir, "settings.json")}
	prompt, err := s.opts.Roles.Prompt(job.Role, job.ID, true)
	if err != nil {
		return jobFiles{}, err
	}
	if err := os.WriteFile(files.prompt, []byte(prompt), 0o600); err != nil {
		return jobFiles{}, fmt.Errorf("writing the job prompt: %w", err)
	}
	withMCP := s.opts.Launch.Executable != "" && s.opts.Launch.AgentSocket != ""
	if err := writeJSON(files.settings, agentcfg.ClaudeSettings(s.opts.Launch.Executable, s.opts.Launch.AgentSocket, job.ID, withMCP)); err != nil {
		return jobFiles{}, err
	}
	if withMCP {
		files.mcpConfig = filepath.Join(dir, "mcp.json")
		if err := writeJSON(files.mcpConfig, agentcfg.MCPConfig(s.opts.Launch.Executable, s.opts.Launch.AgentSocket, job.ID)); err != nil {
			return jobFiles{}, err
		}
	}
	return files, nil
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// agentArgs is the agent's command line. Content goes in files, never in argv (macOS refuses a
// start command longer than the terminal's line-input limit; DESIGN 7 step 4).
func (s *Service) agentArgs(job Job, files jobFiles, resume bool) []string {
	if job.Engine == EngineCodex {
		// Verified against codex-cli 0.167 --help: there is no --no-daemon flag.
		args := []string{"--ask-for-approval", "never", "--sandbox", "workspace-write"}
		if job.Model != "" {
			args = append(args, "--model", job.Model)
		}
		return args
	}
	flag := "--session-id"
	if resume {
		flag = "--resume"
	}
	args := []string{flag, job.SessionID}
	if s.opts.Launch.PermissionMode != "" {
		args = append(args, "--permission-mode", s.opts.Launch.PermissionMode)
	}
	if job.Model != "" {
		args = append(args, "--model", job.Model)
	}
	args = append(args, "--append-system-prompt-file", files.prompt, "--settings", files.settings)
	if files.mcpConfig != "" {
		args = append(args, "--mcp-config", files.mcpConfig)
	}
	return args
}

func sessionOf(agent herdr.Agent) string {
	if agent.AgentSession != nil {
		return agent.AgentSession.Value
	}
	return ""
}

// ---- The orchestrator ----------------------------------------------------------------------

// DispatchOrchestrator gives the oldest queued no-directory job to the orchestrator, when it is
// ready and holds no other running job (an agent holds at most one running job, 6.1).
func (s *Service) DispatchOrchestrator(ctx context.Context) {
	if s.opts.Orchestrator == nil || s.opts.Herdr == nil {
		return
	}
	info := s.opts.Orchestrator()
	if info.PaneID == "" || !info.Ready {
		return
	}
	queued, err := s.opts.Store.ListJobs(ctx, []string{string(StateQueued), string(StateRunning)}, listLimit)
	if err != nil {
		return
	}
	var next *store.JobRecord
	for i := range queued {
		record := queued[i]
		if record.AgentName == OrchestratorName && State(record.State) == StateRunning {
			return // it is busy with one
		}
		if record.Dir == "" && State(record.State) == StateQueued && record.AgentName == "" {
			next = &queued[i] // ListJobs is newest first, so the last match is the oldest
		}
	}
	if next == nil {
		return
	}
	job := s.toJob(*next, 0)
	text := fmt.Sprintf("[crowsnest job %s] You hold job %s (role %s). Pass job_id %q to job_update, done, fail and job_get. "+
		"The turn may only end once the job is declared (done, fail, blocked, or waiting on questions).\n\n%s", job.ID, job.ID, job.Role, job.ID, job.BriefMD)
	if job.DoneWhen != "" {
		text += "\n\nDone when: " + job.DoneWhen
	}
	if _, err := orch.Prompt(ctx, s.opts.Herdr, info.PaneID, orch.PromptRequest{Text: text}); err != nil {
		s.opts.Logger.Info("the orchestrator could not take a job yet", "job", job.ID, "err", err)
		return
	}
	_, _ = s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		now := s.opts.Now()
		r.PaneID, r.AgentName, r.BriefSent, r.StartedAt, r.LastActivityAt = info.PaneID, OrchestratorName, true, now, now
		r.State = string(StateRunning)
		c.event(EventLaunch, "Assigned to the orchestrator", map[string]any{"pane": info.PaneID})
		return nil
	})
}
