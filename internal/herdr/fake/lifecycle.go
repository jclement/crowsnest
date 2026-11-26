// Agent lifecycle scripting for the fake: what the orchestrator's start-up and upkeep code needs
// to be tested against (DESIGN.md section 7). agent.start records its arguments, answers with the
// launch still pending (as herdr does over the socket; agent.get then shows it ready), and takes
// the Claude session id from --session-id or --resume, as real Claude does. A test can make the
// next start stop at a trust dialog, make an agent exit back to its shell, and read the
// environment a pane was created with.

package fake

import (
	"strings"

	"github.com/jclement/crowsnest/internal/herdr"
)

// StartNotReadyNext makes the next agent.start behave like Claude stopping at a folder-trust
// dialog: the agent is attached, blocked, still launching and never interactive-ready until
// ClearDialog lets it through.
func (f *Fake) StartNotReadyNext() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startNotReady = true
}

// ClearDialog marks an agent that stopped at a start-up dialog as ready and idle, as if someone
// answered the dialog in the terminal.
func (f *Fake) ClearDialog(target string) error {
	f.mu.Lock()
	p, err := f.resolveLocked(target)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	f.setStatusLocked(p, herdr.StatusIdle)
	p.agent.InteractiveReady, p.agent.LaunchPending = true, false
	f.mu.Unlock()
	f.emitStatus(p.info.ID, p.info.WorkspaceID, herdr.StatusIdle)
	return nil
}

// ExitAgent ends the agent in a pane: herdr forgets it and the pane is back at its shell, which
// is what /exit or a crash looks like from outside.
func (f *Fake) ExitAgent(paneID string) error {
	f.mu.Lock()
	p, err := f.paneLocked(paneID)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	p.agent = nil
	p.info.Agent, p.info.AgentSession, p.info.AgentStatus = "", nil, herdr.StatusUnknown
	p.processes = []herdr.Process{{PID: 3000 + len(f.panes), Name: "zsh", Argv: []string{"-zsh"}, Cwd: p.info.Cwd}}
	p.screen, p.ansi = shellPrompt(p.info.Cwd), ""
	f.rollUpLocked(p.info.WorkspaceID)
	f.bumpLocked(p)
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "pane_updated", PaneID: paneID, WorkspaceID: p.info.WorkspaceID})
	return nil
}

// LingerAgent ends the agent in a pane the way real herdr sometimes shows it right after /exit:
// the pane is back at its shell, but agent.get still answers for the name, with no session and
// an unknown state.
func (f *Fake) LingerAgent(paneID string) error {
	f.mu.Lock()
	p, err := f.paneLocked(paneID)
	if err != nil || p.agent == nil {
		f.mu.Unlock()
		return herdr.ErrAgentNotFound
	}
	p.agent.AgentStatus, p.agent.AgentSession = herdr.StatusUnknown, nil
	p.info.AgentStatus, p.info.AgentSession = herdr.StatusUnknown, nil
	p.processes = []herdr.Process{{PID: 3000 + len(f.panes), Name: "zsh", Argv: []string{"-zsh"}, Cwd: p.info.Cwd}}
	p.screen, p.ansi = shellPrompt(p.info.Cwd), ""
	f.bumpLocked(p)
	f.mu.Unlock()
	return nil
}

// onlyShell reports whether a pane's foreground holds nothing but a shell.
func onlyShell(processes []herdr.Process) bool {
	for _, process := range processes {
		if strings.TrimPrefix(process.Name, "-") != "zsh" && process.Name != "bash" && process.Name != "sh" {
			return false
		}
	}
	return true
}

// Starts returns every accepted agent.start, in order.
func (f *Fake) Starts() []herdr.AgentStartParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]herdr.AgentStartParams(nil), f.starts...)
}

// PaneEnv returns the environment a pane's workspace or tab was created with.
func (f *Fake) PaneEnv(paneID string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.panes[paneID]; ok {
		return p.env
	}
	return nil
}

// sessionFromArgs finds Claude's session id in agent.start args (--session-id X or --resume X),
// or returns fallback.
func sessionFromArgs(args []string, fallback string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session-id" || args[i] == "--resume" {
			return args[i+1]
		}
	}
	return fallback
}
