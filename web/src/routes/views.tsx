// The views that are still empty states. Each later milestone replaces one: Jobs (M7), Inbox (M6),
// Terminal (M5). Fleet (routes/FleetView.tsx) and Chat (routes/ChatView.tsx) are real.
import { Inbox, ListTodo, SquareTerminal } from "lucide-react";
import { EmptyState } from "../components/ui/EmptyState";

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

export function TerminalView() {
  return (
    <EmptyState icon={SquareTerminal} title="No terminal attached">
      A live terminal on the herdr session opens here.
    </EmptyState>
  );
}
