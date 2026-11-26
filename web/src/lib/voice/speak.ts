// Speak mode: whether this browser plays spoken replies (DESIGN.md 13.2). One switch for the whole
// app, in the header on every view, remembered per browser (localStorage). It is separate from
// the microphone: push to talk works with speak mode off, and speak mode needs no microphone.
//
// The orchestrator writes a SAY line on every reply whatever this says; the server announces each
// one as a `say` event, and only a browser with speak mode on fetches its audio. So with speak
// mode off everywhere, nothing is ever synthesised.
//
// This module is the one place speak mode lives. Anything that wants to read or flip it (the
// header toggle, the Cmd+K palette) calls useSpeakMode() or setSpeakMode().
import { useSyncExternalStore } from "react";
import { player } from "./player";

const STORAGE_KEY = "crowsnest-speak-mode";

let on = readStored();
const listeners = new Set<() => void>();

function readStored(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage.getItem(STORAGE_KEY) === "on";
  } catch {
    return false; // storage blocked: off until switched on
  }
}

/**
 * Turns speak mode on or off and remembers it. Call it from a click (or key) handler: switching on
 * unlocks audio playback, which browsers allow only inside a user gesture. Remembered as on, a
 * reload starts with speak mode on but audio still locked; the player then shows Play for the
 * first reply (one tap), as browsers require.
 */
export function setSpeakMode(next: boolean): void {
  if (next) player.unlock();
  if (next === on) return;
  on = next;
  try {
    localStorage.setItem(STORAGE_KEY, on ? "on" : "off");
  } catch {
    // Not remembered; it still works for this page.
  }
  if (!on) player.stop();
  listeners.forEach((listener) => listener());
}

/** Whether speak mode is on in this browser. */
export function speakModeOn(): boolean {
  return on;
}

/** Speak mode for a component: [on, set]. */
export function useSpeakMode(): [boolean, (next: boolean) => void] {
  const value = useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => on,
    () => false,
  );
  return [value, setSpeakMode];
}

/** A `say` event from the server: what to play, if speak mode is on here. */
export interface SayEvent {
  kind?: "speak" | "chime";
  source?: string;
  text_id?: string;
  text?: string;
  pane_id?: string;
}

/** Handles a `say` event: speak it or chime when speak mode is on in this browser, else nothing. */
export function onSayEvent(data: unknown): void {
  if (!on || !data || typeof data !== "object") return;
  const event = data as SayEvent;
  if (event.kind === "speak" && event.text_id) player.enqueue({ textId: event.text_id, text: event.text ?? "" });
  else if (event.kind === "chime") player.chime();
}

/** Test hook: forget the remembered value and turn speak mode off without touching the player. */
export function resetSpeakModeForTests(): void {
  on = false;
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
  listeners.forEach((listener) => listener());
}
