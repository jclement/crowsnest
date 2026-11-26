// AgentConversation: the slot for an agent's conversation (Claude's transcript rendered as
// messages). M3 ships the placeholder; M4/M8 replace the body with the real view, keeping this
// component's name and props so the Fleet and Jobs views need no change.
import { MessagesSquare } from "lucide-react";
import type { Agent } from "../../lib/types";
import { EmptyState } from "../ui/EmptyState";

export interface AgentConversationProps {
  /** The agent whose conversation to show (its pane id is the API target). */
  agent: Agent;
}

export function AgentConversation({ agent }: AgentConversationProps) {
  return (
    <EmptyState icon={MessagesSquare} title="Conversation" className="h-auto py-8">
      {agent.has_transcript
        ? `${agent.name}'s conversation will appear here, read from its Claude transcript.`
        : `No transcript was found for ${agent.name}, so only its terminal can be shown.`}
    </EmptyState>
  );
}
