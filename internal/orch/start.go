// Starting Claude: finding or creating the orchestrator's pane, writing the files it is started
// with, building the argument list, and calling agent.start.

package orch

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/transcript"
)

//go:embed templates/CROWSNEST.md
var templates embed.FS

// File names under StateDir.
const (
	InstructionsFile = "CROWSNEST.md"
	SettingsFile     = "claude-settings.json"
	MCPConfigFile    = "mcp.json"
)

// MCPAllowRule pre-approves every tool of crowsnest's MCP server in the orchestrator's settings,
// so `ask` never stops at a permission prompt (DESIGN.md 6.2; verified in M6 under
// --permission-mode auto, see section 7.6).
const MCPAllowRule = "mcp__crowsnest__*"

// shellNames are the processes that count as "a bare shell" in a pane.
var shellNames = map[string]bool{"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true}

// findShellPane returns a pane at a bare shell for the orchestrator: the pane it last ran in,
// else a shell pane of the orchestrator workspace, else a new tab there, else a new workspace.
func (o *Orchestrator) findShellPane(ctx context.Context) (string, error) {
	if paneID, err := o.opts.KV.GetKV(ctx, kvPaneID); err == nil && o.atShell(ctx, paneID) {
		return paneID, nil
	}
	workspaceID, err := o.findWorkspace(ctx)
	if err != nil {
		return "", err
	}
	if workspaceID != "" {
		if paneID := o.shellPaneIn(ctx, workspaceID); paneID != "" {
			return paneID, nil
		}
	}
	paneID, err := o.createPane(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	return paneID, o.waitForShell(ctx, paneID)
}

func (o *Orchestrator) findWorkspace(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	workspaces, err := o.opts.Client.WorkspaceList(ctx)
	if err != nil {
		return "", fmt.Errorf("listing workspaces: %w", err)
	}
	for _, workspace := range workspaces {
		if workspace.Label == WorkspaceLabel {
			return workspace.ID, nil
		}
	}
	return "", nil
}

func (o *Orchestrator) shellPaneIn(ctx context.Context, workspaceID string) string {
	listCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	panes, err := o.opts.Client.PaneList(listCtx, workspaceID)
	if err != nil {
		return ""
	}
	for _, pane := range panes {
		if pane.Agent == "" && o.atShell(ctx, pane.ID) {
			return pane.ID
		}
	}
	return ""
}

// createPane makes the orchestrator a pane: a tab in its workspace when that exists (and has no
// free shell), otherwise the workspace itself. Either way the pane gets the crowsnest env.
func (o *Orchestrator) createPane(ctx context.Context, workspaceID string) (string, error) {
	if err := os.MkdirAll(o.opts.Dir, 0o750); err != nil {
		return "", fmt.Errorf("creating the orchestrator directory %s: %w", o.opts.Dir, err)
	}
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	if workspaceID != "" {
		created, err := o.opts.Client.TabCreate(ctx, herdr.TabCreateParams{
			WorkspaceID: workspaceID, Label: WorkspaceLabel, Cwd: o.opts.Dir, Env: o.paneEnv(),
		})
		if err != nil {
			return "", fmt.Errorf("creating a tab for the orchestrator: %w", err)
		}
		return created.RootPane.ID, nil
	}
	created, err := o.opts.Client.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{
		Label: WorkspaceLabel, Cwd: o.opts.Dir, Env: o.paneEnv(),
	})
	if err != nil {
		return "", fmt.Errorf("creating the orchestrator workspace: %w", err)
	}
	return created.RootPane.ID, nil
}

// paneEnv is the environment of the orchestrator's pane (DESIGN.md 7, step 2): the agent socket
// so `crowsnest ask` can reach the server, and the binary's directory first on PATH so it resolves.
func (o *Orchestrator) paneEnv() map[string]string {
	env := map[string]string{"CROWSNEST": "1", "CROWSNEST_SESSION": o.opts.Session}
	if o.opts.AgentSocket != "" {
		env["CROWSNEST_SOCKET"] = o.opts.AgentSocket
	}
	if o.opts.BinDir != "" {
		env["PATH"] = o.opts.BinDir + string(os.PathListSeparator) + o.opts.BasePath
	}
	return env
}

// atShell reports whether a pane's foreground holds only a shell.
func (o *Orchestrator) atShell(ctx context.Context, paneID string) bool {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	info, err := o.opts.Client.PaneProcessInfo(ctx, paneID)
	if err != nil {
		return false
	}
	for _, process := range info.ForegroundProcesses {
		if !shellNames[strings.TrimPrefix(filepath.Base(process.Name), "-")] {
			return false
		}
	}
	return true
}

// waitForShell polls until the pane is at a bare shell, for up to ShellWait. A freshly created
// pane needs a moment for its shell to start (DESIGN.md 5).
func (o *Orchestrator) waitForShell(ctx context.Context, paneID string) error {
	deadline := o.opts.Now().Add(o.opts.ShellWait)
	for {
		if o.atShell(ctx, paneID) {
			return nil
		}
		if o.opts.Now().After(deadline) {
			return fmt.Errorf("pane %s: %w after %s", paneID, errNoShell, o.opts.ShellWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// ---- agent.start -----------------------------------------------------------------------

// start writes the start-up files and starts Claude in paneID.
func (o *Orchestrator) start(ctx context.Context, paneID string, resume bool) (Status, error) {
	files, err := o.writeFiles()
	if err != nil {
		return o.fail(err)
	}
	sessionID, resuming := o.chooseSession(ctx, resume)
	args := o.startArgs(sessionID, resuming, files)
	o.mu.Lock()
	prior := o.knownSession
	o.mu.Unlock()
	o.opts.Logger.Info("starting the orchestrator", "pane", paneID, "session", sessionID, "resume", resuming)
	agent, err := o.agentStart(ctx, herdr.AgentStartParams{Name: Name, Kind: "claude", PaneID: paneID, Args: args, TimeoutMS: startTimeoutMS})
	if err == nil {
		agent, err = o.waitForLaunch(ctx, agent)
	}
	switch {
	case err == nil:
		// The id passed to Claude is the truth, not herdr's report: that can still be the
		// previous conversation's, or missing, right after a launch (R1-C02).
		o.remember(ctx, paneID, sessionID)
		return o.followLaunch(agent, sessionID, prior), nil
	case errors.Is(err, herdr.ErrAgentNotReady):
		o.remember(ctx, paneID, sessionID)
		return o.update(func(s *Status) {
			o.launch = newLaunchBaseline(sessionID, prior)
			s.State, s.Detail, s.PaneID, s.SessionID = StateNeedsTerminal, needsTerminalDetail, paneID, sessionID
		}), nil
	default:
		return o.fail(fmt.Errorf("starting Claude in pane %s: %w", paneID, err))
	}
}

// herdr's answers to agent.start that are worth retrying for a short while (both found in M4).
const (
	codeAgentNameTaken = "agent_name_taken" // another (dead) agent record holds the name
	codeAgentPaneBusy  = "agent_pane_busy"  // the pane's shell is not an available prompt yet
)

// startRetryWait bounds how long a start keeps retrying those answers.
const startRetryWait = 15 * time.Second

// agentStart calls agent.start, riding out two transient refusals seen against real herdr:
//
//   - agent_pane_busy right after a workspace is created: the shell process exists (so the pane
//     passed the bare-shell check) but herdr does not count it as an available prompt yet. The
//     start is simply retried.
//   - agent_name_taken right after Claude exits: herdr keeps the named record for a while, so the
//     dead record's name is cleared and the start retried. Only a record whose pane is at a bare
//     shell is touched.
func (o *Orchestrator) agentStart(ctx context.Context, params herdr.AgentStartParams) (herdr.Agent, error) {
	deadline := o.opts.Now().Add(startRetryWait)
	for {
		startCtx, cancel := context.WithTimeout(ctx, startTimeoutMS*time.Millisecond+10*time.Second)
		agent, err := o.opts.Client.AgentStart(startCtx, params)
		cancel()
		var herdrErr *herdr.Error
		if !errors.As(err, &herdrErr) || o.opts.Now().After(deadline) {
			return agent, err
		}
		switch herdrErr.Code {
		case codeAgentNameTaken:
			o.releaseStaleName(ctx)
		case codeAgentPaneBusy:
		default:
			return agent, err
		}
		select {
		case <-ctx.Done():
			return agent, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// releaseStaleName clears the orchestrator name from a record whose Claude has exited.
func (o *Orchestrator) releaseStaleName(ctx context.Context) {
	stale, alive, err := o.liveAgent(ctx)
	if err != nil || alive || stale.PaneID == "" {
		return
	}
	renameCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	if _, err := o.opts.Client.AgentRename(renameCtx, stale.PaneID, ""); err != nil {
		o.opts.Logger.Debug("clearing a stale orchestrator name", "pane", stale.PaneID, "err", err)
	}
}

// waitForLaunch waits for a started agent to finish launching. Over the socket agent.start
// answers at once with launch_pending set (the CLI is what waits; found in M4), and Claude takes a
// few seconds to boot, so the start-up sequence polls until herdr calls the agent
// interactive-ready. An agent still not ready when the launch timeout passes, or one sitting at a
// dialog before it ever became ready (folder trust, login), is reported as agent_not_ready.
func (o *Orchestrator) waitForLaunch(ctx context.Context, agent herdr.Agent) (herdr.Agent, error) {
	deadline := o.opts.Now().Add(o.opts.LaunchTimeout)
	for {
		if !agent.LaunchPending && agent.InteractiveReady {
			return agent, nil
		}
		if !agent.InteractiveReady && agent.AgentStatus == herdr.StatusBlocked {
			return agent, fmt.Errorf("the agent is at a start-up dialog: %w", herdr.ErrAgentNotReady)
		}
		if o.opts.Now().After(deadline) {
			return agent, fmt.Errorf("the agent did not become ready within %s: %w", o.opts.LaunchTimeout, herdr.ErrAgentNotReady)
		}
		select {
		case <-ctx.Done():
			return agent, ctx.Err()
		case <-time.After(launchPollInterval):
		}
		next, err := o.getAgent(ctx)
		if err != nil && !herdr.IsNotFound(err) {
			return agent, fmt.Errorf("waiting for Claude to start: %w", err)
		}
		if err == nil {
			agent = next
		}
	}
}

// chooseSession returns the session id to start with, and whether that is a resume: the stored
// one when asked to resume and its transcript exists, else a new one.
func (o *Orchestrator) chooseSession(ctx context.Context, resume bool) (string, bool) {
	stored, err := o.opts.KV.GetKV(ctx, kvSessionID)
	if resume && err == nil && stored != "" && o.opts.ClaudeConfigDir != "" {
		if _, locateErr := transcript.Locate(o.opts.ClaudeConfigDir, o.opts.Dir, stored); locateErr == nil {
			return stored, true
		}
	}
	return uuid.NewString(), false
}

// startFiles are the paths of the files Claude is started with.
type startFiles struct {
	instructions string // CROWSNEST.md, appended to the system prompt
	settings     string // claude-settings.json: the MCP allow rule (and M7's hooks)
	mcpConfig    string // mcp.json naming `crowsnest mcp`; empty when there is no socket
}

// startArgs is Claude's command line. It stays short on purpose: macOS refuses a start command
// longer than the terminal's line-input limit, so content goes in files (DESIGN.md 7, step 4).
func (o *Orchestrator) startArgs(sessionID string, resume bool, files startFiles) []string {
	flag := "--session-id"
	if resume {
		flag = "--resume"
	}
	args := []string{flag, sessionID}
	if o.opts.PermissionMode != "" {
		args = append(args, "--permission-mode", o.opts.PermissionMode)
	}
	if o.opts.Model != "" {
		args = append(args, "--model", o.opts.Model)
	}
	args = append(args, "--append-system-prompt-file", files.instructions, "--settings", files.settings)
	if files.mcpConfig != "" {
		args = append(args, "--mcp-config", files.mcpConfig)
	}
	return args
}

// writeFiles renders CROWSNEST.md and writes the Claude settings file and, when there is an agent
// socket, the MCP config. All are rewritten on every start so an upgraded crowsnest brings its
// new instructions. M7 adds the supervisor's hooks to the settings.
func (o *Orchestrator) writeFiles() (startFiles, error) {
	if err := os.MkdirAll(o.opts.StateDir, 0o700); err != nil {
		return startFiles{}, fmt.Errorf("creating the state directory %s: %w", o.opts.StateDir, err)
	}
	files := startFiles{
		instructions: filepath.Join(o.opts.StateDir, InstructionsFile),
		settings:     filepath.Join(o.opts.StateDir, SettingsFile),
	}
	body, err := o.Instructions()
	if err != nil {
		return startFiles{}, err
	}
	if err := writePrivate(files.instructions, []byte(body)); err != nil {
		return startFiles{}, err
	}
	if err := writeJSONFile(files.settings, ClaudeSettings(o.mcpEnabled())); err != nil {
		return startFiles{}, err
	}
	if o.mcpEnabled() {
		files.mcpConfig = filepath.Join(o.opts.StateDir, MCPConfigFile)
		if err := writeJSONFile(files.mcpConfig, MCPConfig(o.opts.Executable, o.opts.AgentSocket)); err != nil {
			return startFiles{}, err
		}
	}
	return files, nil
}

func (o *Orchestrator) mcpEnabled() bool {
	return o.opts.Executable != "" && o.opts.AgentSocket != ""
}

// ClaudeSettings is the --settings file: the allow rule for crowsnest's MCP tools when they are
// offered. (M7 adds the hooks.)
func ClaudeSettings(withMCP bool) map[string]any {
	if !withMCP {
		return map[string]any{}
	}
	return map[string]any{"permissions": map[string]any{"allow": []string{MCPAllowRule}}}
}

// MCPConfig is the --mcp-config file: one stdio server, `<executable> mcp`, told where the agent
// socket is. The pane's own environment (HERDR_PANE_ID) reaches it too, which is how the server
// knows which pane is asking.
func MCPConfig(executable, socket string) map[string]any {
	return map[string]any{"mcpServers": map[string]any{
		"crowsnest": map[string]any{
			"type":    "stdio",
			"command": executable,
			"args":    []string{"mcp"},
			"env":     map[string]string{"CROWSNEST_SOCKET": socket},
		},
	}}
}

func writeJSONFile(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return writePrivate(path, append(body, '\n'))
}

func writePrivate(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Instructions renders the embedded CROWSNEST.md template for this session.
func (o *Orchestrator) Instructions() (string, error) {
	tmpl, err := template.ParseFS(templates, "templates/"+InstructionsFile)
	if err != nil {
		return "", fmt.Errorf("parsing the instructions template: %w", err)
	}
	var out strings.Builder
	data := map[string]string{"Session": o.opts.Session, "Dir": o.opts.Dir, "Version": o.opts.Version}
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("rendering the instructions: %w", err)
	}
	return out.String(), nil
}
