// The four views as empty states. Each later milestone replaces one: Chat (M4), Inbox (M6),
// Fleet (M3), Terminal (M5).
import { Inbox, MessageSquare, Ship, SquareTerminal } from "lucide-react";
import { EmptyState } from "../components/EmptyState";

export function ChatView() {
  return (
    <EmptyState icon={MessageSquare} title="No conversation yet">
      The orchestrator's conversation appears here once crowsnest starts it in your herdr session.
    </EmptyState>
  );
}

export function InboxView() {
  return (
    <EmptyState icon={Inbox} title="Nothing needs you">
      Questions from agents and terminal dialogs waiting for an answer will land here.
    </EmptyState>
  );
}

export function FleetView() {
  return (
    <EmptyState icon={Ship} title="No fleet to show">
      Workspaces, tabs and agents in the herdr session will be listed here.
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
