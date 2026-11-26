// The push-to-talk state machine (DESIGN.md 13), as a pure reducer so every transition is tested
// without a browser. The hook in recorder.ts runs the side effects (start the microphone, stop
// and upload, the review timer, the send) by comparing the phase before and after each event.
//
// Gestures: holding the mic button (or the space bar) records while held and sends on release.
// On the button (a pointer), a press shorter than TAP_MS is a tap, which locks recording on and
// the next press stops it: a phone has no comfortable way to hold for a minute. On the keyboard a
// short press is never a tap-lock: a quick Space was locking the microphone on by accident (found
// driving the real app, see the voice-fix report), so it just does nothing. Either way a
// recording shorter than MIN_CLIP_MS is not sent: it can only be a slip. After the recording is
// transcribed the text is shown for REVIEW_MS with Cancel before it is sent, so a mis-hearing can
// be stopped.

/** A pointer press shorter than this is a tap (lock on), not a hold; a key press, an accident. */
export const TAP_MS = 300;
/** A recording shorter than this is thrown away rather than sent. */
export const MIN_CLIP_MS = 500;
/** How long the transcribed text is shown, with Cancel, before it is sent. */
export const REVIEW_MS = 1500;
/** The recording stops by itself (and is sent) at this length: five minutes. The server's
 * voice.MaxRecordingSeconds and upload cap match it. */
export const MAX_RECORDING_MS = 300_000;
/** The recording indicator warns this long before the cap. */
export const LIMIT_WARNING_MS = 30_000;

export type PttMode = "hold" | "lock";
/** What pressed: the mic button (pointer) or a key (Space, or the composer's chord). */
export type PttSource = "pointer" | "key";

export type PttState =
  | { phase: "idle" }
  /** Waiting for the microphone (the permission prompt, or the device starting). */
  | { phase: "starting"; mode: PttMode; source: PttSource; pressedAt: number }
  | { phase: "recording"; mode: PttMode; source: PttSource; pressedAt: number; startedAt: number }
  | { phase: "uploading" }
  | { phase: "review"; text: string }
  | { phase: "sending"; text: string }
  | { phase: "error"; message: string };

export type PttEvent =
  | { type: "press"; at: number; source?: PttSource }
  | { type: "release"; at: number }
  | { type: "micReady"; at: number }
  | { type: "micFailed"; message: string }
  | { type: "limit" }
  | { type: "cancel" }
  | { type: "transcribed"; text: string }
  | { type: "uploadFailed"; message: string }
  | { type: "reviewDone" }
  | { type: "sendNow" }
  | { type: "sent" }
  | { type: "sendFailed"; message: string }
  | { type: "dismiss" };

export const initialPtt: PttState = { phase: "idle" };

/** The next state. Events that do not apply to the current phase leave it unchanged. */
export function pttReducer(state: PttState, event: PttEvent): PttState {
  switch (event.type) {
    case "press":
      if (state.phase === "idle" || state.phase === "error") return { phase: "starting", mode: "hold", source: event.source ?? "pointer", pressedAt: event.at };
      // A press while locked on is the press that stops it.
      if (state.phase === "recording" && state.mode === "lock") return finish(state, event.at);
      return state;
    case "release":
      return release(state, event.at);
    case "micReady":
      return state.phase === "starting" ? { phase: "recording", mode: state.mode, source: state.source, pressedAt: state.pressedAt, startedAt: event.at } : state;
    case "micFailed":
      return state.phase === "starting" ? { phase: "error", message: event.message } : state;
    case "limit":
      return state.phase === "recording" ? { phase: "uploading" } : state;
    case "cancel":
      return state.phase === "starting" || state.phase === "recording" || state.phase === "review" ? initialPtt : state;
    case "transcribed":
      if (state.phase !== "uploading") return state;
      return event.text.trim() ? { phase: "review", text: event.text.trim() } : { phase: "error", message: "Didn't catch that. Hold the button and speak again." };
    case "uploadFailed":
      return state.phase === "uploading" ? { phase: "error", message: event.message } : state;
    case "reviewDone":
    case "sendNow":
      return state.phase === "review" ? { phase: "sending", text: state.text } : state;
    case "sent":
      return state.phase === "sending" ? initialPtt : state;
    case "sendFailed":
      return state.phase === "sending" ? { phase: "error", message: event.message } : state;
    case "dismiss":
      return state.phase === "error" ? initialPtt : state;
  }
}

/** The hint for a key press too short to be a hold. */
export const SHORT_KEY_HINT = "Hold the key down while you speak, then let go to send.";
/** The hint for a recording too short to send. */
export const SHORT_CLIP_HINT = "That was too short to send. Hold while you speak.";

/**
 * Releasing a hold sends; releasing a pointer tap locks recording on; a short key press does
 * nothing; a locked recording ignores a release.
 */
function release(state: PttState, at: number): PttState {
  if ((state.phase !== "starting" && state.phase !== "recording") || state.mode !== "hold") return state;
  const tap = at - state.pressedAt < TAP_MS;
  if (tap && state.source === "pointer") return { ...state, mode: "lock" };
  // A key let go before the microphone was even recording (a tap, or a short hold while the
  // device started, which can take half a second in WebKit): nothing was recorded, and saying so
  // beats going quietly back to idle.
  if (state.source === "key" && (tap || state.phase === "starting")) return { phase: "error", message: SHORT_KEY_HINT };
  // Let go of the button after a long hold before the microphone even started: nothing recorded.
  if (state.phase === "starting") return initialPtt;
  return finish(state, at);
}

/** Stopping a recording: upload it, unless it is too short to hold any speech. */
function finish(state: Extract<PttState, { phase: "recording" }>, at: number): PttState {
  return at - state.startedAt < MIN_CLIP_MS ? { phase: "error", message: SHORT_CLIP_HINT } : { phase: "uploading" };
}

/** Side effects of a transition, for the hook to run. */
export type PttEffect = "startMic" | "stopAndUpload" | "discardMic" | "startReview" | "send";

/** The effects of moving from prev to next. */
export function pttEffects(prev: PttState, next: PttState): PttEffect[] {
  if (prev.phase === next.phase) return [];
  const micOpen = prev.phase === "starting" || prev.phase === "recording";
  const effects: PttEffect[] = [];
  if (next.phase === "starting") effects.push("startMic");
  if (micOpen && next.phase === "uploading") effects.push("stopAndUpload");
  if (micOpen && (next.phase === "idle" || next.phase === "error")) effects.push("discardMic");
  if (next.phase === "review") effects.push("startReview");
  if (next.phase === "sending") effects.push("send");
  return effects;
}

/** Whether the recording indicator should warn that the cap is near. */
export function nearLimit(elapsedMs: number): boolean {
  return elapsedMs >= MAX_RECORDING_MS - LIMIT_WARNING_MS;
}

/** "m:ss" for the recording indicator. */
export function formatElapsed(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
