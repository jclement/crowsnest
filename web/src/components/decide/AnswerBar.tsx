// AnswerBar: the decision packet's sticky bar at the bottom of the screen, in thumb reach (R4
// Fable 3.3, Codex P1). It holds the note (kept in localStorage per item, so rotating the phone,
// switching apps or iOS reloading a discarded page never loses it), the choices, and "Open the
// full job". A question's options answer in one press (multi-select: checkboxes and Submit); an
// approval is Allow once or Deny (a dangerous allow asks again, as the card does); a dialog's
// numbered options press their digit, bound to the screen they were drawn from. A 409 (someone
// answered first, the screen changed) refetches, so the packet turns into its stale notice
// instead of an error, and the note stays.
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Check, CheckCheck, ExternalLink, FolderLock, Send, ShieldCheck, Star, X } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { ApiError } from "../../lib/api";
import type { Approval } from "../../lib/approvals";
import { cn } from "../../lib/cn";
import { grants, isGate, loadNote, saveNote } from "../../lib/decision";
import { buildAnswer, canSubmit, type Question } from "../../lib/questions";
import { useAnswerQuestion, useApproval, useDecideApproval, useQuestion, useSendKeys } from "../../lib/queries";
import type { AgentScreen, DialogOption } from "../../lib/types";
import { useToast } from "../ui/toast-context";

export type BarTarget =
  | { kind: "question"; question: Question }
  | { kind: "approval"; approval: Approval }
  | { kind: "dialog"; paneId: string; screen: AgentScreen; agentName: string };

interface AnswerBarProps {
  itemId: string;
  jobId?: string;
  target: BarTarget;
  /** After a successful answer: the packet moves on to the next decision. */
  onAnswered: () => void;
}

/** More options than this scroll inside the bar instead of pushing the page up. */
const MAX_INLINE_OPTIONS = 3;

export function AnswerBar({ itemId, jobId, target, onAnswered }: AnswerBarProps) {
  const noteId = useId();
  const [note, setNoteState] = useState(() => loadNote(itemId));
  const [error, setError] = useState<string | null>(null);
  const setNote = (text: string) => {
    setNoteState(text);
    saveNote(itemId, text);
  };
  // Another tab or a reload of this one may have typed into the same item's note.
  useEffect(() => {
    const onStorage = () => setNoteState(loadNote(itemId));
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [itemId]);
  const done = () => {
    saveNote(itemId, "");
    onAnswered();
  };
  const manual = target.kind === "question" && target.question.kind === "manual";
  const showNote = target.kind !== "dialog";

  return (
    <div
      role="region"
      aria-label="Answer"
      data-testid="answer-bar"
      className="sticky bottom-0 z-20 border-t border-border bg-surface-raised px-3 pb-2 pt-2"
    >
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-2">
        {error && (
          <p role="alert" className="text-xs font-medium text-danger">
            {error}
          </p>
        )}
        {showNote && (
          <div className="flex flex-col gap-0.5">
            <label htmlFor={noteId} className="text-2xs font-medium text-muted">
              {manual ? "Your answer" : target.kind === "approval" ? "Note (sent with a deny)" : "Note (optional; it outranks the choice)"}
            </label>
            <textarea
              id={noteId}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              rows={manual ? 3 : 1}
              maxLength={target.kind === "approval" ? 2000 : 8000}
              placeholder={manual ? "Write the answer the agent asked for" : "Add context or a condition"}
              className="max-h-28 min-h-11 w-full resize-y rounded border border-border-strong bg-surface px-2.5 py-2 text-base text-heading placeholder:text-muted focus:border-accent focus:outline-none md:min-h-8 md:text-xs"
            />
          </div>
        )}
        {target.kind === "question" && <QuestionChoices question={target.question} note={note} onError={setError} onDone={done} />}
        {target.kind === "approval" && <ApprovalChoices approval={target.approval} note={note} onError={setError} onDone={done} />}
        {target.kind === "dialog" && <DialogChoices paneId={target.paneId} screen={target.screen} agentName={target.agentName} onError={setError} onDone={done} />}
        {jobId && (
          <Link
            to="/jobs"
            search={{ job: jobId }}
            className="inline-flex min-h-11 items-center gap-1 self-start rounded px-1 text-xs text-accent hover:underline md:min-h-7"
          >
            <ExternalLink className="h-3.5 w-3.5" aria-hidden />
            Open the full job ({jobId})
          </Link>
        )}
      </div>
    </div>
  );
}

interface ChoiceProps {
  note: string;
  onError: (message: string | null) => void;
  onDone: () => void;
}

// ---- Questions --------------------------------------------------------------------------------

function QuestionChoices({ question, note, onError, onDone }: ChoiceProps & { question: Question }) {
  const answer = useAnswerQuestion(question.id);
  const refetch = useQuestion(question.id).refetch;
  const toast = useToast();
  const [picked, setPicked] = useState<string[]>([]);
  const submit = (keys: string[]) => {
    if (!canSubmit(question, keys, note)) return;
    onError(null);
    answer.mutateAsync(buildAnswer(question, keys, note)).then(
      () => {
        toast.show({ tone: "success", title: `Answered ${question.id}`, description: question.title });
        onDone();
      },
      (failure: unknown) => {
        if (failure instanceof ApiError && failure.status === 409) {
          void refetch();
          return;
        }
        onError(failure instanceof Error ? failure.message : String(failure));
      },
    );
  };
  const busy = answer.isPending;
  const gate = isGate(question);
  const [confirming, setConfirming] = useState<string | null>(null);
  const pending = question.options.find((option) => option.key === confirming);
  if (pending) {
    return (
      <div className="flex flex-col gap-1.5" role="group" aria-label="Confirm the choice">
        <p className="flex items-start gap-1.5 text-xs font-medium text-warning">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
          This widens what agents may do. Read the evidence above, then confirm “{pending.label}”.
        </p>
        <div className="grid grid-cols-2 gap-1.5">
          <BarButton primary onPress={() => submit([pending.key])} disabled={busy} icon={<ShieldCheck className="h-4 w-4" aria-hidden />}>
            Yes, {pending.label}
          </BarButton>
          <BarButton onPress={() => setConfirming(null)} disabled={busy}>
            Back
          </BarButton>
        </div>
      </div>
    );
  }
  if (question.kind === "confirm" || (question.kind === "choice" && !question.multi)) {
    return (
      <OptionList count={question.options.length}>
        {question.options.map((option) => (
          <BarButton
            key={option.key}
            onPress={() => (gate && grants(option.key) ? setConfirming(option.key) : submit([option.key]))}
            disabled={busy}
            recommended={option.key === question.recommend}
            icon={<Check className="h-4 w-4" aria-hidden />}
          >
            {option.label}
          </BarButton>
        ))}
      </OptionList>
    );
  }
  if (question.kind === "choice" && question.multi) {
    return (
      <div className="flex flex-col gap-1.5">
        <OptionList count={question.options.length}>
          {question.options.map((option) => (
            <label key={option.key} className="flex min-h-11 cursor-pointer items-center gap-2 rounded-md border border-border-strong bg-surface px-3 text-sm text-heading md:min-h-8">
              <input
                type="checkbox"
                checked={picked.includes(option.key)}
                onChange={() => setPicked((current) => (current.includes(option.key) ? current.filter((key) => key !== option.key) : [...current, option.key]))}
                className="h-4 w-4 accent-[var(--color-accent)]"
              />
              <span className="min-w-0 flex-1 truncate">{option.label}</span>
              {option.key === question.recommend && <Star className="h-3.5 w-3.5 text-accent" aria-label="Recommended" />}
            </label>
          ))}
        </OptionList>
        <BarButton primary onPress={() => submit(picked)} disabled={busy || picked.length === 0} icon={<Send className="h-4 w-4" aria-hidden />}>
          Submit {picked.length > 0 ? `(${picked.length})` : ""}
        </BarButton>
      </div>
    );
  }
  const label = question.kind === "ack" ? "Acknowledge" : question.kind === "manual" ? "Send answer" : "Dismiss";
  const icon = question.kind === "ack" ? <CheckCheck className="h-4 w-4" aria-hidden /> : question.kind === "manual" ? <Send className="h-4 w-4" aria-hidden /> : <X className="h-4 w-4" aria-hidden />;
  return (
    <BarButton primary onPress={() => submit([])} disabled={busy || (question.kind === "manual" && !note.trim())} icon={icon}>
      {label}
    </BarButton>
  );
}

// ---- Approvals --------------------------------------------------------------------------------

function ApprovalChoices({ approval, note, onError, onDone }: ChoiceProps & { approval: Approval }) {
  const decide = useDecideApproval(approval.id);
  const refetch = useApproval(approval.id).refetch;
  const toast = useToast();
  const [confirming, setConfirming] = useState(false);
  const dangerous = approval.risk === "dangerous";
  const send = (allow: boolean, confirm = false) => {
    onError(null);
    decide.mutateAsync({ decision: allow ? "allow" : "deny", note: note.trim() || undefined, confirm: confirm || undefined }).then(
      (decided) => {
        toast.show({ tone: allow ? "success" : "info", title: allow ? "Allowed" : "Denied", description: decided.summary });
        onDone();
      },
      (failure: unknown) => {
        if (failure instanceof ApiError && failure.code === "CONFIRM_REQUIRED") {
          setConfirming(true);
          return;
        }
        if (failure instanceof ApiError && failure.status === 409) {
          void refetch();
          return;
        }
        onError(failure instanceof Error ? failure.message : String(failure));
      },
    );
  };
  if (confirming) {
    return (
      <div className="flex flex-col gap-1.5" role="group" aria-label="Confirm a dangerous allow">
        <p className="flex items-start gap-1.5 text-xs font-medium text-danger">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
          This is dangerous ({approval.risk_reasons.join("; ") || "see the reasons"}). Allow it anyway?
        </p>
        <div className="grid grid-cols-2 gap-1.5">
          <BarButton danger onPress={() => send(true, true)} disabled={decide.isPending} icon={<ShieldCheck className="h-4 w-4" aria-hidden />}>
            Yes, allow it
          </BarButton>
          <BarButton onPress={() => setConfirming(false)} disabled={decide.isPending}>
            Cancel
          </BarButton>
        </div>
      </div>
    );
  }
  return (
    <div className="grid grid-cols-2 gap-1.5">
      <BarButton primary={!dangerous} onPress={() => (dangerous ? setConfirming(true) : send(true))} disabled={decide.isPending} icon={<Check className="h-4 w-4" aria-hidden />}>
        Allow once
      </BarButton>
      <BarButton onPress={() => send(false)} disabled={decide.isPending} icon={<X className="h-4 w-4" aria-hidden />}>
        Deny
      </BarButton>
    </div>
  );
}

// ---- Dialogs ----------------------------------------------------------------------------------

function DialogChoices({ paneId, screen, agentName, onError, onDone }: Omit<ChoiceProps, "note"> & { paneId: string; screen: AgentScreen; agentName: string }) {
  const sendKeys = useSendKeys(paneId);
  const toast = useToast();
  const options = screen.dialog.options;
  if (screen.dialog.form || options.length === 0) {
    return (
      <Link
        to="/agents"
        search={{ agent: paneId, view: "terminal" }}
        className="inline-flex min-h-11 items-center justify-center rounded-md border border-border-strong bg-surface px-3 text-sm font-medium text-heading md:min-h-8"
      >
        Open the terminal (this dialog needs it)
      </Link>
    );
  }
  const press = (option: DialogOption, success?: string) => {
    onError(null);
    const keys = option.keys && option.keys.length > 0 ? option.keys : [option.key];
    sendKeys.mutateAsync({ keys, screen_hash: screen.screen_hash }).then(
      (result) => {
        if (result.dialog_gone === false) {
          // The server watched the screen: the same question is still up (DESIGN.md 8.2.1).
          onError("Your answer was sent, but the same question is still on the agent's screen. Look at the screen above; if it has not moved, open the terminal.");
          return;
        }
        toast.show({ tone: "success", title: success ?? `Answered ${agentName || "the agent"}`, description: option.label });
        onDone();
      },
      (failure: unknown) => {
        // A 409: the screen changed. The refetch the hook does redraws the packet with the new
        // screen (or its stale notice), so nothing else is needed.
        if (failure instanceof ApiError && failure.status === 409) {
          onError("The screen changed before the press arrived; nothing was sent. Check it again.");
          return;
        }
        onError(failure instanceof Error ? failure.message : String(failure));
      },
    );
  };
  const trust = screen.dialog.trust;
  if (trust) {
    const folder = trust.folder || "its folder";
    return (
      <OptionList count={2}>
        <BarButton primary onPress={() => press(trust.allow, `${agentName || "The agent"} may work in ${folder}`)} disabled={sendKeys.isPending} icon={<FolderLock className="h-4 w-4" aria-hidden />}>
          Trust this folder
        </BarButton>
        <BarButton onPress={() => press(trust.deny, `${agentName || "The agent"} was told not to trust ${folder}`)} disabled={sendKeys.isPending} icon={<X className="h-4 w-4" aria-hidden />}>
          Don&apos;t trust
        </BarButton>
      </OptionList>
    );
  }
  return (
    <OptionList count={options.length}>
      {options.map((option, index) => (
        <BarButton
          key={`${option.key}-${index}`}
          onPress={() => press(option)}
          disabled={sendKeys.isPending}
          danger={!!option.warning}
          icon={<span className="font-mono text-xs">{option.key || "›"}</span>}
        >
          {option.label}
          {option.warning && <span className="block text-2xs font-normal text-danger">{option.warning}</span>}
        </BarButton>
      ))}
    </OptionList>
  );
}

// ---- Pieces -------------------------------------------------------------------------------------

function OptionList({ count, children }: { count: number; children: React.ReactNode }) {
  return (
    <div className={cn("flex flex-col gap-1.5", count > MAX_INLINE_OPTIONS && "max-h-[38vh] overflow-y-auto overscroll-contain pr-0.5")} role="group" aria-label="Choices">
      {children}
    </div>
  );
}

interface BarButtonProps {
  onPress: () => void;
  disabled?: boolean;
  primary?: boolean;
  danger?: boolean;
  recommended?: boolean;
  icon?: React.ReactNode;
  children: React.ReactNode;
}

function BarButton({ onPress, disabled, primary, danger, recommended, icon, children }: BarButtonProps) {
  return (
    <button
      type="button"
      onClick={onPress}
      disabled={disabled}
      className={cn(
        "flex min-h-12 w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm font-medium transition-colors disabled:opacity-50 md:min-h-9",
        primary
          ? "border-accent bg-accent text-accent-fg hover:bg-accent/90"
          : danger
            ? "border-danger/60 bg-danger/5 text-danger hover:bg-danger/10"
            : recommended
              ? "border-accent/60 bg-accent/5 text-heading hover:bg-accent/10"
              : "border-border-strong bg-surface text-heading hover:bg-hover",
      )}
    >
      {icon && <span className="shrink-0">{icon}</span>}
      <span className="min-w-0 flex-1 break-words">{children}</span>
      {recommended && (
        <span className="inline-flex shrink-0 items-center gap-1 rounded bg-accent/10 px-1.5 py-0.5 text-2xs font-medium text-accent">
          <Star className="h-3 w-3" aria-hidden />
          Recommended
        </span>
      )}
    </button>
  );
}
