// ConversationView: an agent's conversation and the composer under it. The Chat view shows the
// orchestrator's as a page; the Fleet panel shows any agent's, embedded (AgentConversation). The
// list follows new messages only while the reader is at the bottom (useStickToBottom) and offers
// a "new messages" pill otherwise.
import { ArrowDown, MessagesSquare, RefreshCw } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { visibleMessages, type ChatState } from "../../lib/conversation";
import { useAgentMessages } from "../../lib/conversation-queries";
import { DESKTOP_QUERY, useMediaQuery } from "../../lib/media";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { EmptyState } from "../ui/EmptyState";
import { SkeletonRows } from "../ui/Skeleton";
import { Composer, type ComposerProps } from "./Composer";
import { MessageItem } from "./MessageItem";
import { useStickToBottom } from "./useStickToBottom";

export interface ConversationViewProps {
  /** The API target: a pane id, or "orchestrator". */
  target: string;
  agentName: string;
  state: ChatState;
  /** "page" fills the view (Chat); "embedded" is a bounded box (the Fleet panel). */
  variant: "page" | "embedded";
  showThinking?: boolean;
  onOpenTerminal?: () => void;
  /**
   * Show the pane's text when there is no transcript (Codex, or a Claude whose session herdr does
   * not know). False for a Claude agent whose transcript simply does not exist yet (Claude writes
   * it on the first prompt, so a fresh conversation has none): it gets the empty state instead.
   */
  paneFallback?: boolean;
  /** Replaces the plain Composer (Chat's voice composer wraps it); given its props, including the
   * onSent that scrolls the conversation to its newest message. */
  renderComposer?: (props: ComposerProps) => ReactNode;
  /** When set (voice is on), replies with a SAY line get a play button that speaks it through
   * this API target. */
  playTarget?: string;
}

export function ConversationView({
  target,
  agentName,
  state,
  variant,
  showThinking = false,
  onOpenTerminal,
  paneFallback = true,
  renderComposer,
  playTarget,
}: ConversationViewProps) {
  const query = useAgentMessages(target);
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const messages = useMemo(() => {
    const shown = visibleMessages(query.data?.messages ?? [], showThinking);
    return paneFallback ? shown : shown.filter((message) => message.kind !== "pane");
  }, [query.data, showThinking, paneFallback]);
  const revision = messages.reduce((highest, message) => Math.max(highest, message.seq), 0);
  const ownScroll = variant === "embedded" || desktop;
  const { anchorRef, unseen, scrollToBottom } = useStickToBottom(messages.length, revision, `${ownScroll}:${query.data?.sessionId}`);
  const page = variant === "page";

  return (
    <div className={cn("flex min-w-0 flex-col", page && "md:h-full md:min-h-0")}>
      <div
        data-scroll-container={ownScroll ? "" : undefined}
        className={cn(
          "relative min-w-0 px-3 py-3",
          page ? "flex-1 md:min-h-0 md:overflow-y-auto" : "max-h-[32rem] min-h-40 overflow-y-auto",
        )}
      >
        <div className={cn("mx-auto flex min-w-0 flex-col gap-3", page && "max-w-3xl")}>
          <ConversationBody query={query} messages={messages} agentName={agentName} playTarget={playTarget} />
          <div ref={anchorRef} aria-hidden className="h-px" />
        </div>
      </div>
      <div
        className={cn(
          "border-t border-border bg-surface px-3 pt-2",
          // Sticky and relative both anchor the new-messages pill below.
          page ? "sticky bottom-0 pb-2 md:relative" : "relative pb-2",
        )}
      >
        {/* The pill hangs just above the composer rather than at the bottom of the list: on a
            phone the list scrolls with the page and the sticky composer covered a pill placed
            there, so it could not be tapped (found by the M9 WebKit run). */}
        {unseen > 0 && (
          <div className="pointer-events-none absolute inset-x-0 bottom-full mb-2 flex justify-center">
            <Button compact variant="primary" icon={ArrowDown} onClick={scrollToBottom} className="pointer-events-auto rounded-full shadow-md">
              {unseen === 1 ? "1 new message" : `${unseen} new messages`}
            </Button>
          </div>
        )}
        <div className={cn("mx-auto flex min-w-0 flex-col gap-2", page && "max-w-3xl")}>
          {(renderComposer ?? ((props: ComposerProps) => <Composer {...props} />))({ target, agentName, state, onSent: scrollToBottom, onOpenTerminal })}
        </div>
      </div>
    </div>
  );
}

function ConversationBody({
  query,
  messages,
  agentName,
  playTarget,
}: {
  query: ReturnType<typeof useAgentMessages>;
  messages: ReturnType<typeof visibleMessages>;
  agentName: string;
  playTarget?: string;
}) {
  if (query.isPending) return <SkeletonRows rows={5} label="Loading the conversation" />;
  if (query.isError) {
    return (
      <Banner
        tone="danger"
        role="alert"
        action={
          <Button compact icon={RefreshCw} onClick={() => void query.refetch()}>
            Retry
          </Button>
        }
      >
        Could not load the conversation: {query.error.message}
      </Banner>
    );
  }
  if (messages.length === 0) {
    return (
      <EmptyState icon={MessagesSquare} title="No messages yet" className="h-auto py-10">
        Anything you send {agentName} appears here, with its replies.
      </EmptyState>
    );
  }
  return (
    <>
      {query.data?.earlier && <p className="text-center text-2xs text-muted">Older messages are in the transcript; showing the newest.</p>}
      <ol aria-label="Conversation" className="flex min-w-0 flex-col gap-3">
        {messages.map((message) => (
          <li key={message.id} className="min-w-0">
            <MessageItem message={message} playTarget={playTarget} />
          </li>
        ))}
      </ol>
    </>
  );
}
