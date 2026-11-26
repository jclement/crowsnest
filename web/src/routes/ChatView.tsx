// The Chat view (DESIGN.md section 12): the orchestrator's conversation, a sticky composer, its
// status chip (driven by `orchestrator` events), and a menu with thinking, Restart and New
// conversation. When the orchestrator needs the terminal (a start-up prompt) or is not running,
// a banner says why and links to its pane in Fleet. With voice on, the header has the talk-mode
// toggle and the composer area the push-to-talk panel (components/voice/TalkPanel.tsx).
import { useNavigate } from "@tanstack/react-router";
import { MessageSquare, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { ChatMenu } from "../components/chat/ChatMenu";
import { ChatStatusChip } from "../components/chat/ChatStatusChip";
import { ConversationView } from "../components/chat/ConversationView";
import { TalkPanel, TalkToggle } from "../components/voice/TalkPanel";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { EmptyState } from "../components/ui/EmptyState";
import { SkeletonRows } from "../components/ui/Skeleton";
import { chatStateOf } from "../lib/conversation";
import { useOrchestrator } from "../lib/conversation-queries";
import { useMe } from "../lib/queries";

/** The orchestrator's API target: the server resolves the name to its pane. */
const ORCHESTRATOR = "orchestrator";

export function ChatView() {
  const status = useOrchestrator();
  const navigate = useNavigate();
  const [showThinking, setShowThinking] = useState(false);
  const voice = useMe().data?.features.voice ?? false;

  if (status.isPending) return <SkeletonRows rows={6} label="Loading the orchestrator" />;
  if (status.isError) {
    return (
      <EmptyState icon={MessageSquare} title="No orchestrator">
        {status.error.message}
      </EmptyState>
    );
  }
  const orchestrator = status.data;
  const state = chatStateOf(orchestrator.state);
  const paneId = orchestrator.pane_id;
  const openTerminal = paneId ? () => void navigate({ to: "/fleet", search: { agent: paneId } }) : undefined;

  return (
    <section aria-label="Chat with the orchestrator" className="flex min-w-0 flex-col md:h-full md:min-h-0">
      <header className="sticky top-0 z-10 flex min-h-11 shrink-0 items-center gap-2 border-b border-border bg-surface-raised px-3 md:static">
        <h1 className="text-sm font-semibold text-heading">Orchestrator</h1>
        <ChatStatusChip state={state} />
        {orchestrator.session_id && (
          <span className="hidden min-w-0 truncate font-mono text-2xs text-muted sm:inline" title="Claude session id">
            {orchestrator.session_id.slice(0, 8)}
          </span>
        )}
        <div className="ml-auto flex items-center gap-1.5">
          {voice && <TalkToggle />}
          <ChatMenu
            showThinking={showThinking}
            onToggleThinking={() => setShowThinking((value) => !value)}
            canRelaunch={orchestrator.state !== "disabled"}
          />
        </div>
      </header>
      {orchestrator.detail && (state === "needs_terminal" || state === "failed") && (
        <Banner
          tone={state === "failed" ? "danger" : "warning"}
          role="alert"
          action={
            openTerminal && (
              <Button compact icon={SquareTerminal} onClick={openTerminal}>
                Open terminal
              </Button>
            )
          }
        >
          {orchestrator.detail}
        </Banner>
      )}
      {orchestrator.state === "disabled" ? (
        <EmptyState icon={MessageSquare} title="The orchestrator is off">
          This server was started with --no-orchestrator. Agents in the fleet can still be messaged from Fleet.
        </EmptyState>
      ) : paneId ? (
        <ConversationView
          target={ORCHESTRATOR}
          agentName="the orchestrator"
          state={state}
          variant="page"
          showThinking={showThinking}
          onOpenTerminal={openTerminal}
          paneFallback={false}
          composerExtra={
            voice
              ? (scrollToBottom) => (
                  <TalkPanel target={ORCHESTRATOR} onSent={scrollToBottom} disabled={state === "blocked" || state === "needs_terminal" || state === "failed"} />
                )
              : undefined
          }
        />
      ) : (
        <EmptyState icon={MessageSquare} title="Starting the orchestrator">
          {orchestrator.detail ?? "Crowsnest is starting Claude in your herdr session."}
        </EmptyState>
      )}
    </section>
  );
}
