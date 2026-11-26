// Recording: a thin wrapper over getUserMedia and MediaRecorder, and the usePushToTalk hook that
// drives it from the state machine in ptt.ts. The microphone is opened for each recording and
// released straight after, so the browser's "recording" indicator is only on while it records.
import { useEffect, useRef, useState } from "react";
import { endpoints } from "../api";
import { MAX_RECORDING_MS, REVIEW_MS, initialPtt, pttEffects, pttReducer, type PttEffect, type PttEvent, type PttState } from "./ptt";
import { micErrorMessage, recordingType } from "./support";

/** Bit rate asked of the recorder: plenty for speech, and a minute stays far under the upload cap. */
const AUDIO_BITS_PER_SECOND = 64_000;
/** MediaRecorder hands over data in slices this long, so a stop loses at most this much. */
const TIMESLICE_MS = 250;

/** One open microphone and its recorder. */
export class Recording {
  private readonly chunks: Blob[] = [];
  private constructor(
    private readonly stream: MediaStream,
    private readonly recorder: MediaRecorder,
  ) {
    recorder.addEventListener("dataavailable", (event) => {
      if (event.data.size > 0) this.chunks.push(event.data);
    });
  }

  /** Opens the microphone and starts recording. Rejects with the browser's error. */
  static async start(): Promise<Recording> {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } });
    try {
      const type = recordingType();
      const recorder = new MediaRecorder(stream, { ...(type ? { mimeType: type } : {}), audioBitsPerSecond: AUDIO_BITS_PER_SECOND });
      const recording = new Recording(stream, recorder);
      recorder.start(TIMESLICE_MS);
      return recording;
    } catch (error) {
      stream.getTracks().forEach((track) => track.stop());
      throw error;
    }
  }

  /** Stops recording and resolves with the audio, typed as the recorder made it. */
  stop(): Promise<Blob> {
    return new Promise((resolve) => {
      const finish = () => {
        this.release();
        const type = this.recorder.mimeType || this.chunks[0]?.type || "audio/webm";
        resolve(new Blob(this.chunks, { type }));
      };
      if (this.recorder.state === "inactive") return finish();
      this.recorder.addEventListener("stop", finish, { once: true });
      this.recorder.stop();
    });
  }

  /** Stops and throws the audio away. */
  discard(): void {
    if (this.recorder.state !== "inactive") this.recorder.stop();
    this.release();
  }

  private release() {
    this.stream.getTracks().forEach((track) => track.stop());
  }
}

/**
 * Runs the push-to-talk state machine against a real recorder: each event goes through the pure
 * reducer, and the effects of the transition (open the microphone, stop and upload, the review
 * timer, the send) run here, outside React. `send` receives what was said once the review delay
 * passed (or Send now) and resolves, or rejects with a message to show.
 */
export class PushToTalkController {
  state: PttState = initialPtt;
  private recording: Recording | null = null;
  private reviewTimer: number | undefined;
  private limitTimer: number | undefined;

  constructor(
    private send: (text: string) => Promise<void>,
    private readonly reviewMs: number,
    private readonly onChange: (state: PttState) => void,
  ) {}

  /** Replaces the send callback (the hook's latest closure). */
  setSend(send: (text: string) => Promise<void>) {
    this.send = send;
  }

  dispatch = (event: PttEvent) => {
    const prev = this.state;
    const next = pttReducer(prev, event);
    if (next === prev) return;
    this.state = next;
    this.onChange(next);
    for (const effect of pttEffects(prev, next)) this.run(effect, next);
  };

  press = () => this.dispatch({ type: "press", at: performance.now() });
  release = () => this.dispatch({ type: "release", at: performance.now() });
  cancel = () => {
    window.clearTimeout(this.reviewTimer);
    this.dispatch({ type: "cancel" });
  };
  sendNow = () => {
    window.clearTimeout(this.reviewTimer);
    this.dispatch({ type: "sendNow" });
  };
  dismiss = () => this.dispatch({ type: "dismiss" });

  /**
   * Releases the microphone and timers and returns to idle (the component unmounted). Not final:
   * React's development double-mount reuses the controller afterwards.
   */
  dispose() {
    window.clearTimeout(this.reviewTimer);
    this.closeMic()?.discard();
    this.state = initialPtt;
  }

  private run(effect: PttEffect, next: PttState) {
    switch (effect) {
      case "startMic":
        return this.openMic();
      case "discardMic":
        return this.closeMic()?.discard();
      case "stopAndUpload":
        return this.upload();
      case "startReview":
        window.clearTimeout(this.reviewTimer);
        this.reviewTimer = window.setTimeout(() => this.dispatch({ type: "reviewDone" }), this.reviewMs);
        return;
      case "send":
        if (next.phase !== "sending") return;
        this.send(next.text).then(
          () => this.dispatch({ type: "sent" }),
          (error: unknown) => this.dispatch({ type: "sendFailed", message: errorText(error, "Not sent.") }),
        );
    }
  }

  private openMic() {
    Recording.start().then(
      (opened) => {
        if (this.state.phase !== "starting") {
          opened.discard(); // cancelled, or let go, while the microphone was starting
          return;
        }
        this.recording = opened;
        this.limitTimer = window.setTimeout(() => this.dispatch({ type: "limit" }), MAX_RECORDING_MS);
        this.dispatch({ type: "micReady", at: performance.now() });
      },
      (error: unknown) => this.dispatch({ type: "micFailed", message: micErrorMessage(error) }),
    );
  }

  private upload() {
    const current = this.closeMic();
    if (!current) return;
    void current
      .stop()
      .then((audio) => endpoints.transcribe(audio))
      .then(
        (result) => this.dispatch({ type: "transcribed", text: result.text }),
        (error: unknown) => this.dispatch({ type: "uploadFailed", message: errorText(error, "Could not transcribe that.") }),
      );
  }

  private closeMic(): Recording | null {
    window.clearTimeout(this.limitTimer);
    const current = this.recording;
    this.recording = null;
    return current;
  }
}

function errorText(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

export interface PushToTalk {
  state: PttState;
  press: () => void;
  release: () => void;
  cancel: () => void;
  sendNow: () => void;
  dismiss: () => void;
}

/**
 * Push-to-talk for a component. With `review: false` (dictation into a field) the text is handed
 * over at once instead of after the 1.5 s review.
 */
export function usePushToTalk(send: (text: string) => Promise<void>, options: { review?: boolean } = {}): PushToTalk {
  const reviewMs = options.review === false ? 0 : REVIEW_MS;
  const [state, setState] = useState<PttState>(initialPtt);
  const [controller] = useState(() => new PushToTalkController(send, reviewMs, setState));
  const latestSend = useRef(send);
  useEffect(() => {
    latestSend.current = send;
    controller.setSend((text) => latestSend.current(text));
  }, [controller, send]);
  useEffect(() => () => controller.dispose(), [controller]);
  return { state, press: controller.press, release: controller.release, cancel: controller.cancel, sendNow: controller.sendNow, dismiss: controller.dismiss };
}
