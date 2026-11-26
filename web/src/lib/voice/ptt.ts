// The push-to-talk state machine (DESIGN.md 13), as a pure reducer so every transition is tested
// without a browser. The hook in recorder.ts runs the side effects (start the microphone, stop
// and upload, the review timer, the send) by comparing the phase before and after each event.
//
// Gestures: holding the button (or the space bar) records while held and sends on release. A
// press shorter than TAP_MS is a tap, which locks recording on; the next press stops it. After
// the recording is transcribed the text is shown for REVIEW_MS with Cancel before it is sent, so
// a mis-hearing can be stopped.

/** A press shorter than this is a tap (lock on), not a hold. */
export const TAP_MS = 300;
/** How long the transcribed text is shown, with Cancel, before it is sent. */
export const REVIEW_MS = 1500;
/** The recording stops by itself at this length (the server caps uploads to match). */
export const MAX_RECORDING_MS = 60_000;

export type PttMode = "hold" | "lock";

export type PttState =
  | { phase: "idle" }
  /** Waiting for the microphone (the permission prompt, or the device starting). */
  | { phase: "starting"; mode: PttMode; pressedAt: number }
  | { phase: "recording"; mode: PttMode; pressedAt: number; startedAt: number }
  | { phase: "uploading" }
  | { phase: "review"; text: string }
  | { phase: "sending"; text: string }
  | { phase: "error"; message: string };

export type PttEvent =
  | { type: "press"; at: number }
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
      if (state.phase === "idle" || state.phase === "error") return { phase: "starting", mode: "hold", pressedAt: event.at };
      // A press while locked on is the press that stops it.
      if (state.phase === "recording" && state.mode === "lock") return { phase: "uploading" };
      return state;
    case "release":
      return release(state, event.at);
    case "micReady":
      return state.phase === "starting" ? { phase: "recording", mode: state.mode, pressedAt: state.pressedAt, startedAt: event.at } : state;
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

/** Releasing a hold sends; releasing a tap locks recording on; a locked recording ignores it. */
function release(state: PttState, at: number): PttState {
  if ((state.phase !== "starting" && state.phase !== "recording") || state.mode !== "hold") return state;
  const tap = at - state.pressedAt < TAP_MS;
  if (tap) return { ...state, mode: "lock" };
  // Let go after a long hold before the microphone even started: nothing was recorded.
  if (state.phase === "starting") return initialPtt;
  return { phase: "uploading" };
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

/** "m:ss" for the recording indicator. */
export function formatElapsed(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
