// The Chat composer with a microphone (DESIGN.md 13.1), used when the server has voice on:
//
// - MicButton, beside Send, always there: hold to talk (or tap to lock on, tap again to send). The
//   first press asks for the microphone; nothing else needs switching on first. Push to talk does
//   not need speak mode, and speak mode does not need the microphone.
// - VoiceStatus, above the box while something is happening: starting the microphone, recording
//   with elapsed time against the five-minute cap (a warning in the last 30 s; at the cap the
//   recording stops and is sent), transcribing, the transcript for 1.5 s with Cancel and Send
//   now, or what went wrong.
// - useSpaceToTalk: holding Space anywhere outside a text field records for as long as it is held
//   and sends on release. In a text field Space types a space, so the composer offers a chord
//   there: hold Option+Space (Ctrl+Space off a Mac).
import { useTrayKeepOut } from "../../lib/trayKeepOut";
import { Loader2, Lock, Mic, MicOff, Square, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { endpoints } from "../../lib/api";
import { cn } from "../../lib/cn";
import { DESKTOP_QUERY, useMediaQuery } from "../../lib/media";
import { MAX_RECORDING_MS, formatElapsed, nearLimit } from "../../lib/voice/ptt";
import { usePushToTalk, type PushToTalk } from "../../lib/voice/recorder";
import { insecureNotice, micSupport, unsupportedNotice } from "../../lib/voice/support";
import { Composer, type ComposerProps } from "../chat/Composer";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";

/** Whether this is a Mac, for the chord's name (Option on a Mac, Ctrl elsewhere). */
const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
/** The chord that talks from inside a text field. */
const chordLabel = isMac ? "⌥ Space" : "Ctrl+Space";

/** States in which nothing can be sent to the agent, so the microphone is off too. */
const unavailable = new Set(["blocked", "needs_terminal", "off", "failed"]);

export function VoiceComposer(props: ComposerProps) {
  const { target, onSent, state } = props;
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const [notice, setNotice] = useState<string | null>(null);
  const send = useCallback(
    async (text: string) => {
      const result = await endpoints.prompt(target, { text, voice: true });
      onSent?.(result);
    },
    [target, onSent],
  );
  const ptt = usePushToTalk(send);
  const enabled = !unavailable.has(state);
  const support = micSupport();
  // Without a microphone a press explains why instead of starting.
  const guarded: PushToTalk = {
    ...ptt,
    press: (source) => {
      if (support !== "ok") {
        setNotice(support === "insecure" ? insecureNotice : unsupportedNotice);
        return;
      }
      setNotice(null);
      ptt.press(source);
    },
  };
  useSpaceToTalk(guarded, enabled && desktop);
  // The talk review (Send now, Cancel) sits above the composer: the tray keeps clear of both.
  const rootRef = useRef<HTMLDivElement>(null);
  useTrayKeepOut(rootRef, "composer");

  return (
    <div ref={rootRef} className="flex flex-col gap-1.5">
      {notice && (
        <Banner tone="warning" icon={MicOff} role="status" className="rounded" action={<Button compact variant="ghost" icon={X} onClick={() => setNotice(null)} aria-label="Dismiss" />}>
          {notice}
        </Banner>
      )}
      <VoiceStatus ptt={ptt} />
      <Composer
        {...props}
        accessory={enabled ? <MicButton ptt={guarded} /> : undefined}
        extraHint={
          enabled ? (
            <span data-testid="space-hint">
              Hold <kbd className="font-mono">Space</kbd> to talk (<kbd className="font-mono">{chordLabel}</kbd> while typing).
            </span>
          ) : undefined
        }
      />
    </div>
  );
}

// ---- The microphone button ---------------------------------------------------------------

function MicButton({ ptt }: { ptt: PushToTalk }) {
  const { state } = ptt;
  const recording = state.phase === "recording";
  const starting = state.phase === "starting";
  const locked = (recording || starting) && state.mode === "lock";
  const busy = state.phase === "uploading" || state.phase === "sending";

  const onPointerDown = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault(); // no focus, no text selection, no long-press menu
    event.currentTarget.setPointerCapture?.(event.pointerId);
    ptt.press("pointer");
  };
  const onPointerUp = (event: ReactPointerEvent<HTMLButtonElement>) => {
    event.preventDefault();
    ptt.release();
  };

  const label = locked
    ? "Recording (locked). Tap to send."
    : recording
      ? "Recording. Release to send."
      : starting
        ? "Starting the microphone"
        : busy
          ? "Transcribing"
          : "Hold to talk";

  return (
    <button
      type="button"
      disabled={busy}
      onPointerDown={onPointerDown}
      onPointerUp={onPointerUp}
      onPointerCancel={() => ptt.cancel()}
      onContextMenu={(event) => event.preventDefault()}
      // The keyboard path for a focused mic is Space (useSpaceToTalk) or Enter, held.
      onKeyDown={(event) => {
        if (event.key === "Enter" && !event.repeat) {
          event.preventDefault();
          ptt.press("key");
        }
      }}
      onKeyUp={(event) => {
        if (event.key === "Enter") ptt.release();
      }}
      aria-label={label}
      title={`${label} (or hold Space)`}
      aria-pressed={recording || locked}
      data-phase={state.phase}
      data-testid="mic-button"
      className={cn(
        "inline-flex h-11 w-11 shrink-0 touch-none select-none items-center justify-center rounded border transition-colors [-webkit-touch-callout:none] disabled:opacity-50 md:h-9 md:w-9",
        recording || starting ? "border-danger bg-danger text-white" : "border-accent/60 bg-accent/10 text-accent hover:bg-accent/20",
      )}
    >
      {busy || starting ? (
        <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
      ) : locked ? (
        <Lock aria-hidden className="h-4 w-4" />
      ) : recording ? (
        <Square aria-hidden className="h-3.5 w-3.5 fill-current" />
      ) : (
        <Mic aria-hidden className="h-5 w-5 md:h-4 md:w-4" />
      )}
    </button>
  );
}

// ---- What is happening -------------------------------------------------------------------

function VoiceStatus({ ptt }: { ptt: PushToTalk }) {
  const { state } = ptt;
  const elapsed = useElapsed(state.phase === "recording" ? state.startedAt : null);

  switch (state.phase) {
    case "idle":
      return null;
    case "error":
      return (
        <Banner tone="danger" role="alert" className="rounded" action={<Button compact variant="ghost" icon={X} onClick={ptt.dismiss} aria-label="Dismiss" />}>
          {state.message}
        </Banner>
      );
    case "starting":
      return (
        <StatusRow tone="muted" testId="voice-status">
          <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
          <span className="flex-1">Starting the microphone…</span>
          <Button compact variant="ghost" icon={X} onClick={ptt.cancel}>
            Cancel
          </Button>
        </StatusRow>
      );
    case "recording": {
      const warn = nearLimit(elapsed);
      const left = Math.max(0, MAX_RECORDING_MS - elapsed);
      return (
        <StatusRow tone={warn ? "warning" : "danger"} testId="voice-status">
          <span aria-hidden className="h-2.5 w-2.5 shrink-0 animate-pulse rounded-full bg-danger" />
          <span className="min-w-0 flex-1">
            <span className="font-medium">Recording</span>{" "}
            <span className="font-mono tabular-nums" data-testid="recording-elapsed">
              {formatElapsed(elapsed)} / {formatElapsed(MAX_RECORDING_MS)}
            </span>
            {warn ? (
              <span className="ml-1.5 font-medium" role="alert" data-testid="recording-limit-warning">
                Stops and sends in {formatElapsed(left + 999)}
              </span>
            ) : (
              <span className="ml-1.5 text-muted">{state.mode === "lock" ? "Tap the mic to send." : "Let go to send."}</span>
            )}
          </span>
          <Button compact variant="ghost" icon={X} onClick={ptt.cancel} aria-label="Cancel recording">
            <span className="hidden sm:inline">Cancel</span>
          </Button>
        </StatusRow>
      );
    }
    case "uploading":
      return (
        <StatusRow tone="muted" testId="voice-status">
          <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
          <span>Transcribing…</span>
        </StatusRow>
      );
    case "review":
    case "sending":
      return (
        <div role="status" aria-live="polite" className="flex min-h-11 items-center gap-2 rounded border border-accent/40 bg-accent/5 px-3 py-1.5">
          <div className="min-w-0 flex-1">
            <p className="text-2xs font-medium uppercase tracking-wide text-accent">{state.phase === "review" ? "Sending what you said…" : "Sending…"}</p>
            <p className="break-words text-sm text-heading" data-testid="voice-transcript">
              {state.text}
            </p>
          </div>
          {state.phase === "review" && (
            <span className="flex shrink-0 gap-1">
              <Button compact variant="ghost" icon={X} onClick={ptt.cancel}>
                Cancel
              </Button>
              <Button compact variant="primary" onClick={ptt.sendNow}>
                Send now
              </Button>
            </span>
          )}
        </div>
      );
  }
}

function StatusRow({ tone, testId, children }: { tone: "muted" | "danger" | "warning"; testId: string; children: ReactNode }) {
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid={testId}
      className={cn(
        "flex min-h-11 items-center gap-2 rounded border px-3 py-1 text-xs md:min-h-9",
        tone === "danger" && "border-danger/40 bg-danger/5 text-heading",
        tone === "warning" && "border-warning bg-warning/10 text-heading",
        tone === "muted" && "border-border bg-surface-raised text-muted",
      )}
    >
      {children}
    </div>
  );
}

function useElapsed(startedAt: number | null): number {
  const [now, setNow] = useState(() => performance.now());
  useEffect(() => {
    if (startedAt === null) return;
    const timer = window.setInterval(() => setNow(performance.now()), 250);
    return () => window.clearInterval(timer);
  }, [startedAt]);
  return startedAt === null ? 0 : Math.max(0, now - startedAt);
}

// ---- Space to talk -----------------------------------------------------------------------

/** Whether a key event's target is somewhere text is typed. */
function isTextField(target: EventTarget | null): boolean {
  return target instanceof HTMLElement && (target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName));
}

/** Whether a key event's target is inside a dialog or menu, whose own buttons Space must work. */
function inDialog(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest("[role=dialog],[role=menu],[role=listbox]");
}

/**
 * Hold Space to talk (DESIGN.md 13.1). Outside text fields, Space is taken over completely while
 * this is mounted: its default action is prevented on keydown and keyup, so it never scrolls the
 * page or presses a focused button. Inside a text field Space types; Option+Space (Ctrl+Space)
 * talks there instead. Key auto-repeat is ignored. If the window loses focus or the page is hidden
 * while the key is down (the keyup would never arrive), the recording ends there as if released.
 */
function useSpaceToTalk(ptt: PushToTalk, enabled: boolean) {
  const latest = useRef(ptt);
  useEffect(() => {
    latest.current = ptt;
  });
  useEffect(() => {
    if (!enabled) return;
    let holding = false;
    const endHold = () => {
      if (!holding) return;
      holding = false;
      latest.current.release();
    };
    const down = (event: KeyboardEvent) => {
      if (event.key === "Escape" && (latest.current.state.phase === "recording" || latest.current.state.phase === "starting")) {
        latest.current.cancel();
        return;
      }
      if (event.code !== "Space" || event.metaKey || inDialog(event.target)) return;
      const chord = event.altKey || event.ctrlKey;
      if (isTextField(event.target) && !chord) return; // typing a space
      event.preventDefault();
      if (event.repeat || holding) return;
      holding = true;
      latest.current.press("key");
    };
    const up = (event: KeyboardEvent) => {
      if (event.code !== "Space" || !holding) return;
      event.preventDefault();
      endHold();
    };
    const hidden = () => {
      if (document.visibilityState === "hidden") endHold();
    };
    window.addEventListener("keydown", down, true);
    window.addEventListener("keyup", up, true);
    window.addEventListener("blur", endHold);
    document.addEventListener("visibilitychange", hidden);
    return () => {
      window.removeEventListener("keydown", down, true);
      window.removeEventListener("keyup", up, true);
      window.removeEventListener("blur", endHold);
      document.removeEventListener("visibilitychange", hidden);
      endHold();
    };
  }, [enabled]);
}
