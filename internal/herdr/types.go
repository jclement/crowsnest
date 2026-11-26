// Package herdr is crowsnest's client for a herdr server: the Client interface every other
// package programs against, the typed shapes of herdr's socket API, the real implementation
// (SocketClient, NDJSON over the session's unix socket) and the session manager that finds or
// starts a named session. herdr/fake is the in-memory implementation behind --demo and tests.
//
// Shapes here follow `herdr api schema --json` (herdr 0.9.5, protocol 22), which is the authority:
// only the fields crowsnest reads are declared, and unknown fields are ignored so a newer herdr
// that adds fields does not break decoding. DESIGN.md section 5 is the specification.
package herdr

import (
	"encoding/json"
)

// ProtocolVersion is the herdr socket protocol these types were written against. A server
// reporting another protocol is logged, not refused: herdr has kept older fields stable so far.
const ProtocolVersion = 22

// AgentStatus is herdr's lifecycle state for an agent (or the roll-up for a pane, tab or
// workspace).
type AgentStatus string

// The agent states herdr reports. Idle and done both mean ready for input; blocked means herdr
// recognised an approval or question dialog; unknown means an agent may be present but herdr
// cannot classify it.
const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

// Ready reports whether an agent in this state is waiting for input.
func (s AgentStatus) Ready() bool { return s == StatusIdle || s == StatusDone }

// ReadSource selects which part of a pane's terminal a read returns.
type ReadSource string

// Read sources. Visible is the current screen; recent is scrollback plus screen; unwrapped
// joins soft-wrapped lines; detection is the region herdr's own state detection looks at.
const (
	SourceVisible         ReadSource = "visible"
	SourceRecent          ReadSource = "recent"
	SourceRecentUnwrapped ReadSource = "recent_unwrapped"
	SourceDetection       ReadSource = "detection"
)

// ReadFormat is plain text or text with ANSI escapes kept.
type ReadFormat string

// Read formats.
const (
	FormatText ReadFormat = "text"
	FormatANSI ReadFormat = "ansi"
)

// ---- Session objects -------------------------------------------------------------

// Pong is the answer to ping.
type Pong struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

// Worktree describes the git checkout a workspace was opened on, when it was.
type Worktree struct {
	RepoName     string `json:"repo_name"`
	RepoRoot     string `json:"repo_root"`
	CheckoutPath string `json:"checkout_path"`
	IsLinked     bool   `json:"is_linked_worktree"`
	RepoKey      string `json:"repo_key"`
}

// Workspace is one herdr workspace (a sidebar entry).
type Workspace struct {
	ID          string      `json:"workspace_id"`
	Number      int         `json:"number"`
	Label       string      `json:"label"`
	Focused     bool        `json:"focused"`
	PaneCount   int         `json:"pane_count"`
	TabCount    int         `json:"tab_count"`
	ActiveTabID string      `json:"active_tab_id"`
	AgentStatus AgentStatus `json:"agent_status"`
	Worktree    *Worktree   `json:"worktree,omitempty"`
}

// Tab is one tab inside a workspace.
type Tab struct {
	ID          string      `json:"tab_id"`
	WorkspaceID string      `json:"workspace_id"`
	Number      int         `json:"number"`
	Label       string      `json:"label"`
	Focused     bool        `json:"focused"`
	PaneCount   int         `json:"pane_count"`
	AgentStatus AgentStatus `json:"agent_status"`
}

// AgentSession is the agent's own session handle as herdr detected it. For Claude, Kind is "id"
// and Value is the Claude session id, which names its transcript file.
type AgentSession struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// Pane is one terminal pane. Agent is the detected agent kind ("claude", "codex"), empty for a
// plain shell.
type Pane struct {
	ID            string        `json:"pane_id"`
	TerminalID    string        `json:"terminal_id"`
	WorkspaceID   string        `json:"workspace_id"`
	TabID         string        `json:"tab_id"`
	Focused       bool          `json:"focused"`
	AgentStatus   AgentStatus   `json:"agent_status"`
	Revision      uint64        `json:"revision"`
	Agent         string        `json:"agent,omitempty"`
	AgentSession  *AgentSession `json:"agent_session,omitempty"`
	Cwd           string        `json:"cwd,omitempty"`
	ForegroundCwd string        `json:"foreground_cwd,omitempty"`
	Label         string        `json:"label,omitempty"`
	Title         string        `json:"title,omitempty"`
	TerminalTitle string        `json:"terminal_title_stripped,omitempty"`
}

// Agent is an agent herdr knows about: a pane whose occupant herdr recognised, plus the name
// crowsnest (or anyone) gave it.
type Agent struct {
	PaneID           string        `json:"pane_id"`
	TerminalID       string        `json:"terminal_id"`
	WorkspaceID      string        `json:"workspace_id"`
	TabID            string        `json:"tab_id"`
	Name             string        `json:"name,omitempty"`
	Agent            string        `json:"agent,omitempty"` // kind: claude, codex, …
	AgentStatus      AgentStatus   `json:"agent_status"`
	AgentSession     *AgentSession `json:"agent_session,omitempty"`
	Cwd              string        `json:"cwd,omitempty"`
	ForegroundCwd    string        `json:"foreground_cwd,omitempty"`
	Focused          bool          `json:"focused"`
	InteractiveReady bool          `json:"interactive_ready"`
	LaunchPending    bool          `json:"launch_pending"`
	Revision         uint64        `json:"revision"`
	StateChangeSeq   uint64        `json:"state_change_seq"`
	CompletionSeq    *uint64       `json:"completion_seq,omitempty"`
	Title            string        `json:"title,omitempty"`
	TerminalTitle    string        `json:"terminal_title_stripped,omitempty"`
}

// Snapshot is the whole session at one moment (session.snapshot).
type Snapshot struct {
	Version            string      `json:"version"`
	Protocol           int         `json:"protocol"`
	Workspaces         []Workspace `json:"workspaces"`
	Tabs               []Tab       `json:"tabs"`
	Panes              []Pane      `json:"panes"`
	Agents             []Agent     `json:"agents"`
	FocusedWorkspaceID string      `json:"focused_workspace_id,omitempty"`
	FocusedTabID       string      `json:"focused_tab_id,omitempty"`
	FocusedPaneID      string      `json:"focused_pane_id,omitempty"`
}

// Process is one foreground process in a pane.
type Process struct {
	PID     int      `json:"pid"`
	Name    string   `json:"name"`
	Argv    []string `json:"argv,omitempty"`
	Cmdline string   `json:"cmdline,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
}

// ProcessInfo is pane.process_info: the shell and whatever runs in the foreground of a pane.
type ProcessInfo struct {
	PaneID              string    `json:"pane_id"`
	ShellPID            int       `json:"shell_pid,omitempty"`
	ForegroundProcesses []Process `json:"foreground_processes"`
}

// ReadResult is the text of a pane read.
type ReadResult struct {
	PaneID      string     `json:"pane_id"`
	WorkspaceID string     `json:"workspace_id"`
	TabID       string     `json:"tab_id"`
	Source      ReadSource `json:"source"`
	Format      ReadFormat `json:"format"`
	Text        string     `json:"text"`
	Revision    uint64     `json:"revision"`
	Truncated   bool       `json:"truncated"`
}

// WorkspaceCreated is the result of workspace.create: the workspace, its first tab and the
// root pane of that tab.
type WorkspaceCreated struct {
	Workspace Workspace `json:"workspace"`
	Tab       Tab       `json:"tab"`
	RootPane  Pane      `json:"root_pane"`
}

// TabCreated is the result of tab.create.
type TabCreated struct {
	Tab      Tab  `json:"tab"`
	RootPane Pane `json:"root_pane"`
}

// ---- Request parameters ------------------------------------------------------------

// WorkspaceCreateParams are workspace.create's options; all are optional.
type WorkspaceCreateParams struct {
	Label string            `json:"label,omitempty"`
	Cwd   string            `json:"cwd,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
	Focus bool              `json:"focus,omitempty"`
}

// TabCreateParams are tab.create's options. WorkspaceID empty means herdr's focused workspace,
// so callers in crowsnest always set it.
type TabCreateParams struct {
	WorkspaceID string            `json:"workspace_id,omitempty"`
	Label       string            `json:"label,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Focus       bool              `json:"focus,omitempty"`
}

// ReadOptions select what a pane or agent read returns. Lines 0 means herdr's default.
// ANSI keeps escape sequences (format ansi, strip_ansi false), which draft detection needs.
type ReadOptions struct {
	Source ReadSource
	Lines  int
	ANSI   bool
}

// AgentStartParams are agent.start's arguments. PaneID must be a pane at a bare shell prompt.
type AgentStartParams struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	PaneID    string   `json:"pane_id"`
	Args      []string `json:"args,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"` // herdr accepts 3001..300000
}

// PromptWait asks agent.prompt to wait for one of Until after submitting. A nil PromptWait
// returns as soon as the text and Enter are written.
type PromptWait struct {
	Until     []AgentStatus `json:"until,omitempty"`
	TimeoutMS int           `json:"timeout_ms,omitempty"`
}

// ---- Events --------------------------------------------------------------------------

// Subscription is one entry of events.subscribe. PaneID is required for the per-pane types
// (pane.agent_status_changed, pane.output_matched, pane.scroll_changed) and must be empty for
// the rest: herdr rejects the whole subscription if either rule is broken.
type Subscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// Event subscription types that take no pane id: the lifecycle of workspaces, tabs and panes.
var LifecycleEventTypes = []string{
	"workspace.created", "workspace.updated", "workspace.metadata_updated", "workspace.renamed",
	"workspace.moved", "workspace.reordered", "workspace.closed", "workspace.focused",
	"worktree.created", "worktree.opened", "worktree.removed",
	"tab.created", "tab.closed", "tab.focused", "tab.renamed", "tab.moved",
	"pane.created", "pane.closed", "pane.updated", "pane.focused", "pane.moved", "pane.exited",
	"pane.agent_detected", "layout.updated",
}

// EventAgentStatusChanged is the per-pane subscription type for agent state changes.
const EventAgentStatusChanged = "pane.agent_status_changed"

// RequiresPane reports whether a subscription type needs a pane id.
func RequiresPane(subscriptionType string) bool {
	switch subscriptionType {
	case EventAgentStatusChanged, "pane.output_matched", "pane.scroll_changed":
		return true
	}
	return false
}

// Event is one pushed event. Kind is herdr's event name as sent ("pane_created",
// "pane.agent_status_changed"); PaneID and WorkspaceID are filled when the payload carries them;
// Data is the raw payload for consumers that need more. crowsnest treats events as
// invalidations, so most consumers read only Kind.
type Event struct {
	Kind        string          `json:"event"`
	PaneID      string          `json:"pane_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Status      AgentStatus     `json:"agent_status,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// EventStream is a live subscription. Events is closed when the stream ends; Err then says why
// (ErrEventsLost when herdr dropped events and the caller must resync, nil after Close).
type EventStream interface {
	Events() <-chan Event
	Err() error
	Close() error
}
