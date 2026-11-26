// Types for the data crowsnest's API returns (DESIGN.md section 10). The server is the authority:
// these mirror the Go structs in internal/fleet (view.go, screen.go) and internal/httpd
// (agents.go) field for field, in their JSON spelling. Keep this file a plain mirror of the wire
// format; anything derived for display belongs in lib/fleet.ts.

/** herdr's agent states. `idle` and `done` both mean ready for input; `blocked` is a dialog. */
export type AgentStatus = "idle" | "done" | "working" | "blocked" | "unknown";

export const agentStatuses: readonly AgentStatus[] = ["blocked", "working", "done", "idle", "unknown"];

/** The agent in a pane, as it sits in the workspace tree (fleet.Agent). */
export interface PaneAgent {
  /** herdr agent name; empty for an unnamed agent. */
  name: string;
  /** How the server addresses this agent in /agents/{target}: its name, or its pane id. */
  target: string;
  /** The kind herdr recognised: "claude", "codex", … */
  kind: string;
  status: AgentStatus;
  /** Claude's session id (the transcript file name), when herdr knows it. */
  session_id?: string;
  /** The terminal title; Claude puts its current task here. */
  title: string;
  /** The screen currently shows a dialog (DESIGN.md section 8.2). */
  has_dialog: boolean;
  /** A Claude session id is known, so a conversation view is possible. */
  has_transcript: boolean;
  interactive_ready: boolean;
}

/** An agent with where it lives: one entry of the fleet's flat `agents` list (fleet.AgentEntry). */
export interface Agent extends PaneAgent {
  pane_id: string;
  tab_id: string;
  workspace_id: string;
  /** The workspace's label. */
  workspace: string;
  cwd: string;
  /** Blocked, or showing a dialog. */
  needs_you: boolean;
}

/** One terminal; `agent` is null for a plain shell. */
export interface Pane {
  id: string;
  cwd: string;
  title: string;
  focused: boolean;
  agent_status: AgentStatus;
  agent: PaneAgent | null;
}

export interface Tab {
  id: string;
  number: number;
  label: string;
  focused: boolean;
  agent_status: AgentStatus;
  panes: Pane[];
}

/** A git worktree behind a workspace, as herdr reports it. */
export interface Worktree {
  repo_name: string;
  repo_root: string;
  checkout_path: string;
  is_linked_worktree: boolean;
  repo_key: string;
}

export interface Workspace {
  id: string;
  number: number;
  label: string;
  focused: boolean;
  agent_status: AgentStatus;
  worktree?: Worktree;
  tabs: Tab[];
}

/** Agent states summed for badges. `blocked` includes agents showing a dialog. */
export interface FleetCounts {
  agents: number;
  working: number;
  blocked: number;
  ready: number;
  unknown: number;
  dialogs: number;
  settled: number;
  panes: number;
  workspaces: number;
}

/** GET /api/v1/fleet, and the data of every `fleet` event. */
export interface Fleet {
  session: string;
  herdr_version: string;
  /** Increases on every change the server sees. */
  revision: number;
  updated_at: string;
  workspaces: Workspace[];
  /** Every agent, flat, in tree order. */
  agents: Agent[];
  counts: FleetCounts;
  /** Set while herdr is not answering; the rest is then the last good picture. */
  error?: string;
}

/** One numbered option of a terminal dialog: pressing `key` (a digit) chooses it. */
export interface DialogOption {
  key: string;
  label: string;
  /** The highlighted (cursor) option. */
  selected: boolean;
  /** Set by the server when the label itself is risky ("stops asking", "rm -rf", …). */
  warning?: string;
}

export interface ScreenDialog {
  present: boolean;
  options: DialogOption[];
  /** A multi-question form or multi-select list: the UI offers "open terminal" only (8.2). */
  form: boolean;
  /** Risky phrases found anywhere in the dialog. */
  warnings: string[];
}

/** Text typed at the terminal and waiting in the agent's input box (section 7.3). */
export interface Draft {
  input_found: boolean;
  present: boolean;
  text: string;
}

/** GET /api/v1/agents/{target}/screen */
export interface AgentScreen {
  target: string;
  pane_id: string;
  /** Agent name; empty for an unnamed agent or a plain pane. */
  name: string;
  /** Agent kind; empty for a plain pane. */
  kind: string;
  status: AgentStatus;
  /** The visible screen as plain text, verbatim. */
  text: string;
  /** The visible screen with colours. */
  ansi: string;
  /** Identifies this exact screen; a key press must quote it (409 CONFLICT if it changed). */
  screen_hash: string;
  revision: number;
  dialog: ScreenDialog;
  draft: Draft;
}

/** POST /api/v1/agents/{target}/keys */
export interface SendKeysRequest {
  keys: string[];
  screen_hash: string;
}

/** What POST /keys confirms: the keys sent, and the screen hash they were checked against. */
export interface KeysResponse {
  sent: string[];
  pane_id: string;
  screen_hash: string;
}

/** The names of the events on /api/v1/events (DESIGN.md section 10). */
export const eventNames = ["hello", "heartbeat", "job", "fleet", "message", "question", "orchestrator", "poke", "journal", "say", "schedule", "spend", "attachment", "approval", "sys", "system"] as const;
export type EventName = (typeof eventNames)[number];
