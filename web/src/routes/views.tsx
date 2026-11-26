// The views that are still empty states. Each later milestone replaces one: Jobs (M7), Inbox (M6).
// Fleet (routes/FleetView.tsx) and Chat (routes/ChatView.tsx) are real, and so is Terminal (M5), below.
import { Inbox, ListTodo } from "lucide-react";
import { TerminalPanel } from "../components/terminal/TerminalPanel";
import { EmptyState } from "../components/ui/EmptyState";
import { terminalPaths } from "../lib/api";

export function JobsView() {
  return (
    <EmptyState icon={ListTodo} title="No jobs yet">
      Give an agent a job and watch it here: what it is doing now, what it needs from you, and what it finished. Jobs
      arrive in a later build.
    </EmptyState>
  );
}

export function InboxView() {
  return (
    <EmptyState icon={Inbox} title="Nothing needs you">
      Questions from agents and terminal dialogs waiting for an answer will land here. Until then, agents that need you
      are flagged in Fleet.
    </EmptyState>
  );
}

/** The whole herdr session (the TUI in a PTY) in a terminal that fills the visible screen. */
export function TerminalView() {
  return <TerminalPanel path={terminalPaths.tui} label="herdr session terminal" fill="viewport" />;
}
