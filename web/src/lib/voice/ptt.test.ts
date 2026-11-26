import { describe, expect, it } from "vitest";
import {
  MAX_RECORDING_MS,
  MIN_CLIP_MS,
  SHORT_CLIP_HINT,
  SHORT_KEY_HINT,
  TAP_MS,
  formatElapsed,
  initialPtt,
  nearLimit,
  pttEffects,
  pttReducer,
  type PttEvent,
  type PttState,
} from "./ptt";

/** Runs events from idle and returns every state along the way. */
function run(...events: PttEvent[]): PttState[] {
  const states: PttState[] = [];
  let state = initialPtt;
  for (const event of events) {
    state = pttReducer(state, event);
    states.push(state);
  }
  return states;
}

const last = (states: PttState[]): PttState => states[states.length - 1] ?? initialPtt;

describe("push-to-talk state machine", () => {
  it("hold to talk: press, mic ready, release after a hold → upload → review → send", () => {
    const states = run(
      { type: "press", at: 0 },
      { type: "micReady", at: 50 },
      { type: "release", at: 2000 },
      { type: "transcribed", text: "  deploy the docs  " },
      { type: "reviewDone" },
      { type: "sent" },
    );
    expect(states.map((state) => state.phase)).toEqual(["starting", "recording", "uploading", "review", "sending", "idle"]);
    expect(states[3]).toEqual({ phase: "review", text: "deploy the docs" });
  });

  it("a tap locks recording on, and the next press stops it", () => {
    const states = run({ type: "press", at: 0 }, { type: "micReady", at: 40 }, { type: "release", at: TAP_MS - 1 });
    expect(last(states)).toMatchObject({ phase: "recording", mode: "lock" });
    // A release while locked does nothing; the next press uploads.
    let state = pttReducer(last(states), { type: "release", at: 5000 });
    expect(state).toMatchObject({ phase: "recording", mode: "lock" });
    state = pttReducer(state, { type: "press", at: 6000 });
    expect(state).toEqual({ phase: "uploading" });
  });

  it("a tap before the mic is ready also locks; the mic arriving keeps the lock", () => {
    const states = run({ type: "press", at: 0 }, { type: "release", at: 100 }, { type: "micReady", at: 800 });
    expect(last(states)).toMatchObject({ phase: "recording", mode: "lock", startedAt: 800 });
  });

  it("letting go of a long hold before the mic started records nothing", () => {
    expect(last(run({ type: "press", at: 0 }, { type: "release", at: 3000 }))).toEqual(initialPtt);
  });

  // The hold-space bugs found driving the real app: a quick Space locked the mic on (Chromium),
  // and a quick Space before the mic was ready went on to send a clip (WebKit).
  it("a short key press never locks recording on, before or after the mic is ready", () => {
    const after = run({ type: "press", at: 0, source: "key" }, { type: "micReady", at: 40 }, { type: "release", at: 100 });
    expect(last(after)).toEqual({ phase: "error", message: SHORT_KEY_HINT });
    expect(pttEffects(after[1]!, after[2]!)).toEqual(["discardMic"]);
    const before = run({ type: "press", at: 0, source: "key" }, { type: "release", at: 100 }, { type: "micReady", at: 800 });
    expect(before.map((state) => state.phase)).toEqual(["starting", "error", "error"]);
  });

  it("a key held long enough records while held and sends on release", () => {
    const states = run({ type: "press", at: 0, source: "key" }, { type: "micReady", at: 50 }, { type: "release", at: 30_000 });
    expect(last(states)).toEqual({ phase: "uploading" });
  });

  it("a recording too short to hold speech is thrown away, not sent", () => {
    // A key hold past TAP_MS whose mic only just started.
    const held = run({ type: "press", at: 0, source: "key" }, { type: "micReady", at: 350 }, { type: "release", at: 400 });
    expect(last(held)).toEqual({ phase: "error", message: SHORT_CLIP_HINT });
    expect(pttEffects(held[1]!, held[2]!)).toEqual(["discardMic"]);
    // A pointer tap-lock stopped straight away.
    const locked = run({ type: "press", at: 0 }, { type: "micReady", at: 10 }, { type: "release", at: 50 }, { type: "press", at: 10 + MIN_CLIP_MS - 1 });
    expect(last(locked)).toEqual({ phase: "error", message: SHORT_CLIP_HINT });
  });

  it("warns in the last 30 seconds of the five minutes", () => {
    expect(MAX_RECORDING_MS).toBe(300_000);
    expect(nearLimit(269_000)).toBe(false);
    expect(nearLimit(270_000)).toBe(true);
  });

  it("the five-minute limit stops a recording and uploads it", () => {
    expect(last(run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "limit" }))).toEqual({ phase: "uploading" });
  });

  it("cancel works while starting, recording and in review, not while uploading", () => {
    expect(last(run({ type: "press", at: 0 }, { type: "cancel" }))).toEqual(initialPtt);
    expect(last(run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "cancel" }))).toEqual(initialPtt);
    expect(last(run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "release", at: 900 }, { type: "transcribed", text: "x" }, { type: "cancel" }))).toEqual(initialPtt);
    expect(last(run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "release", at: 900 }, { type: "cancel" }))).toEqual({ phase: "uploading" });
  });

  it("errors: mic refused, nothing heard, upload or send failed; dismiss or press again", () => {
    expect(last(run({ type: "press", at: 0 }, { type: "micFailed", message: "blocked" }))).toEqual({ phase: "error", message: "blocked" });
    const empty = last(run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "release", at: 900 }, { type: "transcribed", text: "   " }));
    expect(empty.phase).toBe("error");
    expect(last(run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "release", at: 900 }, { type: "uploadFailed", message: "502" }))).toEqual({ phase: "error", message: "502" });
    const sendFailed = last(
      run({ type: "press", at: 0 }, { type: "micReady", at: 1 }, { type: "release", at: 900 }, { type: "transcribed", text: "x" }, { type: "sendNow" }, { type: "sendFailed", message: "busy" }),
    );
    expect(sendFailed).toEqual({ phase: "error", message: "busy" });
    expect(pttReducer(sendFailed, { type: "dismiss" })).toEqual(initialPtt);
    expect(pttReducer(sendFailed, { type: "press", at: 10 }).phase).toBe("starting");
  });

  it("ignores events that do not apply", () => {
    expect(pttReducer(initialPtt, { type: "release", at: 1 })).toBe(initialPtt);
    expect(pttReducer(initialPtt, { type: "transcribed", text: "x" })).toBe(initialPtt);
    expect(pttReducer({ phase: "uploading" }, { type: "press", at: 1 })).toEqual({ phase: "uploading" });
  });

  it("names the side effects of each transition", () => {
    const starting: PttState = { phase: "starting", mode: "hold", source: "pointer", pressedAt: 0 };
    const recording = { phase: "recording", mode: "hold", source: "pointer", pressedAt: 0, startedAt: 1 } as const;
    expect(pttEffects(initialPtt, starting)).toEqual(["startMic"]);
    expect(pttEffects(recording, { phase: "uploading" })).toEqual(["stopAndUpload"]);
    expect(pttEffects(recording, initialPtt)).toEqual(["discardMic"]);
    expect(pttEffects(starting, { phase: "error", message: "x" })).toEqual(["discardMic"]);
    expect(pttEffects({ phase: "uploading" }, { phase: "review", text: "x" })).toEqual(["startReview"]);
    expect(pttEffects({ phase: "review", text: "x" }, { phase: "sending", text: "x" })).toEqual(["send"]);
    expect(pttEffects(recording, { ...recording, mode: "lock" })).toEqual([]);
  });

  it("formats elapsed time", () => {
    expect(formatElapsed(0)).toBe("0:00");
    expect(formatElapsed(9_900)).toBe("0:09");
    expect(formatElapsed(60_000)).toBe("1:00");
  });
});
