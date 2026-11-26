// AgentConversation: an agent's conversation in the Fleet panel (and later the Jobs view):
// Claude's transcript rendered as messages, with a composer to message the agent. Agents with no
// transcript (Codex, or a Claude whose session herdr has not detected) show their pane text
// instead; a Claude with a known session but no transcript yet (no prompt so far) is empty. The name and props are the slot M3 left; the body is the shared ConversationView.
import { chatStateOf } from "../../lib/conversation";
import type { Agent } from "../../lib/types";
import { ConversationView } from "../chat/ConversationView";

export interface AgentConversationProps {
  /** The agent whose conversation to show (its pane id is the API target). */
  agent: Agent;
}

export function AgentConversation({ agent }: AgentConversationProps) {
  return (
    <ConversationView
      target={agent.pane_id}
      agentName={agent.name || agent.pane_id}
      state={chatStateOf(agent.status)}
      variant="embedded"
      paneFallback={agent.kind !== "claude" || !agent.session_id}
    />
  );
}
