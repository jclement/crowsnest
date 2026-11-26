// Talk mode in the Chat view (DESIGN.md 13), shown only when the server has voice on:
//
// - TalkToggle, in the header: switches talk mode on for this tab. The click is the user gesture
//   that unlocks audio playback, so spoken replies can play later without one.
// - TalkPanel, inside the composer while talk mode is on: the large push-to-talk button (hold to
//   talk, tap to lock on; the space bar does the same on a desktop), the recording indicator with
//   elapsed time, the transcribed text for 1.5 s with Cancel before it is sent, and the player
//   bar (what is being said, Stop). A page without a secure context gets a notice instead of the
//   button, because the browser offers it no microphone.
import { Loader2, Lock, Mic, MicOff, Square, Volume2, VolumeX, X } from "lucide-react";
import { useCallback, useEffect, useState, type PointerEvent as ReactPointerEvent } from "react";
import { endpoints } from "../../lib/api";
import { cn } from "../../lib/cn";
import { MAX_RECORDING_MS, formatElapsed } from "../../lib/voice/ptt";
import { player, usePlayer } from "../../lib/voice/player";
import { usePushToTalk, type PushToTalk } from "../../lib/voice/recorder";
import { insecureNotice, micSupport, unsupportedNotice } from "../../lib/voice/support";
import { setTalkMode, useTalkMode } from "../../lib/voice/talk";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";

export function TalkToggle() {
  const on = useTalkMode();
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={() => setTalkMode(!on)}
      title={on ? "Talk mode is on: replies are spoken. Click to turn it off." : "Turn talk mode on: push to talk, hear replies."}
      className={cn(
        "inline-flex min-h-11 items-center gap-1.5 rounded border px-2.5 text-xs font-medium transition-colors md:min-h-7",
        on ? "border-accent bg-accent text-accent-fg" : "border-border-strong bg-surface text-heading hover:bg-hover",
      )}
    >
      {on ? <Volume2 aria-hidden className="h-4 w-4" /> : <VolumeX aria-hidden className="h-4 w-4" />}
      Talk
    </button>
  );
}

interface TalkPanelProps {
  /** The API target the transcribed text is sent to ("orchestrator"). */
  target: string;
  /** Called after a voice prompt was sent (the conversation scrolls down). */
  onSent?: () => void;
  disabled?: boolean;
}

export function TalkPanel({ target, onSent, disabled }: TalkPanelProps) {
  const on = useTalkMode();
  const send = useCallback(
    async (text: string) => {
      await endpoints.prompt(target, { text, voice: true });
      onSent?.();
    },
    [target, onSent],
  );
  const ptt = usePushToTalk(send);
  if (!on) return null;
  const support = micSupport();
  return (
    <div className="flex flex-col gap-1.5" data-testid="talk-panel">
      <PlayerBar />
      {support === "ok" ? (
        <PushToTalkControl ptt={ptt} disabled={disabled} />
      ) : (
        <Banner tone="warning" icon={MicOff} role="status" className="rounded">
          {support === "insecure" ? insecureNotice : unsupportedNotice}
        </Banner>
      )}
    </div>
  );
}

// ---- Push to talk ----------------------------------------------------------------------

function PushToTalkControl({ ptt, disabled }: { ptt: PushToTalk; disabled?: boolean }) {
  const { state } = ptt;
  useSpaceBar(ptt, !disabled);

  if (state.phase === "review" || state.phase === "sending") {
    return (
      <div role="status" aria-live="polite" className="flex min-h-14 items-center gap-2 rounded border border-accent/40 bg-accent/5 px-3 py-2">
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

  return (
    <div className="flex flex-col gap-1">
      {state.phase === "error" && (
        <Banner tone="danger" role="alert" className="rounded" action={<Button compact variant="ghost" icon={X} onClick={ptt.dismiss} aria-label="Dismiss" />}>
          {state.message}
        </Banner>
      )}
      <TalkButton ptt={ptt} disabled={disabled} />
    </div>
  );
}

function TalkButton({ ptt, disabled }: { ptt: PushToTalk; disabled?: boolean }) {
  const { state } = ptt;
  const recording = state.phase === "recording";
  const busy = state.phase === "starting" || state.phase === "uploading";
  const locked = (state.phase === "recording" || state.phase === "starting") && state.mode === "lock";
  const elapsed = useElapsed(recording ? state.startedAt : null);

  const onPointerDown = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0 || disabled) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture?.(event.pointerId);
    ptt.press();
  };
  const onPointerUp = (event: ReactPointerEvent<HTMLButtonElement>) => {
    event.preventDefault();
    ptt.release();
  };

  const label = locked
    ? "Recording (locked). Tap to send."
    : recording
      ? "Recording. Release to send."
      : state.phase === "uploading"
        ? "Transcribing…"
        : state.phase === "starting"
          ? "Starting the microphone…"
          : "Hold to talk";

  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        disabled={disabled || state.phase === "uploading"}
        onPointerDown={onPointerDown}
        onPointerUp={onPointerUp}
        onPointerCancel={() => ptt.cancel()}
        onContextMenu={(event) => event.preventDefault()}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.repeat) ptt.press();
        }}
        onKeyUp={(event) => {
          if (event.key === "Enter") ptt.release();
        }}
        aria-label={label}
        aria-pressed={recording || locked}
        data-phase={state.phase}
        className={cn(
          "flex min-h-14 flex-1 touch-none select-none items-center justify-center gap-2 rounded-lg border-2 px-4 text-sm font-semibold transition-colors [-webkit-touch-callout:none] disabled:opacity-50",
          recording ? "border-danger bg-danger text-white" : "border-accent bg-accent/10 text-accent hover:bg-accent/15",
        )}
      >
        {busy ? (
          <Loader2 aria-hidden className="h-5 w-5 animate-spin" />
        ) : locked ? (
          <Lock aria-hidden className="h-5 w-5" />
        ) : recording ? (
          <span aria-hidden className="h-3 w-3 animate-pulse rounded-full bg-white" />
        ) : (
          <Mic aria-hidden className="h-5 w-5" />
        )}
        <span>{label}</span>
        {recording && (
          <span className="font-mono tabular-nums" data-testid="recording-elapsed">
            {formatElapsed(elapsed)} / {formatElapsed(MAX_RECORDING_MS)}
          </span>
        )}
      </button>
      {(recording || state.phase === "starting") && (
        <Button variant="ghost" icon={X} onClick={ptt.cancel} aria-label="Cancel recording" className="min-w-11" />
      )}
      <span className="hidden text-2xs text-muted md:inline">or hold Space</span>
    </div>
  );
}

/** Space bar push-to-talk on a desktop, unless typing in a field. */
function useSpaceBar(ptt: PushToTalk, enabled: boolean) {
  const { press, release, cancel } = ptt;
  useEffect(() => {
    if (!enabled) return;
    const typing = (target: EventTarget | null) =>
      target instanceof HTMLElement && (target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName));
    const down = (event: KeyboardEvent) => {
      if (event.key === "Escape") return cancel();
      if (event.code !== "Space" || typing(event.target)) return;
      event.preventDefault();
      if (!event.repeat) press();
    };
    const up = (event: KeyboardEvent) => {
      if (event.code !== "Space" || typing(event.target)) return;
      event.preventDefault();
      release();
    };
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
    };
  }, [enabled, press, release, cancel]);
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

// ---- Player ----------------------------------------------------------------------------

/** What is being said, with Stop; or Play when the browser refused to start audio. */
export function PlayerBar() {
  const snapshot = usePlayer();
  if (!snapshot.current && !snapshot.failed) return null;
  if (!snapshot.current) {
    return <p className="text-2xs text-muted">A spoken reply could not be played.</p>;
  }
  return (
    <div role="status" aria-live="polite" className="flex min-h-9 items-center gap-2 rounded border border-border bg-surface-raised px-2 py-1 text-xs" data-testid="player-bar">
      <Volume2 aria-hidden className="h-4 w-4 shrink-0 text-accent" />
      <p className="min-w-0 flex-1 truncate text-heading" title={snapshot.current.text}>
        {snapshot.blocked ? "Tap Play to hear the reply." : snapshot.current.text}
        {snapshot.queued > 0 && <span className="text-muted"> (+{snapshot.queued} more)</span>}
      </p>
      {snapshot.blocked ? (
        <Button compact variant="primary" icon={Volume2} onClick={() => player.unlock()}>
          Play
        </Button>
      ) : (
        <Button compact variant="ghost" icon={Square} onClick={() => player.stop()}>
          Stop
        </Button>
      )}
    </div>
  );
}
