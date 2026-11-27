// Earcons (R4 Fable 4.1, Codex V2): short, distinct sounds so Jeff can follow what happened
// without looking: heard (his speech was transcribed), sent (a job or answer went out), received
// (an agent's line arrived), needs you (something new joined the queue) and error. Generated with
// an AudioContext (no files, nothing the CSP has to allow); each is under half a second, quiet,
// and plays only after a user gesture has unlocked audio (the browser's rule), which the mic press
// or the speak-mode switch always is.

export type Earcon = "heard" | "sent" | "received" | "needs-you" | "error";

/** Each earcon as notes: frequency in Hz and length in seconds, played one after another. */
export const EARCON_NOTES: Record<Earcon, { hz: number; seconds: number }[]> = {
  heard: [
    { hz: 660, seconds: 0.07 },
    { hz: 880, seconds: 0.09 },
  ],
  sent: [{ hz: 990, seconds: 0.12 }],
  received: [{ hz: 520, seconds: 0.1 }],
  "needs-you": [
    { hz: 740, seconds: 0.08 },
    { hz: 740, seconds: 0.08 },
    { hz: 988, seconds: 0.14 },
  ],
  error: [
    { hz: 330, seconds: 0.12 },
    { hz: 247, seconds: 0.18 },
  ],
};

/** Quiet: an earcon should never be louder than speech. */
const GAIN = 0.06;
/** A small gap between notes so a repeat reads as two notes. */
const GAP_SECONDS = 0.03;

let context: AudioContext | null = null;

/** Plays an earcon; does nothing where audio is unavailable. */
export function playEarcon(kind: Earcon): void {
  const Context = typeof window === "undefined" ? undefined : (window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext);
  if (!Context) return;
  try {
    context ??= new Context();
    if (context.state === "suspended") void context.resume();
    let at = context.currentTime;
    for (const note of EARCON_NOTES[kind]) {
      const oscillator = context.createOscillator();
      const gain = context.createGain();
      oscillator.frequency.value = note.hz;
      gain.gain.setValueAtTime(GAIN, at);
      gain.gain.exponentialRampToValueAtTime(0.0001, at + note.seconds);
      oscillator.connect(gain).connect(context.destination);
      oscillator.start(at);
      oscillator.stop(at + note.seconds);
      at += note.seconds + GAP_SECONDS;
    }
  } catch {
    // Audio refused (no gesture yet, a closed context): an earcon is a nicety, never an error.
  }
}

/**
 * Reads a short sentence aloud with the browser's own speech (never ElevenLabs: this text is
 * crowsnest's readback of what Jeff said, not an agent's line). Returns false where the browser
 * has none. Speaking stops anything it was saying, so a new readback barges in on an old one.
 */
export function speakLocally(text: string): boolean {
  const synth = typeof window === "undefined" ? undefined : window.speechSynthesis;
  if (!synth || typeof SpeechSynthesisUtterance === "undefined") return false;
  synth.cancel();
  synth.speak(new SpeechSynthesisUtterance(text));
  return true;
}

/** Stops the browser's own speech (pressing the mic again is the cheap barge-in). */
export function stopLocalSpeech(): void {
  if (typeof window !== "undefined") window.speechSynthesis?.cancel();
}
