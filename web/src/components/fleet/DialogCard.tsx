// DialogCard: answers a terminal dialog from the browser (DESIGN.md section 8.2). It shows the
// screen verbatim (Jeff reads the real thing, not a paraphrase) and one button per numbered
// option; a press sends that digit bound to the hash of the screen it was rendered from. If the
// screen changed meanwhile the server answers 409 CONFLICT, nothing is sent, and the card shows
// the new screen with a note to check it again. Options that widen permissions or destroy work
// carry a warning badge: the server's own `warning` on the option, or a match in lib/dialog.ts
// (whichever fires); risky phrases elsewhere in the dialog are shown above the options. A form
// (several questions, or a multi-select list) gets "open terminal" only.
import { AlertTriangle, MessageSquareWarning, RefreshCw, SquareTerminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import { cn } from "../../lib/cn";
import { riskReason } from "../../lib/dialog";
import { useSendKeys } from "../../lib/queries";
import type { AgentScreen, DialogOption } from "../../lib/types";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { useToast } from "../ui/toast-context";

interface DialogCardProps {
  /** The agent's pane id: the API target for the key press. */
  target: string;
  agentName: string;
  screen: AgentScreen;
  onOpenTerminal: () => void;
}

export function DialogCard({ target, agentName, screen, onOpenTerminal }: DialogCardProps) {
  const sendKeys = useSendKeys(target);
  const toast = useToast();
  const [conflict, setConflict] = useState(false);
  const dialog = screen.dialog;

  const press = (option: DialogOption) => {
    setConflict(false);
    // mutateAsync, not mutate's own callbacks: the hook refetches the fleet before those run, and
    // an answered dialog leaves the fleet's needs-you list, which unmounts this card, and React
    // Query drops the callbacks of an unmounted caller. The confirmation was lost every time the
    // dialog cleared quickly (found by the M9 fixtures run). A promise settles regardless.
    sendKeys.mutateAsync({ keys: [option.key], screen_hash: screen.screen_hash }).then(
      () => toast.show({ tone: "success", title: `Sent “${option.key}” to ${agentName}`, description: option.label }),
      (error: unknown) => {
        if (error instanceof ApiError && error.status === 409) {
          setConflict(true);
          return;
        }
        toast.show({ tone: "danger", title: "Answer not sent", description: error instanceof Error ? error.message : String(error) });
      },
    );
  };

  return (
    <Card
      aria-label={`Dialog from ${agentName}`}
      tone="danger"
      bodyClassName="flex flex-col gap-3 p-3"
      title={
        <>
          <MessageSquareWarning className="h-4 w-4 text-danger" aria-hidden />
          <span className="truncate">{agentName} is asking</span>
        </>
      }
      actions={
        <Button compact variant="ghost" icon={SquareTerminal} onClick={onOpenTerminal}>
          Open terminal
        </Button>
      }
    >
      {conflict && (
        <Banner tone="warning" icon={RefreshCw} className="rounded">
          The screen changed before your answer arrived, so nothing was sent. This is the new screen: check it and answer
          again.
        </Banner>
      )}
      <ScreenText text={screen.text} />
      {dialog.present && dialog.warnings.length > 0 && (
        <Banner tone="warning" icon={AlertTriangle} className="rounded">
          This dialog mentions something risky: {dialog.warnings.join(", ")}.
        </Banner>
      )}
      {!dialog.present ? (
        <p className="text-xs text-muted">
          The agent is blocked, but no numbered options were recognised on its screen. Answer it in the terminal.
        </p>
      ) : dialog.form ? (
        <Banner tone="info" className="rounded">
          This is a form with several questions. Answer it in the terminal.
        </Banner>
      ) : (
        <ul aria-label="Options" className="flex flex-col gap-1.5">
          {dialog.options.map((option) => (
            <li key={option.key}>
              <OptionButton option={option} disabled={sendKeys.isPending} onPress={() => press(option)} />
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function OptionButton({ option, disabled, onPress }: { option: DialogOption; disabled: boolean; onPress: () => void }) {
  const risk = riskReason(option.label) ?? option.warning ?? null;
  return (
    <button
      type="button"
      onClick={onPress}
      disabled={disabled}
      className={cn(
        "flex min-h-11 w-full items-start gap-2.5 rounded border px-2.5 py-2 text-left text-sm transition-colors disabled:opacity-50 md:min-h-9 md:text-xs",
        risk ? "border-warning/60 bg-warning/5 hover:bg-warning/10" : "border-border-strong bg-surface hover:border-accent hover:bg-hover",
      )}
    >
      <kbd className="mt-px inline-flex h-5 w-5 shrink-0 items-center justify-center rounded border border-border-strong bg-surface-sunken font-mono text-2xs font-semibold text-heading">
        {option.key}
      </kbd>
      <span className="min-w-0 flex-1 break-words text-heading">{option.label}</span>
      {risk && (
        <Chip tone="warning" title={risk} className="mt-px">
          <AlertTriangle className="h-3 w-3" aria-hidden />
          <span>Risky</span>
          <span className="sr-only">: {risk}</span>
        </Chip>
      )}
    </button>
  );
}

/** The screen, verbatim, scrolled to its bottom (where dialogs are). */
export function ScreenText({ text, className }: { text: string; className?: string }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [text]);
  return (
    <pre
      ref={ref}
      aria-label="Screen text"
      // Focusable so a keyboard can scroll it (axe: scrollable-region-focusable, M9).
      tabIndex={0}
      className={cn(
        "max-h-72 overflow-auto rounded border border-border bg-surface-sunken p-2.5 font-mono text-2xs leading-tight text-heading",
        className,
      )}
    >
      {text}
    </pre>
  );
}
