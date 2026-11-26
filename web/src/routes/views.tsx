// The views that are still empty states: Jobs (M7). Fleet, Chat and Inbox have their own files in
// routes/, and Terminal (M5) is below.
import { ListTodo } from "lucide-react";
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

/** The whole herdr session (the TUI in a PTY) in a terminal that fills the visible screen. */
export function TerminalView() {
  return <TerminalPanel path={terminalPaths.tui} label="herdr session terminal" fill="viewport" />;
}
