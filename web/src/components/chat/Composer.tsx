// Composer: the box a message to an agent is typed in (DESIGN.md 7.3 and 12). Enter sends on a
// desktop (Shift+Enter for a new line); on a phone Enter is a new line and the button sends.
// While the agent works, a send is queued by Claude and the composer says so. When the agent is
// at a dialog the box is replaced by a note pointing at the dialog, because typed text would land
// in the dialog. A draft typed at the terminal stops the send until the human says to append.
import { Send, SquareTerminal } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { ApiError } from "../../lib/api";
import type { ChatState, DraftDetail, PromptResult } from "../../lib/conversation";
import { useSendPrompt } from "../../lib/conversation-queries";
import { DESKTOP_QUERY, useMediaQuery } from "../../lib/media";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

/** The textarea grows with its text up to this height, then scrolls. */
const MAX_INPUT_PX = 200;

interface ComposerProps {
  /** The API target: a pane id, or "orchestrator". */
  target: string;
  agentName: string;
  state: ChatState;
  /** Called after a successful send (the conversation scrolls to the bottom). */
  onSent?: (result: PromptResult) => void;
  /** Shown as "Open terminal" when the agent needs the terminal; omitted inside the Fleet panel. */
  onOpenTerminal?: () => void;
}

export function Composer({ target, agentName, state, onSent, onOpenTerminal }: ComposerProps) {
  const [text, setText] = useState("");
  const [draft, setDraft] = useState<DraftDetail | null>(null);
  const [queued, setQueued] = useState(false);
  const send = useSendPrompt(target);
  const toast = useToast();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const hintId = useId();

  // "Queued" lasts until the agent is ready again, which is when Claude reads it. Adjusted
  // during render (React's pattern for state that follows a prop) rather than in an effect.
  const [seenState, setSeenState] = useState(state);
  if (seenState !== state) {
    setSeenState(state);
    if (state === "idle") setQueued(false);
  }

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, MAX_INPUT_PX)}px`;
  }, [text]);

  if (state === "blocked" || state === "needs_terminal" || state === "off" || state === "failed") {
    return <ComposerNotice state={state} agentName={agentName} onOpenTerminal={onOpenTerminal} />;
  }

  const submit = (force = false) => {
    if (!text.trim() || send.isPending) return;
    send.mutate(
      { text, force_over_draft: force },
      {
        onSuccess: (result) => {
          setText("");
          setDraft(null);
          setQueued(result.queued);
          if (!result.started && result.note) toast.show({ tone: "warning", title: "Sent, but not picked up yet", description: result.note });
          onSent?.(result);
        },
        onError: (error) => {
          if (error instanceof ApiError && error.code === "DRAFT") {
            setDraft((error.detail as DraftDetail | undefined) ?? { input_found: true, present: true, text: "" });
            return;
          }
          toast.show({ tone: "danger", title: "Not sent", description: error.message });
        },
      },
    );
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    submit();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || !desktop) return;
    event.preventDefault();
    submit();
  };

  const starting = state === "starting";
  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-1.5" aria-label={`Message ${agentName}`}>
      {draft && (
        <Banner
          tone="warning"
          role="alert"
          action={
            <span className="flex gap-1">
              <Button compact variant="warning" onClick={() => submit(true)} disabled={send.isPending}>
                Send anyway
              </Button>
              <Button compact variant="ghost" onClick={() => setDraft(null)}>
                Cancel
              </Button>
            </span>
          }
        >
          <span className="min-w-0">
            Someone typed a draft at the terminal{draft.text ? <>: <q className="font-mono">{draft.text}</q></> : null}. Sending appends to it.
          </span>
        </Banner>
      )}
      <div className="flex items-end gap-2">
        <textarea
          ref={inputRef}
          value={text}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={onKeyDown}
          rows={1}
          disabled={starting}
          aria-label={`Message to ${agentName}`}
          aria-describedby={hintId}
          placeholder={starting ? "Starting…" : `Message ${agentName}…`}
          className="min-h-11 min-w-0 flex-1 resize-none rounded border border-border-strong bg-surface px-3 py-2.5 text-sm text-heading placeholder:text-muted focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30 disabled:opacity-60 md:min-h-9 md:py-2"
        />
        <Button type="submit" variant="primary" icon={Send} disabled={!text.trim() || send.isPending || starting} aria-label="Send" className="min-w-11">
          <span className="hidden md:inline">Send</span>
        </Button>
      </div>
      <p id={hintId} className="min-h-4 text-2xs text-muted">
        {queued ? (
          <span className="font-medium text-accent">Queued: Claude reads it when the current turn ends.</span>
        ) : state === "working" ? (
          "Working. What you send now is queued until this turn ends."
        ) : desktop ? (
          "Enter sends, Shift+Enter adds a line."
        ) : null}
      </p>
    </form>
  );
}

const notices: Record<"blocked" | "needs_terminal" | "off" | "failed", (name: string) => string> = {
  blocked: (name) => `${name} is waiting at a dialog, so nothing can be typed. Answer the dialog first.`,
  needs_terminal: (name) => `${name} stopped at a start-up prompt (folder trust or login). Answer it in the terminal.`,
  off: () => "The orchestrator is off in this server (--no-orchestrator).",
  failed: (name) => `${name} is not running. Restart it from the menu.`,
};

function ComposerNotice({
  state,
  agentName,
  onOpenTerminal,
}: {
  state: keyof typeof notices;
  agentName: string;
  onOpenTerminal?: () => void;
}) {
  const needsTerminal = state === "blocked" || state === "needs_terminal";
  return (
    <Banner
      tone={state === "off" ? "info" : "warning"}
      role="status"
      action={
        needsTerminal && onOpenTerminal ? (
          <Button compact icon={SquareTerminal} onClick={onOpenTerminal}>
            {state === "blocked" ? "Answer" : "Open terminal"}
          </Button>
        ) : undefined
      }
    >
      {notices[state](agentName)}
    </Banner>
  );
}
