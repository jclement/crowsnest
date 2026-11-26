// AgentTerminal: the slot for a live terminal on an agent's pane (the pane-control WebSocket,
// DESIGN.md section 10). M3 ships the placeholder; M5/M8 replace the body with xterm.js,
// keeping this component's name and props.
import { SquareTerminal } from "lucide-react";
import type { Agent } from "../../lib/types";
import { EmptyState } from "../ui/EmptyState";

export interface AgentTerminalProps {
  /** The agent whose pane to attach to (agent.pane_id). */
  agent: Agent;
}

export function AgentTerminal({ agent }: AgentTerminalProps) {
  return (
    <EmptyState icon={SquareTerminal} title="Terminal" className="h-auto py-8">
      A live terminal on pane <span className="font-mono">{agent.pane_id}</span> will open here. Until then, the screen
      preview below shows what it displays.
    </EmptyState>
  );
}
