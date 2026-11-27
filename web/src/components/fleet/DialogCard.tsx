// DialogCard: answers a terminal dialog from the browser (DESIGN.md section 8.2). It shows the
// screen verbatim (Jeff reads the real thing, not a paraphrase) and one button per numbered
// option; a press sends that digit bound to the hash of the screen it was rendered from. If the
// screen changed meanwhile the server answers 409 CONFLICT, nothing is sent, and the card shows
// the new screen with a note to check it again. Options that widen permissions or destroy work
// carry a warning badge: the server's own `warning` on the option, or a match in lib/dialog.ts
// (whichever fires); risky phrases elsewhere in the dialog are shown above the options. A form
// (several questions, or a multi-select list) gets "open terminal" only.
//
// The folder-trust question (DESIGN.md 8.2.1) gets its own card: the agent and the folder named,
// what trusting means, and two buttons, Trust and Don't trust, whatever shape the agent drew its
// menu in (Claude's arrow list, Codex's numbered one); the screen is one tap away. Every answer
// is checked: the server watches the screen and says whether the dialog went (`dialog_gone`), and
// a card whose answer did not take says so instead of a success.
import { AlertTriangle, FolderLock, MessageSquareWarning, RefreshCw, SquareTerminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import { cn } from "../../lib/cn";
import { riskReason } from "../../lib/dialog";
import { useSendKeys } from "../../lib/queries";
import type { AgentScreen, DialogOption, KeysResponse, TrustPrompt } from "../../lib/types";
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
  const [stillUp, setStillUp] = useState(false);
  const dialog = screen.dialog;

  const press = (option: DialogOption, success?: string) => {
    setConflict(false);
    setStillUp(false);
    // mutateAsync, not mutate's own callbacks: the hook refetches the fleet before those run, and
    // an answered dialog leaves the fleet's needs-you list, which unmounts this card, and React
    // Query drops the callbacks of an unmounted caller. The confirmation was lost every time the
    // dialog cleared quickly (found by the M9 fixtures run). A promise settles regardless.
    const keys = option.keys && option.keys.length > 0 ? option.keys : [option.key];
    sendKeys.mutateAsync({ keys, screen_hash: screen.screen_hash }).then(
      (result: KeysResponse) => {
        if (result.dialog_gone === false) {
          setStillUp(true);
          toast.show({ tone: "warning", title: `${agentName} is still asking`, description: "The answer was sent, but the same question is still on its screen. Check it here, or open the terminal." });
          return;
        }
        toast.show({ tone: "success", title: success ?? (option.key ? `Sent “${option.key}” to ${agentName}` : `Answered ${agentName}`), description: option.label });
      },
      (error: unknown) => {
        if (error instanceof ApiError && error.status === 409) {
          setConflict(true);
          return;
        }
        toast.show({ tone: "danger", title: "Answer not sent", description: error instanceof Error ? error.message : String(error) });
      },
    );
  };

  if (dialog.present && dialog.trust) {
    return (
      <TrustCard
        trust={dialog.trust}
        agentName={agentName}
        screen={screen}
        pending={sendKeys.isPending}
        conflict={conflict}
        stillUp={stillUp}
        onPress={press}
        onOpenTerminal={onOpenTerminal}
      />
    );
  }

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
      {conflict && <ConflictBanner />}
      {stillUp && <StillUpBanner />}
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
            <li key={option.key || option.label}>
              <OptionButton option={option} disabled={sendKeys.isPending} onPress={() => press(option)} />
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function ConflictBanner() {
  return (
    <Banner tone="warning" icon={RefreshCw} className="rounded">
      The screen changed before your answer arrived, so nothing was sent. This is the new screen: check it and answer again.
    </Banner>
  );
}

function StillUpBanner() {
  return (
    <Banner tone="warning" icon={AlertTriangle} className="rounded">
      Your answer was sent, but the same question is still on the agent&apos;s screen a few seconds later. Look at the screen
      below; if it has not moved, answer in the terminal.
    </Banner>
  );
}

const engineName: Record<TrustPrompt["engine"], string> = { claude: "Claude Code", codex: "Codex" };

interface TrustCardProps {
  trust: TrustPrompt;
  agentName: string;
  screen: AgentScreen;
  pending: boolean;
  conflict: boolean;
  stillUp: boolean;
  onPress: (option: DialogOption, success?: string) => void;
  onOpenTerminal: () => void;
}

/** The folder-trust question as a decision: who asks, about which folder, what yes means. */
function TrustCard({ trust, agentName, screen, pending, conflict, stillUp, onPress, onOpenTerminal }: TrustCardProps) {
  const engine = engineName[trust.engine];
  const folder = trust.folder || "its working folder";
  const refusal = trust.engine === "codex" ? "Codex goes back to its own launcher and the job waits." : "Claude exits and the job waits.";
  return (
    <Card
      aria-label={`Folder trust for ${agentName}`}
      tone="danger"
      bodyClassName="flex flex-col gap-3 p-3"
      title={
        <>
          <FolderLock className="h-4 w-4 text-danger" aria-hidden />
          <span className="truncate">{agentName} asks to trust a folder</span>
        </>
      }
      actions={
        <Button compact variant="ghost" icon={SquareTerminal} onClick={onOpenTerminal}>
          Open terminal
        </Button>
      }
    >
      {conflict && <ConflictBanner />}
      {stillUp && <StillUpBanner />}
      <div className="flex flex-col gap-1">
        <span className="text-2xs font-medium uppercase tracking-wide text-muted">{engine} will work in</span>
        <code className="break-all rounded bg-surface-sunken px-2 py-1 font-mono text-xs text-heading">{folder}</code>
      </div>
      <p className="text-xs text-body">
        Trusting lets {engine} read, edit and run files in this folder, and lets the folder&apos;s own agent settings and
        hooks run. Crowsnest&apos;s guard and approvals still apply. Trust it if you know what is in it: your own
        repository, or one your team works in. {engine} remembers the answer for this folder; trusting a project&apos;s own folder
        (its page offers it) spares every job worktree made from it this question.
      </p>
      <div className="flex flex-col gap-1.5 sm:flex-row">
        <Button variant="primary" disabled={pending} onClick={() => onPress(trust.allow, `${agentName} may work in ${folder}`)} className="sm:flex-1">
          Trust this folder
        </Button>
        <Button variant="ghost" disabled={pending} onClick={() => onPress(trust.deny, `${agentName} was told not to trust ${folder}`)} className="sm:flex-1">
          Don&apos;t trust
        </Button>
      </div>
      <p className="text-2xs text-muted">Don&apos;t trust: {refusal}</p>
      <details className="text-2xs text-muted">
        <summary className="cursor-pointer select-none">The screen, as {agentName} shows it</summary>
        <ScreenText text={screen.text} className="mt-1.5" />
      </details>
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
      {option.key && (
        <kbd className="mt-px inline-flex h-5 w-5 shrink-0 items-center justify-center rounded border border-border-strong bg-surface-sunken font-mono text-2xs font-semibold text-heading">
          {option.key}
        </kbd>
      )}
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
