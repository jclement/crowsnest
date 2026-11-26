// Talk mode in this tab: the toggle's state, the heartbeat that tells the server this tab can
// hear (so the orchestrator is told `talk=on`), and what a `say` event does. Talk mode is not
// remembered across reloads on purpose: turning it on is the user gesture the autoplay policy
// needs before anything can be spoken.
import { useSyncExternalStore } from "react";
import { endpoints } from "../api";
import { player } from "./player";

/** Until the server says otherwise (POST /voice/talk answers with its interval). */
const DEFAULT_HEARTBEAT_MS = 15_000;
const CLIENT_ID_KEY = "crowsnest-talk-client";

let on = false;
let heartbeat: number | undefined;
const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((listener) => listener());
}

/** This tab's id for the heartbeat: random, kept for the tab's life. */
export function clientId(): string {
  try {
    const existing = sessionStorage.getItem(CLIENT_ID_KEY);
    if (existing) return existing;
  } catch {
    // Storage blocked: a fresh id per page load is fine.
  }
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  const id = "tab-" + Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  try {
    sessionStorage.setItem(CLIENT_ID_KEY, id);
  } catch {
    // ignore
  }
  return id;
}

function beat() {
  endpoints.talk(clientId(), true).then(
    (status) => schedule(status.heartbeat_seconds * 1000),
    () => schedule(DEFAULT_HEARTBEAT_MS),
  );
}

function schedule(ms: number) {
  window.clearTimeout(heartbeat);
  if (on) heartbeat = window.setTimeout(beat, Math.max(5_000, ms));
}

/**
 * Turns talk mode on or off. Call it straight from the toggle's click handler: switching on
 * unlocks audio playback, which only works inside a user gesture.
 */
export function setTalkMode(next: boolean): void {
  if (next === on) return;
  on = next;
  if (on) {
    player.unlock();
    beat();
  } else {
    window.clearTimeout(heartbeat);
    player.stop();
    void endpoints.talk(clientId(), false).catch(() => {});
  }
  emit();
}

/** Whether talk mode is on in this tab. */
export function talkModeOn(): boolean {
  return on;
}

export function useTalkMode(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => on,
    () => false,
  );
}

/** A `say` event from the server: what to play, if talk mode is on here. */
export interface SayEvent {
  kind?: "speak" | "chime";
  source?: string;
  text_id?: string;
  text?: string;
  pane_id?: string;
}

/** Handles a `say` event: speak it or chime when talk mode is on in this tab, else nothing. */
export function onSayEvent(data: unknown): void {
  if (!on || !data || typeof data !== "object") return;
  const event = data as SayEvent;
  if (event.kind === "speak" && event.text_id) player.enqueue({ textId: event.text_id, text: event.text ?? "" });
  else if (event.kind === "chime") player.chime();
}
