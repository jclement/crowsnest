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
//
// Per-agent speaking (Jeff's decision): each standing agent (the orchestrator, each project's
// lead) has its own "speak replies" switch, off by default, remembered per browser
// (useAgentSpeak, in that agent's Chat header and the Voice sheet). The header toggle above is
// the master mute: a line is spoken only when both are on (lib/voice/queue.ts wantsLine). Every
// spoken line goes through the player's one queue, so two agents never talk at once.
import { useSyncExternalStore } from "react";
import { player } from "./player";
import { agentKey, wantsLine } from "./queue";

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

// ---- Each agent's own switch ----------------------------------------------------------------

const AGENTS_KEY = "crowsnest-speak-agents";

let agents: Record<string, boolean> = readAgents();
const agentListeners = new Set<() => void>();

function readAgents(): Record<string, boolean> {
  try {
    const parsed: unknown = JSON.parse((typeof localStorage !== "undefined" && localStorage.getItem(AGENTS_KEY)) || "{}");
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => value === true)) as Record<string, boolean>;
  } catch {
    return {};
  }
}

/** Whether an agent's replies are spoken here (its own switch; the master is separate). */
export function agentSpeakOn(agent: string): boolean {
  return agents[agentKey(agent)] === true;
}

/**
 * Turns one agent's spoken replies on or off in this browser. Turning one on from a click also
 * turns the master on (and so unlocks audio inside the gesture): the human who asks to hear the
 * lead wants to hear it now, not to find a second switch.
 */
export function setAgentSpeak(agent: string, next: boolean): void {
  const key = agentKey(agent);
  if (next) setSpeakMode(true);
  if ((agents[key] === true) === next) return;
  const updated = { ...agents };
  if (next) updated[key] = true;
  else delete updated[key];
  agents = updated;
  try {
    localStorage.setItem(AGENTS_KEY, JSON.stringify(agents));
  } catch {
    // Not remembered; it still works for this page.
  }
  agentListeners.forEach((listener) => listener());
}

/** One agent's switch for a component: [on, set]. */
export function useAgentSpeak(agent: string): [boolean, (next: boolean) => void] {
  const value = useSyncExternalStore(
    (listener) => {
      agentListeners.add(listener);
      return () => agentListeners.delete(listener);
    },
    () => agentSpeakOn(agent),
    () => false,
  );
  return [value, (next: boolean) => setAgentSpeak(agent, next)];
}

/** A `say` event from the server: what to play, if this browser wants to hear that agent. */
export interface SayEvent {
  kind?: "speak" | "chime";
  source?: string;
  text_id?: string;
  text?: string;
  pane_id?: string;
  /** "orchestrator", "lead-<slug>", or "" for a job agent's `say`. */
  agent?: string;
}

/** Handles a `say` event: queue it (or chime) when the master and that agent's switch are on. */
export function onSayEvent(data: unknown): void {
  if (!data || typeof data !== "object") return;
  const event = data as SayEvent;
  if (!wantsLine(event.agent, { master: on, agents })) return;
  if (event.kind === "speak" && event.text_id) player.enqueue({ textId: event.text_id, text: event.text ?? "", agent: event.agent ?? "" });
  else if (event.kind === "chime" && !player.getSnapshot().current) player.chime();
}

/** Test hook: forget the remembered values and turn everything off without touching the player. */
export function resetSpeakModeForTests(): void {
  on = false;
  agents = {};
  try {
    localStorage.removeItem(STORAGE_KEY);
    localStorage.removeItem(AGENTS_KEY);
  } catch {
    // ignore
  }
  listeners.forEach((listener) => listener());
  agentListeners.forEach((listener) => listener());
}
