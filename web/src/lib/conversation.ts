// Types and pure logic for conversations (DESIGN.md 7.2) and the orchestrator (section 7). The
// shapes mirror internal/transcript (Message, Page) and internal/orch (Status, PromptResult) on the
// server; they live here rather than in lib/types.ts so the fleet mirror there stays one file per
// concern.
//
// A conversation is fetched in pages: the first page (no cursor) is the newest messages; later
// pages carry only what changed after the cursor the client holds, including tool cards that
// gained their result. mergePage folds a page into what the client has.

/** What a message is. `pane` is the fallback when no transcript exists: the pane's text. */
export type MessageKind = "user" | "assistant" | "tool" | "thinking" | "pane";

/** How a user message got there: typed (none), or one of crowsnest's automatic headers. */
export type MessageTag = "poke" | "voice" | "answer" | "command";

export interface ToolCall {
  id: string;
  name: string;
  /** One short line from the input: the command, the file, the pattern. */
  summary: string;
  /** The input as indented JSON, truncated by the server. */
  input: string;
  result?: string;
  result_truncated?: boolean;
  is_error?: boolean;
  /** False until the tool's result is in the transcript. */
  done: boolean;
}

export interface Message {
  id: string;
  /** Position in the conversation; stable. */
  index: number;
  /** Revision; grows when the message is added or changed. */
  seq: number;
  kind: MessageKind;
  tag?: MessageTag;
  text?: string;
  tool?: ToolCall;
  at: string;
}

/** GET /agents/{target}/messages */
export interface MessagePage {
  source: "transcript" | "pane";
  session_id: string;
  cursor: string;
  /** Messages is the conversation from the start (or its newest part): replace, do not merge. */
  reset: boolean;
  /** A reset page that left older messages out. */
  earlier: boolean;
  /** More changes are waiting after `cursor`. */
  more: boolean;
  messages: Message[];
}

/** What the client holds for one conversation. */
export interface ConversationState {
  cursor: string;
  source: MessagePage["source"];
  sessionId: string;
  earlier: boolean;
  /** Sorted by index. */
  messages: Message[];
}

/** Folds a page into the state: a reset replaces it, otherwise messages merge by id. */
export function mergePage(previous: ConversationState | undefined, page: MessagePage): ConversationState {
  if (!previous || page.reset) {
    return {
      cursor: page.cursor,
      source: page.source,
      sessionId: page.session_id,
      earlier: page.earlier,
      messages: [...page.messages].sort((a, b) => a.index - b.index),
    };
  }
  if (page.messages.length === 0) return previous.cursor === page.cursor ? previous : { ...previous, cursor: page.cursor };
  const byId = new Map(previous.messages.map((message) => [message.id, message]));
  for (const message of page.messages) byId.set(message.id, message);
  return {
    ...previous,
    cursor: page.cursor,
    source: page.source,
    sessionId: page.session_id,
    messages: [...byId.values()].sort((a, b) => a.index - b.index),
  };
}

// ---- Orchestrator --------------------------------------------------------------------

export type OrchestratorState =
  | "disabled"
  | "starting"
  | "idle"
  | "working"
  | "blocked"
  | "needs_terminal"
  | "restarting"
  | "failed";

/** GET /orchestrator, and the data of an `orchestrator` event. */
export interface OrchestratorStatus {
  state: OrchestratorState;
  name: string;
  pane_id: string;
  session_id: string;
  dir: string;
  /** The model Claude was started with; empty means Claude's own default. */
  model?: string;
  detail?: string;
  restarts: number;
  updated_at: string;
}

/** Restart (same conversation) or New conversation: both relaunch Claude. */
export type RelaunchAction = "restart" | "new";

/** The confirmation each relaunch asks for (the Chat menu and the command palette say the same). */
export const relaunchConfirmations: Record<RelaunchAction, { title: string; body: string; button: string }> = {
  restart: {
    title: "Restart the orchestrator?",
    body: "Claude is stopped (anything it is doing is interrupted) and started again on the same conversation.",
    button: "Restart",
  },
  new: {
    title: "Start a new conversation?",
    body: "Claude is stopped and started with an empty conversation. The current one stays in its transcript file but leaves this view.",
    button: "New conversation",
  },
};

// ---- Sending -------------------------------------------------------------------------

/** POST /agents/{target}/prompt */
export interface PromptRequest {
  text: string;
  force_over_draft?: boolean;
  /** Transcribed speech (talk mode): the server adds the voice header and the talk flag. */
  voice?: boolean;
}

export interface PromptResult {
  pane_id: string;
  status: string;
  /** The agent was mid-turn; Claude reads the text when the turn ends. */
  queued: boolean;
  /** A turn was seen to start (or the text was queued). */
  started: boolean;
  note?: string;
}

/** The detail of a 409 DRAFT refusal: what is in the input box. */
export interface DraftDetail {
  input_found: boolean;
  present: boolean;
  text: string;
}

/** The detail of a 409 BLOCKED refusal: the dialog to answer instead. */
export interface BlockedDetail {
  pane_id: string;
  screen_hash: string;
  dialog: { present: boolean; options: { key: string; label: string }[]; form: boolean };
}

/**
 * The state shown in the conversation's status chip. The orchestrator has a few states of its
 * own; any other agent's comes straight from herdr.
 */
export type ChatState = "idle" | "working" | "blocked" | "needs_terminal" | "starting" | "failed" | "off" | "unknown";

export function chatStateOf(state: OrchestratorState | string | undefined): ChatState {
  switch (state) {
    case "idle":
    case "done":
      return "idle";
    case "working":
    case "blocked":
    case "needs_terminal":
    case "failed":
      return state;
    case "starting":
    case "restarting":
      return "starting";
    case "disabled":
      return "off";
    default:
      return "unknown";
  }
}

/** A message the conversation view shows, after the thinking filter. */
export function visibleMessages(messages: Message[], showThinking: boolean): Message[] {
  return showThinking ? messages : messages.filter((message) => message.kind !== "thinking");
}
