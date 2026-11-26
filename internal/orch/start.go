// Starting Claude: finding or creating the orchestrator's pane, writing the files it is started
// with, building the argument list, and calling agent.start.

package orch

import (
	"context"
	"embed"
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
)

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
	if err := os.MkdirAll(o.opts.Dir, 0o755); err != nil {
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

// paneEnv is the environment of the orchestrator's pane (DESIGN.md 7, step 2). CROWSNEST_SOCKET
// joins it with the agent socket (M6).
func (o *Orchestrator) paneEnv() map[string]string {
	env := map[string]string{"CROWSNEST": "1", "CROWSNEST_SESSION": o.opts.Session}
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
	instructions, settings, err := o.writeFiles()
	if err != nil {
		return o.fail(err)
	}
	sessionID, resuming := o.chooseSession(ctx, resume)
	args := o.startArgs(sessionID, resuming, instructions, settings)
	o.opts.Logger.Info("starting the orchestrator", "pane", paneID, "session", sessionID, "resume", resuming)
	startCtx, cancel := context.WithTimeout(ctx, startTimeoutMS*time.Millisecond+10*time.Second)
	defer cancel()
	agent, err := o.opts.Client.AgentStart(startCtx, herdr.AgentStartParams{
		Name: Name, Kind: "claude", PaneID: paneID, Args: args, TimeoutMS: startTimeoutMS,
	})
	switch {
	case err == nil:
		o.remember(ctx, paneID, sessionOf(agent, sessionID))
		return o.follow(agent), nil
	case errors.Is(err, herdr.ErrAgentNotReady):
		o.remember(ctx, paneID, sessionID)
		return o.update(func(s *Status) {
			s.State, s.Detail, s.PaneID, s.SessionID = StateNeedsTerminal, needsTerminalDetail, paneID, sessionID
		}), nil
	default:
		return o.fail(fmt.Errorf("starting Claude in pane %s: %w", paneID, err))
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

// startArgs is Claude's command line. It stays short on purpose: macOS refuses a start command
// longer than the terminal's line-input limit, so content goes in files (DESIGN.md 7, step 4).
func (o *Orchestrator) startArgs(sessionID string, resume bool, instructions, settings string) []string {
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
	return append(args, "--append-system-prompt-file", instructions, "--settings", settings)
}

// writeFiles renders CROWSNEST.md and writes the (for now empty) Claude settings file; M7 puts
// the supervisor's hooks in it. Both are rewritten on every start so an upgraded crowsnest
// brings its new instructions.
func (o *Orchestrator) writeFiles() (instructions, settings string, err error) {
	if err := os.MkdirAll(o.opts.StateDir, 0o700); err != nil {
		return "", "", fmt.Errorf("creating the state directory %s: %w", o.opts.StateDir, err)
	}
	instructions = filepath.Join(o.opts.StateDir, InstructionsFile)
	body, err := o.Instructions()
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(instructions, []byte(body), 0o600); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", instructions, err)
	}
	settings = filepath.Join(o.opts.StateDir, SettingsFile)
	if err := os.WriteFile(settings, []byte("{}\n"), 0o600); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", settings, err)
	}
	return instructions, settings, nil
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
