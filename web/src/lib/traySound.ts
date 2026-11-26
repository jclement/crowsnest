// The tray's optional arrival sound (DESIGN.md 8.7): off by default, switched on in the tray and
// remembered in this browser. A short two-note chime made with Web Audio (no file to load), and
// only when an approval or question arrives, never on load.
const KEY = "crowsnest-tray-sound";

let listeners: (() => void)[] = [];

export function traySoundOn(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

export function setTraySound(on: boolean): void {
  try {
    if (on) localStorage.setItem(KEY, "1");
    else localStorage.removeItem(KEY);
  } catch {
    // private mode: the switch lasts for this page only
  }
}

/** Calls listener when something arrives (the tray animates); returns an unsubscribe. */
export function onArrival(listener: () => void): () => void {
  listeners.push(listener);
  return () => {
    listeners = listeners.filter((entry) => entry !== listener);
  };
}

/** Something that needs the human arrived: tell the tray, and chime when the sound is on. */
export function onTrayArrival(): void {
  for (const listener of listeners) listener();
  if (traySoundOn()) chime();
}

function chime(): void {
  try {
    const AudioContextClass = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AudioContextClass) return;
    const context = new AudioContextClass();
    const gain = context.createGain();
    gain.connect(context.destination);
    gain.gain.setValueAtTime(0.0001, context.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.08, context.currentTime + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, context.currentTime + 0.35);
    for (const [index, frequency] of [660, 880].entries()) {
      const oscillator = context.createOscillator();
      oscillator.type = "sine";
      oscillator.frequency.value = frequency;
      oscillator.connect(gain);
      oscillator.start(context.currentTime + index * 0.12);
      oscillator.stop(context.currentTime + 0.35);
    }
    window.setTimeout(() => void context.close(), 600);
  } catch {
    // no audio: the animation is enough
  }
}
