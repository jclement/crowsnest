// ChatStatusChip: the agent's state over its conversation (idle, working, blocked, needs
// terminal, …), driven by events. States herdr reports reuse the fleet's StatusDot shapes so a
// state looks the same everywhere; the orchestrator's own states get a plain chip.
import type { ChatState } from "../../lib/conversation";
import type { AgentStatus } from "../../lib/types";
import { Chip, StatusDot, type ChipTone } from "../ui/Chip";

const chatStates: Record<ChatState, { label: string; tone: ChipTone; dot?: AgentStatus; hint: string }> = {
  idle: { label: "Idle", tone: "neutral", dot: "idle", hint: "Ready for a message" },
  working: { label: "Working", tone: "accent", dot: "working", hint: "Running a turn; messages are queued" },
  blocked: { label: "Blocked", tone: "danger", dot: "blocked", hint: "Waiting at a dialog: needs you" },
  needs_terminal: { label: "Needs terminal", tone: "warning", dot: "blocked", hint: "Stopped at a start-up prompt; answer it in the terminal" },
  starting: { label: "Starting", tone: "neutral", dot: "unknown", hint: "Crowsnest is starting Claude" },
  failed: { label: "Not running", tone: "danger", hint: "Claude is not running; restart it from the menu" },
  off: { label: "Off", tone: "neutral", hint: "Started with --no-orchestrator" },
  unknown: { label: "Unknown", tone: "neutral", dot: "unknown", hint: "The state is not known yet" },
};

export function ChatStatusChip({ state }: { state: ChatState }) {
  const meta = chatStates[state];
  return (
    <Chip tone={meta.tone} title={meta.hint}>
      {meta.dot && <StatusDot status={meta.dot} className="scale-75" />}
      <span data-testid="chat-state">{meta.label}</span>
    </Chip>
  );
}
