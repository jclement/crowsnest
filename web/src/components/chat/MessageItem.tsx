// MessageItem: one message of a conversation. Assistant replies render as Markdown (the one safe
// renderer, DESIGN.md 4.8); user messages are plain text in a bubble, with a small label when
// crowsnest typed them (voice, answer) or they are a slash command; a poke is a compact system
// row (PokeRow), not a bubble; tool calls are cards;
// thinking is a collapsed block (and hidden altogether unless asked for); the pane-text fallback
// is preformatted. A reply's leading `SAY:` line (every orchestrator reply has one) becomes a
// small caption above the detail, speak mode or not; with voice on it has a play button.
import { Brain, ChevronRight, Loader2, Play, Volume2 } from "lucide-react";
import { useState } from "react";
import { endpoints } from "../../lib/api";
import { cn } from "../../lib/cn";
import type { Message, MessageTag } from "../../lib/conversation";
import { player } from "../../lib/voice/player";
import { splitSay } from "../../lib/voice/say";
import { Markdown } from "../Markdown";
import { Chip, type ChipTone } from "../ui/Chip";
import { PokeRow } from "./PokeRow";
import { ToolCard } from "./ToolCard";

const tagLabels: Record<MessageTag, { label: string; tone: ChipTone }> = {
  poke: { label: "poke", tone: "neutral" },
  voice: { label: "voice", tone: "accent" },
  answer: { label: "answer", tone: "success" },
  command: { label: "command", tone: "neutral" },
};

/** playTarget, when set (voice on), is the API target a reply's SAY line is played through. */
export function MessageItem({ message, playTarget }: { message: Message; playTarget?: string }) {
  switch (message.kind) {
    case "user":
      return message.tag === "poke" ? <PokeRow text={message.text ?? ""} /> : <UserMessage message={message} />;
    case "assistant":
      return <AssistantMessage id={message.id} text={message.text ?? ""} playTarget={playTarget} />;
    case "tool":
      return message.tool ? <ToolCard tool={message.tool} /> : null;
    case "thinking":
      return <ThinkingBlock text={message.text ?? ""} />;
    case "pane":
      return (
        <div className="min-w-0">
          <p className="mb-1 text-2xs text-muted">No Claude transcript was found for this agent, so this is its terminal text.</p>
          <pre
            data-allow-overflow
            className="overflow-x-auto rounded border border-border bg-surface-sunken p-2 font-mono text-2xs leading-relaxed text-body"
          >
            {message.text}
          </pre>
        </div>
      );
  }
}

/** A reply: Markdown, with a leading `SAY:` line shown as a small spoken caption instead. */
function AssistantMessage({ id, text, playTarget }: { id: string; text: string; playTarget?: string }) {
  const { spoken, body } = splitSay(text);
  return (
    <article aria-label="Reply" className="min-w-0 text-sm">
      {spoken && (
        <p className="mb-1.5 flex items-start gap-1.5 text-xs italic text-muted" data-testid="spoken-caption">
          {playTarget ? (
            <PlaySayButton target={playTarget} messageId={id} />
          ) : (
            <Volume2 aria-label="Spoken" className="mt-0.5 h-3.5 w-3.5 shrink-0 not-italic" />
          )}
          <span className="min-w-0 break-words">{spoken}</span>
        </p>
      )}
      {body.trim() && <Markdown>{body}</Markdown>}
    </article>
  );
}

/**
 * Plays a reply's SAY line now, speak mode or not. The server reads the line from the transcript
 * by message id (the browser never sends the words), and the audio is synthesised only now, or
 * comes from the cache if speak mode already played it.
 */
function PlaySayButton({ target, messageId }: { target: string; messageId: string }) {
  const [busy, setBusy] = useState(false);
  const play = () => {
    player.unlock(); // inside the click, before any await
    setBusy(true);
    endpoints
      .replySay(target, messageId)
      .then(
        (result) => player.playNow({ textId: result.text_id, text: result.text }),
        () => {},
      )
      .finally(() => setBusy(false));
  };
  return (
    <button
      type="button"
      onClick={play}
      disabled={busy}
      aria-label="Play this reply aloud"
      title="Play aloud"
      className="-my-3 -ml-3 inline-flex h-11 w-11 shrink-0 items-center justify-center rounded not-italic text-muted hover:bg-hover hover:text-accent md:-my-1 md:-ml-1 md:h-6 md:w-6"
    >
      {busy ? <Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" /> : <Play aria-hidden className="h-3.5 w-3.5" />}
    </button>
  );
}

function UserMessage({ message }: { message: Message }) {
  const tag = message.tag ? tagLabels[message.tag] : undefined;
  return (
    <div className="flex min-w-0 flex-col items-end gap-1">
      {tag && <Chip tone={tag.tone}>{tag.label}</Chip>}
      <div
        className={cn(
          "max-w-[92%] rounded-lg px-3 py-2 text-sm whitespace-pre-wrap break-words md:max-w-[80%]",
          "bg-selected text-heading",
          message.tag === "command" && "font-mono text-xs",
        )}
      >
        {message.tag === "voice" ? withoutHeader(message.text ?? "") : message.text}
      </div>
    </div>
  );
}

/** A voice prompt's text without crowsnest's header line; the "voice" chip already says so. */
function withoutHeader(text: string): string {
  const newline = text.indexOf("\n");
  return text.startsWith("[crowsnest voice") && newline !== -1 ? text.slice(newline + 1) : text;
}

function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="min-w-0 text-xs">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="inline-flex min-h-11 items-center gap-1.5 rounded px-1 text-muted hover:text-heading md:min-h-7"
      >
        <ChevronRight aria-hidden className={cn("h-3.5 w-3.5 transition-transform", open && "rotate-90")} />
        <Brain aria-hidden className="h-3.5 w-3.5" />
        Thinking
      </button>
      {open && <p className="ml-5 border-l-2 border-border pl-2 italic whitespace-pre-wrap text-muted">{text}</p>}
    </div>
  );
}
