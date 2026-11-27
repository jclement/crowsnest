// The launcher (DESIGN.md 6.2): giving a job an agent.
//
//   - With a directory that is a git repository and a role that isolates (builders by default):
//     herdr's worktree.create on a branch job/<n>-<slug>, then a tab in that workspace with the
//     job's pane env (worktree.create takes no env).
//   - With any other directory: a tab in a workspace already open on that directory, or a new
//     workspace for it.
//   - Either way: wait for the shell, write prompt.md, settings.json and mcp.json under
//     <state>/jobs/<id>/, agent.start (name job-<n>), then write brief.md there and type one
//     line naming it (briefs.go: a brief is never typed, R2-M1).
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
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
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
	// Identity mints each job agent's tokens (R1b N2): CROWSNEST_TOKEN in its pane's environment,
	// bound to the pane and the job, and a token per start for its MCP server and hooks. The
	// socket derives the caller's job from them, never from CROWSNEST_JOB. nil: no tokens.
	Identity *agentid.Registry
	// RemoteControlPrefix, when set (--job-remote-control), starts Claude job agents with Remote
	// Control named "<prefix> <job id> <short title>" (DESIGN.md 7.10). Empty: off.
	RemoteControlPrefix string
	// Guard, when set, is written to each job's guard.json and named on its PreToolUse guard hook
	// (DESIGN.md 8.7). nil: no guard hook.
	Guard *guard.Rules
	// ExtraEnv is added to every job agent's pane environment (CROWSNEST_HEAVY_DIR, 6.12).
	ExtraEnv map[string]string
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

// launch gives a newly created job its agent. It runs in the background. A job with no
// directory goes to the orchestrator; one with a directory is claimed (opLaunch) for the whole
// launch, so the tick, a second launch or a cancel cannot interleave with it.
func (s *Service) launch(ctx context.Context, id string) {
	if s.opts.Herdr == nil {
		return
	}
	detail, err := s.Get(ctx, id)
	if err != nil || detail.State != StateQueued || detail.Cause == CauseApproval {
		// A job held for the human's approval starts only once allowed (delegate.go).
		return
	}
	if detail.Dir == "" {
		s.DispatchStanding(ctx)
		return
	}
	held := ""
	cl, err := s.claimStep(ctx, id, opLaunch, func(r *store.JobRecord, _ *change) error {
		if State(r.State) != StateQueued || r.PaneID != "" {
			return errSkip // launched already; an interrupted launch with a pane is the tick's
		}
		// The slot limit, the load hold and the pause, weighed under the same lock as the
		// claim, so two launches never both take the last slot (holds.go).
		if held = s.startHoldLocked(ctx, *r); held != "" {
			return errHeld
		}
		if Cause(r.Cause) == CauseHeld {
			r.Cause, r.CauseDetail = "", ""
		}
		return nil
	})
	if errors.Is(err, errHeld) {
		s.markHeld(ctx, id, held)
		return
	}
	if err != nil {
		return
	}
	if err := s.startJobAgent(ctx, cl, detail.Job); err != nil {
		s.launchFailed(ctx, cl, err)
	}
}

// launchFailed marks a job whose agent could not be started: needs_you, so Jeff sees it. A launch
// that lost its claim (the job was cancelled meanwhile) records nothing.
func (s *Service) launchFailed(ctx context.Context, cl claim, err error) {
	if errors.Is(err, errClaimLost) {
		return
	}
	s.opts.Logger.Warn("launching a job agent", "job", cl.id, "err", err)
	s.finishQuietly(ctx, cl, func(r *store.JobRecord, c *change) {
		r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseLaunch), oneLine(err.Error(), 500)
		c.event(EventLaunch, "Could not start the agent: "+oneLine(err.Error(), 200), nil)
		c.notify = fmt.Sprintf("crowsnest: job %s could not start: %s", FormatID(r.ID), oneLine(err.Error(), 120))
	})
}

// startJobAgent prepares a pane, starts the agent in it and sends the brief, under cl.
func (s *Service) startJobAgent(ctx context.Context, cl claim, job Job) error {
	pane, err := s.preparePane(ctx, job)
	if err != nil {
		return err
	}
	sessionID := ""
	if job.Engine == EngineClaude {
		sessionID = uuid.NewString()
	}
	if err := s.update(ctx, cl, func(r *store.JobRecord, c *change) {
		r.PaneID, r.WorkDir, r.WorktreeBranch, r.AgentName, r.ClaudeSessionID = pane.paneID, pane.workDir, pane.branch, AgentName(job.ID), sessionID
		// The directory its children may use, fixed now, before its agent has run (R3-H1).
		r.RootDir = confinementRoot(pane.workDir)
		r.BaseBranch, r.BaseCommit, r.WorkspaceID = pane.baseBranch, pane.baseCommit, pane.workspaceID
		summary := "Opened pane " + pane.paneID + " in " + pane.workDir
		if pane.branch != "" {
			summary += " (worktree, branch " + pane.branch + ")"
		}
		c.event(EventLaunch, summary, map[string]any{"pane": pane.paneID, "dir": pane.workDir, "branch": pane.branch})
	}); err != nil {
		return err
	}
	job.PaneID, job.WorkDir, job.SessionID = pane.paneID, pane.workDir, sessionID
	return s.startInPane(ctx, cl, job, false)
}

// startInPane writes the job's files and starts its agent in job.PaneID (a bare shell), under
// cl. resume restarts Claude on job.SessionID with --resume. Once it is up, the brief is typed
// (or, on a resume of an agent that had its brief, a restart nudge), and the claim is finished.
func (s *Service) startInPane(ctx context.Context, cl claim, job Job, resume bool) error {
	if err := s.holds(ctx, cl); err != nil {
		return err
	}
	files, err := s.writeJobFiles(ctx, job)
	if err != nil {
		return err
	}
	if err := s.holds(ctx, cl); err != nil {
		return err
	}
	params := herdr.AgentStartParams{Name: AgentName(job.ID), Kind: job.Engine, PaneID: job.PaneID, Args: s.agentArgs(job, files, resume), TimeoutMS: startTimeoutMS}
	var agent herdr.Agent
	if claimErr := s.effect(ctx, cl, job.PaneID, func(ctx context.Context) { agent, err = s.startAgent(ctx, params) }); claimErr != nil {
		return claimErr
	}
	if errors.Is(err, herdr.ErrAgentNotReady) {
		s.finishQuietly(ctx, cl, func(r *store.JobRecord, c *change) {
			markDialogLocked(r, c, "The agent stopped at a start-up prompt (folder trust or login). Open its terminal and answer it; crowsnest sends the brief after that.")
		})
		return nil
	}
	if err != nil {
		return fmt.Errorf("starting %s in pane %s: %w", job.Engine, job.PaneID, err)
	}
	if session := sessionOf(agent); session != "" && job.Engine == EngineClaude && !resume {
		job.SessionID = session
	}
	if resume && job.briefSent {
		var sendErr error
		// R7-C04: a held job's agent is back in its pane, but it is not told to carry on.
		if why := s.continuationRefusal(ctx, job.ID); why != "" {
			sendErr = errors.New("not typed: the job is " + why)
		} else if err := s.effect(ctx, cl, job.PaneID, func(ctx context.Context) {
			sendErr = s.sendText(ctx, job.PaneID, AgentName(job.ID), "[crowsnest restart] You were restarted. "+NudgeText(job.ID))
		}); err != nil {
			return err
		}
		s.finishQuietly(ctx, cl, func(r *store.JobRecord, c *change) {
			if sendErr != nil {
				c.event(EventRestart, "Restarted; the restart note could not be typed: "+oneLine(sendErr.Error(), 160), nil)
			}
			if cl.owns(*r) && Cause(r.Cause) == CauseExited {
				r.Stuck, r.Cause, r.CauseDetail = false, "", "" // only the restart's own mark (R3-C04)
			}
		})
		return nil
	}
	return s.sendBrief(ctx, cl, job)
}

// markDialogLocked puts a job in needs_you because of a terminal dialog.
func markDialogLocked(r *store.JobRecord, c *change, detail string) {
	if State(r.State).Terminal() || (State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseDialog) {
		return
	}
	r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseDialog), detail
	c.event(EventStuck, "Needs you: a dialog in the terminal", map[string]any{"pane": r.PaneID})
	c.notify = fmt.Sprintf("crowsnest: job %s needs you at a terminal dialog", FormatID(r.ID))
}

// sendBrief writes the job's brief file and types the one line that names it (briefs.go: a
// brief is never typed, R2-M1), and marks the job running, under cl (moved to opBrief first, so
// a crash while typing is known to be one: at most once). A typing that provably did nothing
// leaves the brief unsent for the tick to try again.
func (s *Service) sendBrief(ctx context.Context, cl claim, job Job) error {
	path, err := s.writeBrief(ctx, job)
	if err != nil {
		return err
	}
	text := briefLine(job, path)
	if cl.op != opBrief {
		if cl, err = s.advance(ctx, cl, opBrief); err != nil {
			return err
		}
	}
	var result orch.PromptResult
	if claimErr := s.effect(ctx, cl, job.PaneID, func(ctx context.Context) {
		result, err = orch.Prompt(ctx, s.opts.Herdr, job.PaneID, orch.PromptRequest{Text: text, ForceOverDraft: true, ExpectAgent: AgentName(job.ID)})
	}); claimErr != nil {
		return claimErr
	}
	outcome := typedOutcome(result, err)
	var blocked *orch.BlockedError
	switch {
	case errors.As(err, &blocked):
		s.finishQuietly(ctx, cl, func(r *store.JobRecord, c *change) {
			markDialogLocked(r, c, "A dialog is up in the agent's terminal, so the brief was not sent. Answer it in the terminal.")
		})
		return nil
	case outcome == typedNo:
		s.finishQuietly(ctx, cl, func(r *store.JobRecord, c *change) {
			c.event(EventLaunch, "The brief could not be typed yet: "+oneLine(err.Error(), 160), nil)
		})
		return nil
	}
	_, err = s.finish(ctx, cl, func(r *store.JobRecord, c *change) {
		now := s.opts.Now()
		r.BriefSent, r.StartedAt, r.LastActivityAt = true, now, now
		briefDelivered(r, cl)
		summary := "Brief sent to " + r.AgentName
		switch {
		case outcome == typedMaybe:
			summary += " (crowsnest lost track of the send; it is not typed again)"
		case !result.Started:
			summary += " (typed; the turn had not visibly started yet)"
		}
		c.event(EventLaunch, summary, nil)
	})
	return err
}

// briefDelivered moves a job whose brief has just been typed to running, replacing only the
// disposition the step claimed (R3-C04): queued (crowsnest's own "no agent yet"), a start-up
// dialog or failed launch, a restart's "exited". If the agent or a hook wrote something newer
// while the brief was typed (job_update blocked, an auth failure, a limit), that stays; a queued
// job still becomes running, since queued with a brief delivered would be a state nobody watches.
func briefDelivered(r *store.JobRecord, cl claim) {
	if cl.owns(*r) {
		r.State, r.Stuck, r.Cause, r.CauseDetail = string(StateRunning), false, "", ""
		return
	}
	if State(r.State) == StateQueued {
		r.State = string(StateRunning)
	}
}

// sendText types a message into a job's agent, provided its pane still holds agent (R2-I5).
func (s *Service) sendText(ctx context.Context, paneID, agent, text string) error {
	_, err := orch.Prompt(ctx, s.opts.Herdr, paneID, orch.PromptRequest{Text: text, ForceOverDraft: true, ExpectAgent: agent})
	return err
}

// ---- Panes ---------------------------------------------------------------------------------

type preparedPane struct {
	paneID, workDir, branch string
	// For a worktree: the main checkout's branch and the commit the worktree started at, and the
	// workspace herdr opened on it (Land's review and teardown, DESIGN.md 6.11).
	baseBranch, baseCommit, workspaceID string
}

// preparePane finds or makes the pane a job's agent starts in.
func (s *Service) preparePane(ctx context.Context, job Job) (preparedPane, error) {
	if job.Origin != OriginHuman {
		// An agent's job was checked and stored with its directory resolved (R3-H1). If the path
		// resolves anywhere else now, a link was swapped since the check: start nothing there.
		if now, err := filepath.EvalSymlinks(job.Dir); err != nil || now != job.Dir {
			return preparedPane{}, fmt.Errorf("the directory %s changed since it was checked (it no longer resolves to itself); not starting there", job.Dir)
		}
	}
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
	baseBranch, _ := landing.CurrentBranch(ctx, job.Dir) // "" when unknown: Land then offers no merge
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
	env, token, err := s.paneEnv(job.ID)
	if err != nil {
		return preparedPane{}, err
	}
	tab, err := s.opts.Herdr.TabCreate(ctx, herdr.TabCreateParams{WorkspaceID: created.Workspace.ID, Label: AgentName(job.ID), Cwd: workDir, Env: env})
	if err != nil {
		return preparedPane{}, fmt.Errorf("opening a tab in the job's worktree: %w", err)
	}
	s.bindPaneToken(ctx, token, tab.RootPane.ID, job.ID)
	baseCommit, _ := landing.Head(ctx, workDir)
	return preparedPane{paneID: tab.RootPane.ID, workDir: workDir, branch: branch,
		baseBranch: baseBranch, baseCommit: baseCommit, workspaceID: created.Workspace.ID}, nil
}

// tabPane: a tab in a workspace open on job.Dir, else a new workspace there.
func (s *Service) tabPane(ctx context.Context, job Job) (preparedPane, error) {
	env, token, err := s.paneEnv(job.ID)
	if err != nil {
		return preparedPane{}, err
	}
	if workspaceID := s.workspaceFor(ctx, job.Dir); workspaceID != "" {
		tab, err := s.opts.Herdr.TabCreate(ctx, herdr.TabCreateParams{WorkspaceID: workspaceID, Label: AgentName(job.ID), Cwd: job.Dir, Env: env})
		if err != nil {
			return preparedPane{}, fmt.Errorf("opening a tab for the job: %w", err)
		}
		s.bindPaneToken(ctx, token, tab.RootPane.ID, job.ID)
		return preparedPane{paneID: tab.RootPane.ID, workDir: job.Dir}, nil
	}
	created, err := s.opts.Herdr.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{Label: filepath.Base(job.Dir), Cwd: job.Dir, Env: env})
	if err != nil {
		return preparedPane{}, fmt.Errorf("opening a workspace for %s: %w", job.Dir, err)
	}
	s.bindPaneToken(ctx, token, created.RootPane.ID, job.ID)
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

// paneEnv is the environment of a job's pane (6.2), with a fresh agent token when there is an
// identity registry; the token is returned so it can be bound once herdr names the pane.
// CROWSNEST_JOB stays as the default job id the agent's own commands use; the socket checks it
// against the token.
func (s *Service) paneEnv(jobID string) (map[string]string, string, error) {
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
	for key, value := range s.opts.Launch.ExtraEnv {
		env[key] = value
	}
	if s.opts.Launch.Identity == nil || s.opts.Launch.AgentSocket == "" {
		return env, "", nil
	}
	token, err := agentid.NewToken()
	if err != nil {
		return nil, "", err
	}
	env[agentid.EnvToken] = token
	return env, token, nil
}

// bindPaneToken binds a pane token to the pane herdr created for job. A failure is logged, not
// fatal: the start token in the agent's MCP config and hook file still identifies it.
func (s *Service) bindPaneToken(ctx context.Context, token, paneID, jobID string) {
	if token == "" {
		return
	}
	if err := s.opts.Launch.Identity.Bind(ctx, token, agentid.Binding{PaneID: paneID, JobID: jobID, Kind: agentid.KindPane}); err != nil {
		s.opts.Logger.Warn("binding a job pane's agent token", "job", jobID, "pane", paneID, "err", err)
	}
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
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output() //nolint:gosec // dir is a validated absolute directory, passed as one argument, never through a shell
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// ---- Files and arguments -------------------------------------------------------------------

type jobFiles struct {
	prompt, settings, mcpConfig string
}

// writeJobFiles writes <state>/jobs/<id>/{prompt.md,settings.json,mcp.json,agent-token}, all
// written fresh and read-only (0400, R3-L4). The token is minted for this start, bound to the job and its pane.
func (s *Service) writeJobFiles(ctx context.Context, job Job) (jobFiles, error) {
	dir, err := s.jobDir(job.ID)
	if err != nil {
		return jobFiles{}, err
	}
	files := jobFiles{prompt: filepath.Join(dir, "prompt.md"), settings: filepath.Join(dir, "settings.json")}
	// Every start (a restart too) records the layers it was given: this is the start that
	// "instructions changed since start" compares against.
	prompt, err := s.agentPrompt(ctx, job, true, true)
	if err != nil {
		return jobFiles{}, err
	}
	if err := writeReadOnly(files.prompt, []byte(prompt)); err != nil {
		return jobFiles{}, fmt.Errorf("writing the job prompt: %w", err)
	}
	withMCP := s.opts.Launch.Executable != "" && s.opts.Launch.AgentSocket != ""
	token, tokenFile := "", ""
	if withMCP && s.opts.Launch.Identity != nil {
		var err error
		token, err = s.opts.Launch.Identity.Mint(ctx, agentid.Binding{PaneID: job.PaneID, JobID: job.ID, Kind: agentid.KindStart})
		if err != nil {
			return jobFiles{}, fmt.Errorf("minting the job agent's token: %w", err)
		}
		tokenFile = filepath.Join(dir, "agent-token")
		if err := writeReadOnly(tokenFile, []byte(token+"\n")); err != nil {
			return jobFiles{}, fmt.Errorf("writing the job agent's token file: %w", err)
		}
	}
	guardRules := ""
	if s.opts.Launch.Guard != nil && withMCP {
		guardRules = filepath.Join(dir, "guard.json")
		if err := writeJSON(guardRules, s.opts.Launch.Guard); err != nil {
			return jobFiles{}, err
		}
	}
	// The job's attachment inbox is readable without a prompt (DESIGN.md 8.6).
	settings := agentcfg.AllowRead(agentcfg.ClaudeSettings(s.opts.Launch.Executable, s.opts.Launch.AgentSocket, job.ID, tokenFile, guardRules, withMCP),
		attach.Layout{StateDir: s.opts.Launch.StateDir}.JobDir(job.ID))
	if err := writeJSON(files.settings, settings); err != nil {
		return jobFiles{}, err
	}
	if withMCP {
		files.mcpConfig = filepath.Join(dir, "mcp.json")
		if err := writeJSON(files.mcpConfig, agentcfg.MCPConfig(s.opts.Launch.Executable, s.opts.Launch.AgentSocket, job.ID, token)); err != nil {
			return jobFiles{}, err
		}
	}
	return files, nil
}

// agentPrompt is what a job agent is told before its brief: the role prompt (or, with the
// instruction layers wired, the global, role and project layers; DESIGN.md 21.3) followed by
// the job protocol. mcp chooses the protocol's wording; record asks the layers to note this
// start. A failure to compose the layers fails the start rather than dropping the human's
// standing instructions without a word.
func (s *Service) agentPrompt(ctx context.Context, job Job, mcp, record bool) (string, error) {
	if s.opts.Instructions == nil {
		return s.opts.Roles.Prompt(job.Role, job.ID, mcp)
	}
	layers, err := s.opts.Instructions.JobLayers(ctx, job, record)
	if err != nil {
		return "", fmt.Errorf("composing the instructions of %s: %w", job.ID, err)
	}
	protocol, err := ProtocolText(job.ID, mcp)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(layers) + "\n\n" + protocol, nil
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := writeReadOnly(path, append(body, '\n')); err != nil {
		return err
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
	if prefix := s.opts.Launch.RemoteControlPrefix; prefix != "" {
		args = append(args, "--remote-control", RemoteControlName(prefix, job))
	}
	args = append(args, "--append-system-prompt-file", files.prompt, "--settings", files.settings)
	if files.mcpConfig != "" {
		args = append(args, "--mcp-config", files.mcpConfig)
	}
	return args
}

// remoteControlTitleRunes keeps a job's Remote Control name readable in a phone's session list.
const remoteControlTitleRunes = 40

// RemoteControlName is a job agent's Remote Control session name: the instance name, the job id
// and the start of its title, on one clean line (the title can be an agent's words).
func RemoteControlName(prefix string, job Job) string {
	title := termtext.Cap(termtext.Clean(job.Title), remoteControlTitleRunes)
	return strings.TrimSpace(prefix + " " + job.ID + " " + title)
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
//
// The choice and the reservation happen in one critical section (R2-C05): the job gets the
// orchestrator's pane and a dispatch claim before anything is typed, so two dispatchers cannot
// pick the same job (or two jobs), and the orchestrator's first job_update already finds its job.
// A brief that provably was not typed releases the job back to the queue; one whose typing is
// uncertain counts as sent (at most once).
func (s *Service) DispatchOrchestrator(ctx context.Context) {
	if s.opts.Orchestrator == nil {
		return
	}
	info := s.opts.Orchestrator()
	s.dispatchTo(ctx, StandingAgent{Name: OrchestratorName, PaneID: info.PaneID, Ready: info.Ready})
}

// dispatchTo is DispatchOrchestrator for any standing agent: the orchestrator, or a project's
// lead, which takes its own project's jobs the same way (DESIGN.md 21.2).
func (s *Service) dispatchTo(ctx context.Context, agent StandingAgent) {
	if s.opts.Herdr == nil || agent.PaneID == "" || !agent.Ready {
		return
	}
	cl, job, ok := s.reserveFor(ctx, agent)
	if !ok {
		return
	}
	path, err := s.writeBrief(ctx, job)
	if err != nil {
		s.opts.Logger.Warn("writing a brief for a standing agent", "agent", agent.Name, "job", job.ID, "err", err)
		s.finishQuietly(ctx, cl, func(r *store.JobRecord, _ *change) {
			r.PaneID, r.AgentName = "", "" // back in the queue, untouched
		})
		return
	}
	var result orch.PromptResult
	if claimErr := s.effect(ctx, cl, agent.PaneID, func(ctx context.Context) {
		result, err = orch.Prompt(ctx, s.opts.Herdr, agent.PaneID, orch.PromptRequest{Text: standingLine(job, path), ExpectAgent: agent.Name})
	}); claimErr != nil {
		return
	}
	outcome := typedOutcome(result, err)
	if outcome == typedNo {
		s.opts.Logger.Info("a standing agent could not take a job yet", "agent", agent.Name, "job", job.ID, "err", err)
		s.finishQuietly(ctx, cl, func(r *store.JobRecord, _ *change) {
			r.PaneID, r.AgentName = "", "" // back in the queue, untouched
		})
		return
	}
	s.finishQuietly(ctx, cl, func(r *store.JobRecord, c *change) {
		now := s.opts.Now()
		r.BriefSent, r.StartedAt, r.LastActivityAt = true, now, now
		if State(r.State) == StateQueued {
			r.State = string(StateRunning) // not over a disposition the agent declared meanwhile (R3-C04)
		}
		summary := "Assigned to the orchestrator"
		if agent.ProjectID != 0 {
			summary = "Assigned to the project's lead (" + agent.Name + ")"
		}
		if outcome == typedMaybe {
			summary += " (crowsnest lost track of the send; it is not typed again)"
		}
		c.event(EventLaunch, summary, map[string]any{"pane": r.PaneID})
	})
}

// reserveFor picks the oldest unassigned no-directory job waiting for agent and reserves it in
// its pane, unless the agent already holds a running (or reserved) job. It
// reads the store directly, not a page of the board (R2-C16).
func (s *Service) reserveFor(ctx context.Context, agent StandingAgent) (claim, Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, err := s.opts.Store.CountJobsHeldBy(ctx, agent.Name, []string{string(StateQueued), string(StateRunning)})
	if err != nil || held > 0 {
		return claim{}, Job{}, false
	}
	next, ok, err := s.oldestFor(ctx, agent)
	if err != nil || !ok {
		return claim{}, Job{}, false
	}
	var cl claim
	job, err := s.mutateLocked(ctx, FormatID(next.ID), func(r *store.JobRecord, c *change) error {
		return s.claimLocked(r, c, opDispatch, func(r *store.JobRecord, _ *change) error {
			if State(r.State) != StateQueued || r.Dir != "" || r.AgentName != "" {
				return errSkip
			}
			r.PaneID, r.AgentName = agent.PaneID, agent.Name
			return nil
		}, &cl)
	})
	if err != nil {
		return claim{}, Job{}, false
	}
	return cl, job, true
}
