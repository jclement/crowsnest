// AnswerBar: the decision packet's sticky bar at the bottom of the screen, in thumb reach (R4
// Fable 3.3, Codex P1). It holds the choices and, folded behind "Add a note", the note (kept in
// localStorage per item, so rotating the phone, switching apps or iOS reloading a discarded page
// never loses it). A question's options answer in one press (multi-select: checkboxes and Submit);
// an approval is Allow once or Deny (a dangerous allow asks again, as the card does); a dialog's
// numbered options press their digit, bound to the screen they were drawn from. A 409 (someone
// answered first, the screen changed) refetches, so the packet turns into its stale notice
// instead of an error, and the note stays.
//
// R10 (Opus B1, B6; Fable B3, F6): the pressed choice says "Sending…" at once; a server that has
// not answered in five seconds is said to be slow (the answer still goes through when it does);
// a failure says so in the bar with Retry, never only in a toast. After an auto-advance the new
// item's choices are inert for ADVANCE_GUARD_MS, so a double tap cannot answer what was never read.
// The bar is short: the note is a link until wanted, two short choices sit side by side, and the
// job link lives in the header only. Keys: 1 to 9 press the choices in order (the same path as a
// tap, the second tap included).
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CheckCheck, FolderLock, Loader2, MessageSquarePlus, RefreshCw, Send, ShieldCheck, Star, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import type { Approval } from "../../lib/approvals";
import { cn } from "../../lib/cn";
import { inSentence, loadNote, needsSecondTap, noteAnswered, saveNote } from "../../lib/decision";
import { focusContext } from "../../lib/hotkeys";
import { LATE_REQUEST_MS } from "../../lib/pending";
import { buildAnswer, canSubmit, type Question } from "../../lib/questions";
import { useAnswerQuestion, useApproval, useDecideApproval, useQuestion, useSendKeys } from "../../lib/queries";
import type { AgentScreen, DialogOption } from "../../lib/types";

export type BarTarget =
  | { kind: "question"; question: Question }
  | { kind: "approval"; approval: Approval }
  | { kind: "dialog"; paneId: string; screen: AgentScreen; agentName: string };

interface AnswerBarProps {
  itemId: string;
  jobId?: string;
  target: BarTarget;
  /** After a successful answer: the packet moves on to the next decision. Gets what was answered. */
  onAnswered: (answered: string) => void;
  /** Choices are inert until this time (ms since the epoch): the auto-advance guard. */
  armedAt?: number;
}

/** More options than this scroll inside the bar instead of pushing the page up. */
const MAX_INLINE_OPTIONS = 3;
/** Two options whose labels are this short sit side by side on a phone. */
const SIDE_BY_SIDE_CHARS = 22;

/** What a press resolves to: "done" moves on; "handled" means the press already said why it stopped. */
type PressResult = "done" | "handled";

/** One press of a choice: its key, what it says while sending, and the work. */
interface Press {
  key: string;
  label: string;
  task: () => Promise<PressResult>;
}

export function AnswerBar({ itemId, target, onAnswered, armedAt = 0 }: AnswerBarProps) {
  const noteId = useId();
  const barRef = useRef<HTMLDivElement>(null);
  const [note, setNoteState] = useState(() => loadNote(itemId));
  const [noteOpen, setNoteOpen] = useState(() => loadNote(itemId) !== "");
  const [notice, setNotice] = useState<string | null>(null);
  const sending = useSending();
  const armed = useArmed(armedAt);
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
  useDigitKeys(barRef, armed);
  const done = (answered: string) => {
    // A short tick where the phone has one: the answer arrived (R10 Fable Delight 3).
    navigator.vibrate?.(12);
    saveNote(itemId, "");
    noteAnswered(answered);
    onAnswered(answered);
  };
  const press = (next: Press) => {
    if (!armed) return;
    setNotice(null);
    sending.run(next);
  };
  const manual = target.kind === "question" && target.question.kind === "manual";
  const showNote = target.kind !== "dialog";
  const choice: ChoiceProps = { note, press, sendingKey: sending.key, armed, onNotice: setNotice, onDone: done };

  return (
    <div
      ref={barRef}
      role="region"
      aria-label="Answer"
      data-testid="answer-bar"
      data-armed={armed ? "true" : "false"}
      className="sticky bottom-0 z-20 border-t border-border bg-surface-raised px-3 pb-2 pt-2"
    >
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-1.5">
        <SendStatus sending={sending} notice={notice} />
        {showNote &&
          (manual || noteOpen ? (
            <div className="flex flex-col gap-0.5">
              <label htmlFor={noteId} className="text-2xs font-medium text-muted">
                {manual ? "Your answer" : target.kind === "approval" ? "Note (sent with a deny)" : "Note (optional; it outranks the choice)"}
              </label>
              <textarea
                id={noteId}
                value={note}
                onChange={(event) => setNote(event.target.value)}
                rows={manual ? 3 : 1}
                autoFocus={!manual && noteOpen && note === ""}
                maxLength={target.kind === "approval" ? 2000 : 8000}
                placeholder={manual ? "Write the answer the agent asked for" : "Add context or a condition"}
                className="max-h-28 min-h-11 w-full resize-y rounded border border-border-strong bg-surface px-2.5 py-2 text-base text-heading placeholder:text-muted focus:border-accent focus:outline-none md:min-h-8 md:text-xs"
              />
            </div>
          ) : (
            <button
              type="button"
              onClick={() => setNoteOpen(true)}
              className="inline-flex min-h-9 items-center gap-1 self-start rounded px-1 text-xs text-accent hover:underline md:min-h-6"
            >
              <MessageSquarePlus className="h-3.5 w-3.5" aria-hidden />
              {target.kind === "approval" ? "Add a note (sent with a deny)" : "Add a note"}
            </button>
          ))}
        {target.kind === "question" && <QuestionChoices question={target.question} {...choice} />}
        {target.kind === "approval" && <ApprovalChoices approval={target.approval} {...choice} />}
        {target.kind === "dialog" && <DialogChoices paneId={target.paneId} screen={target.screen} agentName={target.agentName} {...choice} />}
      </div>
    </div>
  );
}

// ---- Sending, late, failed ---------------------------------------------------------------------

interface Sending {
  /** The key being sent, or null. */
  key: string | null;
  label: string | null;
  /** True once the send has waited past LATE_REQUEST_MS. */
  late: boolean;
  failed: { label: string; message: string; retry: () => void } | null;
  run: (press: Press) => void;
}

function useSending(): Sending {
  const [current, setCurrent] = useState<{ key: string; label: string } | null>(null);
  const [late, setLate] = useState(false);
  const [failed, setFailed] = useState<Sending["failed"]>(null);
  const key = current?.key ?? null;
  useEffect(() => {
    if (!key) return;
    const timer = setTimeout(() => setLate(true), LATE_REQUEST_MS);
    return () => clearTimeout(timer);
  }, [key]);
  const run = (press: Press) => {
    setFailed(null);
    setLate(false);
    setCurrent({ key: press.key, label: press.label });
    press.task().then(
      () => {
        setCurrent(null);
        setLate(false);
      },
      (failure: unknown) => {
        setCurrent(null);
        setLate(false);
        setFailed({ label: press.label, message: failure instanceof Error ? failure.message : String(failure), retry: () => run(press) });
      },
    );
  };
  return { key, label: current?.label ?? null, late, failed, run };
}

function SendStatus({ sending, notice }: { sending: Sending; notice: string | null }) {
  if (sending.failed) {
    return (
      <div role="alert" className="flex items-start gap-2 text-xs font-medium text-danger">
        <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
        <span className="min-w-0 flex-1">
          Not sent: “{sending.failed.label}”. {sending.failed.message}
        </span>
        <button
          type="button"
          onClick={sending.failed.retry}
          className="-my-1 inline-flex min-h-9 shrink-0 items-center gap-1 rounded border border-danger/60 px-2 text-xs text-danger hover:bg-danger/10 md:min-h-6"
        >
          <RefreshCw className="h-3.5 w-3.5" aria-hidden />
          Retry
        </button>
      </div>
    );
  }
  if (notice) {
    return (
      <p role="alert" className="text-xs font-medium text-danger">
        {notice}
      </p>
    );
  }
  return (
    <p role="status" aria-live="polite" className={cn("text-xs", sending.late ? "font-medium text-warning" : "text-muted", !sending.key && "sr-only")}>
      {sending.key
        ? sending.late
          ? `Still sending “${sending.label}”: the server has not answered yet. It goes through when it does; you can leave this page.`
          : `Sending “${sending.label}”…`
        : ""}
    </p>
  );
}

/**
 * False until armedAt (the auto-advance guard), then true; re-renders when it passes. armedAt is
 * fixed for a bar's life: the packet is keyed by its item, so each item gets a bar of its own.
 */
function useArmed(armedAt: number): boolean {
  const [armed, setArmed] = useState(() => Date.now() >= armedAt);
  useEffect(() => {
    const wait = armedAt - Date.now();
    if (wait <= 0) return;
    const timer = setTimeout(() => setArmed(true), wait);
    return () => clearTimeout(timer);
  }, [armedAt]);
  return armed;
}

/** 1 to 9 press the bar's choices in order, outside a text field, once the bar is armed. */
function useDigitKeys(barRef: React.RefObject<HTMLDivElement | null>, armed: boolean) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!armed || event.repeat || event.metaKey || event.ctrlKey || event.altKey || focusContext(event.target, false).typing || focusContext(event.target, false).terminal) return;
      if (!/^[1-9]$/.test(event.key)) return;
      const buttons = barRef.current?.querySelectorAll<HTMLButtonElement>("button[data-choice]:not(:disabled)");
      const button = buttons?.[Number(event.key) - 1];
      if (!button) return;
      event.preventDefault();
      button.click();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [barRef, armed]);
}

/** A press's failure as a rejection the bar shows with Retry; a 409 refetches and is "handled". */
function settle(failure: unknown, onConflict: () => void): PressResult {
  if (failure instanceof ApiError && failure.status === 409) {
    onConflict();
    return "handled";
  }
  throw failure;
}

interface ChoiceProps {
  note: string;
  press: (press: Press) => void;
  sendingKey: string | null;
  armed: boolean;
  onNotice: (message: string | null) => void;
  onDone: (answered: string) => void;
}

// ---- Questions --------------------------------------------------------------------------------

function QuestionChoices({ question, note, press, sendingKey, armed, onDone }: ChoiceProps & { question: Question }) {
  const answer = useAnswerQuestion(question.id);
  const refetch = useQuestion(question.id).refetch;
  const [picked, setPicked] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<string | null>(null);
  const submit = (keys: string[], label: string) => {
    if (!canSubmit(question, keys, note)) return;
    press({
      key: keys.join(",") || "submit",
      label,
      task: () =>
        answer.mutateAsync(buildAnswer(question, keys, note)).then(
          () => {
            onDone(`Answered “${question.title}”`);
            return "done" as const;
          },
          (failure: unknown) => settle(failure, () => void refetch()),
        ),
    });
  };
  const busy = sendingKey !== null;
  const pending = question.options.find((option) => option.key === confirming);
  if (pending) {
    return (
      <div className="flex flex-col gap-1.5" role="group" aria-label="Confirm the choice">
        <p className="flex items-start gap-1.5 text-xs font-medium text-warning">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
          “{pending.label}” lets the agent go ahead with this. Tap again to confirm.
        </p>
        <div className="grid grid-cols-2 gap-1.5">
          <BarButton primary choice onPress={() => submit([pending.key], pending.label)} disabled={busy} sending={sendingKey === pending.key} icon={<ShieldCheck className="h-4 w-4" aria-hidden />}>
            Yes, {inSentence(pending.label)}
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
      <OptionList count={question.options.length} sideBySide={sideBySide(question.options.map((option) => option.label))}>
        {question.options.map((option) => (
          <BarButton
            key={option.key}
            choice
            onPress={() => (needsSecondTap(question, option.key) ? setConfirming(option.key) : submit([option.key], option.label))}
            disabled={busy}
            inert={!armed}
            sending={sendingKey === option.key}
            recommended={option.key === question.recommend}
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
        <BarButton
          primary
          choice
          onPress={() => submit(picked, `${picked.length} chosen`)}
          disabled={busy || picked.length === 0}
          inert={!armed}
          sending={sendingKey === picked.join(",")}
          icon={<Send className="h-4 w-4" aria-hidden />}
        >
          Submit {picked.length > 0 ? `(${picked.length})` : ""}
        </BarButton>
      </div>
    );
  }
  const label = question.kind === "ack" ? "Acknowledge" : question.kind === "manual" ? "Send answer" : "Dismiss";
  const icon = question.kind === "ack" ? <CheckCheck className="h-4 w-4" aria-hidden /> : question.kind === "manual" ? <Send className="h-4 w-4" aria-hidden /> : <X className="h-4 w-4" aria-hidden />;
  return (
    <BarButton
      primary
      choice
      onPress={() => submit([], label)}
      disabled={busy || (question.kind === "manual" && !note.trim())}
      inert={!armed}
      sending={sendingKey === "submit"}
      icon={icon}
    >
      {label}
    </BarButton>
  );
}

// ---- Approvals --------------------------------------------------------------------------------

function ApprovalChoices({ approval, note, press, sendingKey, armed, onDone }: ChoiceProps & { approval: Approval }) {
  const decide = useDecideApproval(approval.id);
  const refetch = useApproval(approval.id).refetch;
  const [confirming, setConfirming] = useState(false);
  const dangerous = approval.risk === "dangerous";
  const send = (allow: boolean, confirm = false) =>
    press({
      key: allow ? "allow" : "deny",
      label: allow ? "Allow once" : "Deny",
      task: () =>
        decide.mutateAsync({ decision: allow ? "allow" : "deny", note: note.trim() || undefined, confirm: confirm || undefined }).then(
          () => {
            onDone(`${allow ? "Allowed" : "Denied"} ${approval.summary}`);
            return "done" as const;
          },
          (failure: unknown) => {
            if (failure instanceof ApiError && failure.code === "CONFIRM_REQUIRED") {
              setConfirming(true);
              return "handled" as const;
            }
            return settle(failure, () => void refetch());
          },
        ),
    });
  const busy = sendingKey !== null;
  if (confirming) {
    return (
      <div className="flex flex-col gap-1.5" role="group" aria-label="Confirm a dangerous allow">
        <p className="flex items-start gap-1.5 text-xs text-danger">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
          <span>
            <span className="font-semibold">This is dangerous. Allow it anyway?</span> {(approval.risk_reasons.join(". ") || "It matched a dangerous rule") + ". It runs as you, now, once."}
          </span>
        </p>
        <div className="grid grid-cols-2 gap-1.5">
          <BarButton danger choice onPress={() => send(true, true)} disabled={busy} sending={sendingKey === "allow"} icon={<ShieldCheck className="h-4 w-4" aria-hidden />}>
            Yes, allow it
          </BarButton>
          <BarButton onPress={() => setConfirming(false)} disabled={busy}>
            Cancel
          </BarButton>
        </div>
      </div>
    );
  }
  return (
    <div className="grid grid-cols-2 gap-1.5" role="group" aria-label="Choices">
      <BarButton
        primary={!dangerous}
        danger={dangerous}
        choice
        onPress={() => (dangerous ? setConfirming(true) : send(true))}
        disabled={busy}
        inert={!armed}
        sending={sendingKey === "allow"}
      >
        {dangerous ? "Allow once…" : "Allow once"}
      </BarButton>
      <BarButton choice onPress={() => send(false)} disabled={busy} inert={!armed} sending={sendingKey === "deny"}>
        Deny
      </BarButton>
    </div>
  );
}

// ---- Dialogs ----------------------------------------------------------------------------------

function DialogChoices({ paneId, screen, agentName, press, sendingKey, armed, onNotice, onDone }: ChoiceProps & { paneId: string; screen: AgentScreen; agentName: string }) {
  const sendKeys = useSendKeys(paneId);
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
  const pressOption = (option: DialogOption, key: string, answered: string) => {
    const keys = option.keys && option.keys.length > 0 ? option.keys : [option.key];
    press({
      key,
      label: option.label,
      task: () =>
        sendKeys.mutateAsync({ keys, screen_hash: screen.screen_hash }).then(
          (result) => {
            if (result.dialog_gone === false) {
              // The server watched the screen: the same question is still up (DESIGN.md 8.2.1).
              onNotice("Your answer was sent, but the same question is still on the agent's screen. Look at the screen above; if it has not moved, open the terminal.");
              return "handled" as const;
            }
            onDone(answered);
            return "done" as const;
          },
          (failure: unknown) => {
            // A 409: the screen changed. The refetch the hook does redraws the packet with the new
            // screen (or its stale notice); say why nothing happened.
            if (failure instanceof ApiError && failure.status === 409) {
              onNotice("The screen changed before the press arrived; nothing was sent. Check it again.");
              return "handled" as const;
            }
            throw failure;
          },
        ),
    });
  };
  const busy = sendingKey !== null;
  const trust = screen.dialog.trust;
  const who = agentName || "the agent";
  if (trust) {
    const folder = trust.folder || "its folder";
    return (
      <OptionList count={2} sideBySide>
        <BarButton primary choice onPress={() => pressOption(trust.allow, "trust", `${who} may work in ${folder}`)} disabled={busy} inert={!armed} sending={sendingKey === "trust"} icon={<FolderLock className="h-4 w-4" aria-hidden />}>
          Trust this folder
        </BarButton>
        <BarButton choice onPress={() => pressOption(trust.deny, "deny", `${who} was told not to trust ${folder}`)} disabled={busy} inert={!armed} sending={sendingKey === "deny"} icon={<X className="h-4 w-4" aria-hidden />}>
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
          choice
          onPress={() => pressOption(option, `${option.key}-${index}`, `${option.label} (${who})`)}
          disabled={busy}
          inert={!armed}
          sending={sendingKey === `${option.key}-${index}`}
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

/** True when every label is short enough for two buttons side by side on a phone. */
function sideBySide(labels: string[]): boolean {
  return labels.length === 2 && labels.every((label) => label.length <= SIDE_BY_SIDE_CHARS);
}

function OptionList({ count, sideBySide: pair = false, children }: { count: number; sideBySide?: boolean; children: React.ReactNode }) {
  return (
    <div
      className={cn(pair ? "grid grid-cols-2 gap-1.5" : "flex flex-col gap-1.5", count > MAX_INLINE_OPTIONS && "max-h-[38vh] overflow-y-auto overscroll-contain pr-0.5")}
      role="group"
      aria-label="Choices"
    >
      {children}
    </div>
  );
}

interface BarButtonProps {
  onPress: () => void;
  disabled?: boolean;
  /** Shown but not pressable yet (the auto-advance guard): aria-disabled, so it keeps its place in the tab order. */
  inert?: boolean;
  /** This press is on its way: a spinner and "Sending…". */
  sending?: boolean;
  /** One of the bar's answers (the 1 to 9 keys press these in order). */
  choice?: boolean;
  primary?: boolean;
  danger?: boolean;
  recommended?: boolean;
  icon?: React.ReactNode;
  children: React.ReactNode;
}

function BarButton({ onPress, disabled, inert, sending, choice, primary, danger, recommended, icon, children }: BarButtonProps) {
  return (
    <button
      type="button"
      onClick={() => {
        if (!inert) onPress();
      }}
      disabled={disabled}
      aria-disabled={inert || undefined}
      aria-busy={sending || undefined}
      data-choice={choice ? "" : undefined}
      className={cn(
        "flex min-h-12 w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm font-medium transition-[colors,opacity] disabled:opacity-50 md:min-h-9",
        inert && "cursor-wait opacity-60",
        primary
          ? "border-accent bg-accent text-accent-fg hover:bg-accent/90"
          : danger
            ? "border-danger/60 bg-danger/5 text-danger hover:bg-danger/10"
            : recommended
              ? "border-accent/60 bg-accent/5 text-heading hover:bg-accent/10"
              : "border-border-strong bg-surface text-heading hover:bg-hover",
        sending && "disabled:opacity-100",
      )}
    >
      {sending ? <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden /> : icon && <span className="shrink-0">{icon}</span>}
      <span className="min-w-0 flex-1 break-words">{sending ? "Sending…" : children}</span>
      {recommended && !sending && (
        <span className="inline-flex shrink-0 items-center gap-1 rounded bg-accent/10 px-1.5 py-0.5 text-2xs font-medium text-accent">
          <Star className="h-3 w-3" aria-hidden />
          <span className="sr-only sm:not-sr-only">Recommended</span>
        </span>
      )}
    </button>
  );
}
