// The fleet view: what GET /api/v1/fleet returns and what a `fleet` SSE event carries. It is the
// herdr snapshot reshaped for a UI (workspaces → tabs → panes, each pane with its agent), plus
// what crowsnest adds from the screen: whether a dialog is up.

package fleet

import (
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

// Fleet is the whole session as the UI sees it.
type Fleet struct {
	Session      string       `json:"session"`       // herdr session name
	HerdrVersion string       `json:"herdr_version"` // server version from the snapshot
	Revision     uint64       `json:"revision"`      // increases by one on every change crowsnest sees
	UpdatedAt    time.Time    `json:"updated_at"`    // when this revision was observed
	Workspaces   []Workspace  `json:"workspaces"`
	Agents       []AgentEntry `json:"agents"` // every agent, flat, for lists and badges
	Counts       Counts       `json:"counts"`
	// Error is set while herdr is not answering; the rest is then the last good picture.
	Error string `json:"error,omitempty"`
}

// Counts summarise agent states for badges.
type Counts struct {
	Agents     int `json:"agents"`
	Working    int `json:"working"`
	Blocked    int `json:"blocked"` // herdr says blocked, or a dialog is on screen
	Ready      int `json:"ready"`   // idle or done
	Unknown    int `json:"unknown"`
	Dialogs    int `json:"dialogs"`
	Settled    int `json:"settled"` // done (finished a turn and not yet looked at)
	Panes      int `json:"panes"`
	Workspaces int `json:"workspaces"`
}

// Workspace is one herdr workspace and its tabs.
type Workspace struct {
	ID          string            `json:"id"`
	Number      int               `json:"number"`
	Label       string            `json:"label"`
	Focused     bool              `json:"focused"`
	AgentStatus herdr.AgentStatus `json:"agent_status"`
	Worktree    *herdr.Worktree   `json:"worktree,omitempty"`
	Tabs        []Tab             `json:"tabs"`
}

// Tab is one tab and its panes.
type Tab struct {
	ID          string            `json:"id"`
	Number      int               `json:"number"`
	Label       string            `json:"label"`
	Focused     bool              `json:"focused"`
	AgentStatus herdr.AgentStatus `json:"agent_status"`
	Panes       []Pane            `json:"panes"`
}

// Pane is one terminal; Agent is nil for a plain shell.
type Pane struct {
	ID          string            `json:"id"`
	Cwd         string            `json:"cwd"`
	Title       string            `json:"title"`
	Focused     bool              `json:"focused"`
	AgentStatus herdr.AgentStatus `json:"agent_status"`
	Agent       *Agent            `json:"agent"`
}

// Agent is the agent in a pane.
type Agent struct {
	Name string `json:"name"` // herdr agent name; empty for an unnamed agent
	// Target is how to address this agent in /agents/{target}: its name, or its pane id.
	Target           string            `json:"target"`
	Kind             string            `json:"kind"` // claude, codex, …
	Status           herdr.AgentStatus `json:"status"`
	SessionID        string            `json:"session_id,omitempty"` // Claude's session id
	Title            string            `json:"title"`
	HasDialog        bool              `json:"has_dialog"`
	HasTranscript    bool              `json:"has_transcript"` // a Claude session id is known
	InteractiveReady bool              `json:"interactive_ready"`
}

// AgentEntry is an agent with where it lives, for the flat list.
type AgentEntry struct {
	Agent
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Workspace   string `json:"workspace"` // workspace label
	Cwd         string `json:"cwd"`
	NeedsYou    bool   `json:"needs_you"` // blocked or showing a dialog
}

// HasPane reports whether paneID names a pane in this fleet. The terminal routes use it to refuse
// a pane id before anything is spawned with it.
func (f Fleet) HasPane(paneID string) bool {
	for _, workspace := range f.Workspaces {
		for _, tab := range workspace.Tabs {
			for _, pane := range tab.Panes {
				if pane.ID == paneID {
					return true
				}
			}
		}
	}
	return false
}

// buildFleet reshapes a snapshot. dialogs says which panes show a dialog on screen.
func buildFleet(session string, snapshot herdr.Snapshot, dialogs map[string]bool) Fleet {
	agentsByPane := make(map[string]herdr.Agent, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		agentsByPane[agent.PaneID] = agent
	}
	panesByTab := map[string][]herdr.Pane{}
	for _, pane := range snapshot.Panes {
		panesByTab[pane.TabID] = append(panesByTab[pane.TabID], pane)
	}
	tabsByWorkspace := map[string][]herdr.Tab{}
	for _, tab := range snapshot.Tabs {
		tabsByWorkspace[tab.WorkspaceID] = append(tabsByWorkspace[tab.WorkspaceID], tab)
	}

	fleet := Fleet{Session: session, HerdrVersion: snapshot.Version, Workspaces: []Workspace{}, Agents: []AgentEntry{}}
	for _, ws := range snapshot.Workspaces {
		workspace := Workspace{ID: ws.ID, Number: ws.Number, Label: ws.Label, Focused: ws.Focused, AgentStatus: ws.AgentStatus, Worktree: ws.Worktree, Tabs: []Tab{}}
		for _, t := range tabsByWorkspace[ws.ID] {
			tab := Tab{ID: t.ID, Number: t.Number, Label: t.Label, Focused: t.Focused, AgentStatus: t.AgentStatus, Panes: []Pane{}}
			for _, p := range panesByTab[t.ID] {
				pane := buildPane(p, agentsByPane, dialogs)
				tab.Panes = append(tab.Panes, pane)
				if pane.Agent != nil {
					fleet.Agents = append(fleet.Agents, AgentEntry{
						Agent: *pane.Agent, PaneID: p.ID, TabID: t.ID, WorkspaceID: ws.ID, Workspace: ws.Label, Cwd: pane.Cwd,
						NeedsYou: pane.Agent.Status == herdr.StatusBlocked || pane.Agent.HasDialog,
					})
				}
			}
			workspace.Tabs = append(workspace.Tabs, tab)
		}
		fleet.Workspaces = append(fleet.Workspaces, workspace)
	}
	fleet.Counts = countFleet(fleet, len(snapshot.Panes))
	return fleet
}

func buildPane(p herdr.Pane, agentsByPane map[string]herdr.Agent, dialogs map[string]bool) Pane {
	pane := Pane{ID: p.ID, Cwd: firstNonEmpty(p.ForegroundCwd, p.Cwd), Title: firstNonEmpty(p.Label, p.Title, p.TerminalTitle), Focused: p.Focused, AgentStatus: p.AgentStatus}
	agent, ok := agentsByPane[p.ID]
	if !ok {
		return pane
	}
	view := &Agent{
		Name: agent.Name, Target: firstNonEmpty(agent.Name, agent.PaneID), Kind: agent.Agent, Status: agent.AgentStatus,
		Title: firstNonEmpty(agent.Title, agent.TerminalTitle), HasDialog: dialogs[p.ID], InteractiveReady: agent.InteractiveReady,
	}
	if session := agent.AgentSession; session != nil && session.Kind == "id" {
		view.SessionID = session.Value
		view.HasTranscript = agent.Agent == "claude"
	}
	pane.Agent = view
	return pane
}

func countFleet(fleet Fleet, panes int) Counts {
	counts := Counts{Agents: len(fleet.Agents), Panes: panes, Workspaces: len(fleet.Workspaces)}
	for _, entry := range fleet.Agents {
		switch {
		case entry.NeedsYou:
			counts.Blocked++
		case entry.Status == herdr.StatusWorking:
			counts.Working++
		case entry.Status.Ready():
			counts.Ready++
		default:
			counts.Unknown++
		}
		if entry.HasDialog {
			counts.Dialogs++
		}
		if entry.Status == herdr.StatusDone {
			counts.Settled++
		}
	}
	return counts
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
