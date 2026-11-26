// Types for the data crowsnest's API returns (DESIGN.md section 10). The fleet shapes mirror
// herdr 0.9.5's own JSON (snake_case, as herdr sends it) because the server passes them through
// with only a little added: `has_dialog` and `has_transcript` on each agent, and the nesting of
// workspaces → tabs → panes → agent. Keep this file a plain mirror of the wire format; anything
// derived for display belongs in lib/fleet.ts.

/** herdr's agent states. `idle` and `done` both mean ready for input; `blocked` is a dialog. */
export type AgentStatus = "idle" | "done" | "working" | "blocked" | "unknown";

export const agentStatuses: readonly AgentStatus[] = ["blocked", "working", "done", "idle", "unknown"];

/** An agent detected in a pane. `agent` is the kind herdr recognised ("claude", "codex", …). */
export interface Agent {
  name: string;
  agent: string;
  agent_status: AgentStatus;
  pane_id: string;
  tab_id: string;
  workspace_id: string;
  cwd: string;
  /** The terminal title with escape sequences removed; Claude puts its current task here. */
  terminal_title_stripped: string;
  /** Claude's session id (the transcript file name), when herdr knows it. */
  agent_session: { value: string } | null;
  /** Increments on every status change; useful to tell a fresh "done" from an old one. */
  state_change_seq: number;
  /** Added by crowsnest: the screen currently shows a dialog (section 8.2). */
  has_dialog: boolean;
  /** Added by crowsnest: a Claude transcript was found, so a conversation view is possible. */
  has_transcript: boolean;
}

export interface Pane {
  pane_id: string;
  tab_id: string;
  workspace_id: string;
  terminal_id: string;
  cwd: string;
  /** The cwd of the foreground process, which differs from `cwd` after a `cd` in a subshell. */
  foreground_cwd: string;
  agent_status: AgentStatus;
  focused: boolean;
  revision: number;
  /** The agent running in this pane, or null for a plain shell. */
  agent: Agent | null;
}

export interface Tab {
  tab_id: string;
  workspace_id: string;
  label: string;
  number: number;
  agent_status: AgentStatus;
  focused: boolean;
  pane_count: number;
  panes: Pane[];
}

export interface Workspace {
  workspace_id: string;
  label: string;
  number: number;
  active_tab_id: string;
  agent_status: AgentStatus;
  focused: boolean;
  pane_count: number;
  tab_count: number;
  tabs: Tab[];
}

/** GET /api/v1/fleet */
export interface Fleet {
  session: string;
  herdr_version: string;
  workspaces: Workspace[];
}

/** One numbered option of a terminal dialog: pressing `digit` chooses it. */
export interface DialogOption {
  digit: string;
  text: string;
}

export interface ScreenDialog {
  options: DialogOption[];
  /** A multi-question form: the UI offers "open terminal" only (section 8.2). */
  multi_form: boolean;
}

/** GET /api/v1/agents/{target}/screen */
export interface AgentScreen {
  /** The visible screen as plain text, verbatim. */
  text: string;
  /** Identifies this exact screen; a key press must quote it (409 CONFLICT if it changed). */
  hash: string;
  status: AgentStatus;
  dialog: ScreenDialog | null;
  /** Whether the agent's input box holds text typed at the terminal (section 7.3). */
  draft: "empty" | "draft" | "unknown";
}

/** POST /api/v1/agents/{target}/keys */
export interface SendKeysRequest {
  keys: string[];
  screen_hash: string;
}

/** The names of the events on /api/v1/events (DESIGN.md section 10). */
export const eventNames = ["hello", "heartbeat", "job", "fleet", "message", "question", "orchestrator", "poke", "say"] as const;
export type EventName = (typeof eventNames)[number];
