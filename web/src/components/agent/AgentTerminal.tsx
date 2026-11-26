// AgentTerminal: a live terminal on one agent's pane, through the pane-control WebSocket
// (DESIGN.md section 10). It is the phone-friendly terminal: just that pane, at the width the
// screen has. Opening it takes the pane's controller seat (herdr allows one); a browser that
// already had it is told "taken over" and offers Reconnect rather than grabbing it back.
import { terminalPaths } from "../../lib/api";
import type { Agent } from "../../lib/types";
import { TerminalPanel } from "../terminal/TerminalPanel";

export interface AgentTerminalProps {
  /** The agent whose pane to attach to (agent.pane_id). */
  agent: Agent;
}

export function AgentTerminal({ agent }: AgentTerminalProps) {
  return (
    <TerminalPanel
      path={terminalPaths.pane(agent.pane_id)}
      label={`Terminal for ${agent.name || agent.pane_id}`}
      fill="fixed"
      className="overflow-hidden rounded-md"
    />
  );
}
