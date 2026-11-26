// The speech player: one queue of server-held texts to play, in order, through one <audio>
// element, plus the short local chime for a reply with nothing to say aloud.
//
// Autoplay policy: browsers (Safari above all) let a page start audio only from a user gesture,
// and an <audio> element that has played once inside a gesture may play again later without one.
// So unlock() must be called synchronously inside a click (the talk-mode toggle, "Read aloud"):
// it plays a moment of generated silence through the element and resumes the AudioContext the
// chime uses. If a later play is still refused, the player says so ("blocked") and the next tap
// on its Play button unlocks it again.
import { useSyncExternalStore } from "react";
import { speechUrl } from "../api";

export interface SpeechItem {
  textId: string;
  /** The sentence, shown as the caption while it plays. */
  text: string;
}

export interface PlayerSnapshot {
  current: SpeechItem | null;
  queued: number;
  /** The browser refused to start audio without a gesture; a tap on Play fixes it. */
  blocked: boolean;
  /** The last item could not be played (the server or the network failed it). */
  failed: boolean;
}

/** At most this many items wait; older ones are dropped rather than played minutes late. */
const MAX_QUEUE = 8;

export class SpeechPlayer {
  private audio: HTMLAudioElement | null = null;
  private context: AudioContext | null = null;
  private queue: SpeechItem[] = [];
  private snapshot: PlayerSnapshot = { current: null, queued: 0, blocked: false, failed: false };
  private readonly listeners = new Set<() => void>();
  private silentUrl: string | null = null;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = () => this.snapshot;

  /** Call inside a user gesture: lets later plays start without one. */
  unlock(): void {
    const audio = this.element();
    if (!this.snapshot.current) {
      audio.src = this.silence();
      void audio.play().catch(() => {});
    }
    try {
      this.context ??= new AudioContext();
      void this.context.resume().catch(() => {});
    } catch {
      // No Web Audio: the chime stays silent, speech still plays.
    }
    if (this.snapshot.blocked) {
      this.update({ blocked: false });
      if (this.snapshot.current) void this.start(this.snapshot.current);
    }
  }

  /** Adds an item to the queue and starts playing if idle. */
  enqueue(item: SpeechItem): void {
    this.queue.push(item);
    if (this.queue.length > MAX_QUEUE) this.queue.splice(0, this.queue.length - MAX_QUEUE);
    if (this.snapshot.current) {
      this.update({ queued: this.queue.length });
      return;
    }
    this.next();
  }

  /** Plays item now, ahead of anything queued (Read aloud). */
  playNow(item: SpeechItem): void {
    this.queue.unshift(item);
    this.audio?.pause();
    this.next();
  }

  /** Stops what is playing and clears the queue. */
  stop(): void {
    this.queue = [];
    if (this.audio) {
      this.audio.pause();
      this.audio.removeAttribute("src");
      this.audio.load();
    }
    this.update({ current: null, queued: 0, blocked: false });
  }

  /** Two soft notes: a reply arrived with no SAY line. */
  chime(): void {
    const context = this.context;
    if (!context || context.state !== "running") return;
    const now = context.currentTime;
    [660, 880].forEach((frequency, i) => {
      const oscillator = context.createOscillator();
      const gain = context.createGain();
      oscillator.frequency.value = frequency;
      gain.gain.setValueAtTime(0.0001, now + i * 0.12);
      gain.gain.exponentialRampToValueAtTime(0.12, now + i * 0.12 + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.0001, now + i * 0.12 + 0.25);
      oscillator.connect(gain).connect(context.destination);
      oscillator.start(now + i * 0.12);
      oscillator.stop(now + i * 0.12 + 0.3);
    });
  }

  private next(): void {
    const item = this.queue.shift() ?? null;
    this.update({ current: item, queued: this.queue.length, failed: false });
    if (item) void this.start(item);
  }

  private async start(item: SpeechItem): Promise<void> {
    const audio = this.element();
    audio.src = speechUrl(item.textId);
    try {
      await audio.play();
    } catch (error) {
      if (this.snapshot.current !== item) return; // stopped or replaced meanwhile
      if (error instanceof DOMException && error.name === "NotAllowedError") {
        this.update({ blocked: true });
        return;
      }
      // A load failure also fires `error`, which moves on; nothing more to do here.
    }
  }

  /** The one audio element, in the document (hidden) so tests can find it. */
  private element(): HTMLAudioElement {
    if (this.audio) return this.audio;
    const audio = document.createElement("audio");
    audio.preload = "auto";
    audio.hidden = true;
    audio.dataset.testid = "speech-audio";
    audio.addEventListener("ended", () => {
      if (this.snapshot.current) this.next();
    });
    audio.addEventListener("error", () => {
      if (!this.snapshot.current) return; // the src was cleared by stop()
      this.update({ failed: true });
      this.next();
    });
    document.body.appendChild(audio);
    this.audio = audio;
    return audio;
  }

  /** 50 ms of silent 8 kHz mono WAV, as a blob URL (the CSP allows blob: media, not data:). */
  private silence(): string {
    if (this.silentUrl) return this.silentUrl;
    const samples = 400;
    const buffer = new ArrayBuffer(44 + samples * 2);
    const view = new DataView(buffer);
    const text = (offset: number, value: string) => [...value].forEach((c, i) => view.setUint8(offset + i, c.charCodeAt(0)));
    text(0, "RIFF");
    view.setUint32(4, 36 + samples * 2, true);
    text(8, "WAVEfmt ");
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true); // PCM
    view.setUint16(22, 1, true); // mono
    view.setUint32(24, 8000, true);
    view.setUint32(28, 16000, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    text(36, "data");
    view.setUint32(40, samples * 2, true);
    this.silentUrl = URL.createObjectURL(new Blob([buffer], { type: "audio/wav" }));
    return this.silentUrl;
  }

  private update(patch: Partial<PlayerSnapshot>): void {
    this.snapshot = { ...this.snapshot, ...patch };
    this.listeners.forEach((listener) => listener());
  }
}

/** The app's one player. */
export const player = new SpeechPlayer();

/** The player's state, for components. */
export function usePlayer(): PlayerSnapshot {
  return useSyncExternalStore(player.subscribe, player.getSnapshot, player.getSnapshot);
}
