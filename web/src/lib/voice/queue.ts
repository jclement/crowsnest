// The spoken-reply queue (DESIGN.md 13, per-agent voices): pure state, no audio. Every standing
// agent (the orchestrator and each project's lead) can speak, each in its own ElevenLabs voice,
// so several can finish a turn at the same moment. This browser plays them through ONE queue so
// two never talk at once: first come, first served. A line that waited more than STALE_MS for its
// turn is dropped rather than spoken late (a two-minute-old "the build passed" is noise, and the
// caption in Chat still has it). lib/voice/player.ts drives this with an <audio> element; the
// rules live here so they are unit-tested without one.
//
// Which lines are queued at all is decided per agent (wantsLine): speaking is a per-agent opt-in
// (each agent's "speak replies" switch, off by default, per browser), and the header's speak
// toggle is the master mute for this browser.

/** One line to speak. */
export interface SpokenLine {
  /** The server-held text id (GET /voice/tts?text_id=…). */
  textId: string;
  /** The sentence, shown in the now-playing strip. */
  text: string;
  /** Who said it: "orchestrator", "lead-<slug>", or "" for a job agent's `say`. */
  agent: string;
  /** When this browser got it (ms since the epoch): staleness is measured from here. */
  at: number;
  /** A playback rate of its own (a preview); otherwise the saved settings' rate. */
  rate?: number;
}

export interface QueueState {
  current: SpokenLine | null;
  /** Oldest first. */
  waiting: SpokenLine[];
}

/** A line older than this when its turn comes is dropped, not spoken late. */
export const STALE_MS = 2 * 60_000;
/** At most this many lines wait; past it the oldest go (they would be stale soon anyway). */
export const MAX_WAITING = 8;

export const emptyQueue: QueueState = { current: null, waiting: [] };

/** A change to the queue and the lines it dropped (stale, or pushed out by a full queue). */
export interface QueueStep {
  state: QueueState;
  dropped: SpokenLine[];
  /** The line that just became current and must start playing, if any. */
  started: SpokenLine | null;
}

function isStale(line: SpokenLine, now: number): boolean {
  return now - line.at > STALE_MS;
}

/** Takes the first line that is still fresh off `lines`; the stale ones before it are dropped. */
function takeFresh(lines: SpokenLine[], now: number): { next: SpokenLine | null; rest: SpokenLine[]; dropped: SpokenLine[] } {
  const dropped: SpokenLine[] = [];
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index] as SpokenLine;
    if (isStale(line, now)) {
      dropped.push(line);
      continue;
    }
    return { next: line, rest: lines.slice(index + 1), dropped };
  }
  return { next: null, rest: [], dropped };
}

/** A new line arrives: it plays now if nothing is playing, else it waits its turn. */
export function enqueueLine(state: QueueState, line: SpokenLine, now: number): QueueStep {
  if (state.current) {
    const waiting = [...state.waiting, line];
    const overflow = waiting.length > MAX_WAITING ? waiting.splice(0, waiting.length - MAX_WAITING) : [];
    return { state: { current: state.current, waiting }, dropped: overflow, started: null };
  }
  const { next, rest, dropped } = takeFresh([...state.waiting, line], now);
  return { state: { current: next, waiting: rest }, dropped, started: next };
}

/** The current line ended, failed or was skipped: the next fresh line plays. */
export function advanceQueue(state: QueueState, now: number): QueueStep {
  const { next, rest, dropped } = takeFresh(state.waiting, now);
  return { state: { current: next, waiting: rest }, dropped, started: next };
}

/** A play button: this line now, in place of whatever is playing; the waiting lines keep their turn. */
export function playFirst(state: QueueState, line: SpokenLine): QueueStep {
  return { state: { current: line, waiting: state.waiting }, dropped: state.current ? [state.current] : [], started: line };
}

/** Stop: nothing plays and nothing waits. */
export function clearQueue(state: QueueState): QueueStep {
  return { state: emptyQueue, dropped: [...(state.current ? [state.current] : []), ...state.waiting], started: null };
}

// ---- Who is heard ------------------------------------------------------------------------------

/** The speaking preferences of this browser. */
export interface SpeakPrefs {
  /** The header's speak toggle: the master mute. */
  master: boolean;
  /** Each agent's own "speak replies" switch, by agentKey; missing means off. */
  agents: Readonly<Record<string, boolean>>;
}

/** The key a `say` event's agent is remembered under: job agents ("") share one switch. */
export const JOB_AGENTS = "jobs";

export function agentKey(agent: string | undefined | null): string {
  return agent ? agent : JOB_AGENTS;
}

/** Whether a line from `agent` is spoken here: the master is on and that agent is opted in. */
export function wantsLine(agent: string | undefined | null, prefs: SpeakPrefs): boolean {
  return prefs.master && prefs.agents[agentKey(agent)] === true;
}

/** The speaker's name for the now-playing strip, from the server's labels when known. */
export function agentLabel(agent: string | undefined | null, labels?: ReadonlyMap<string, string>): string {
  const known = agent ? labels?.get(agent) : undefined;
  if (known) return known;
  if (!agent) return "A job agent";
  if (agent === "orchestrator") return "Orchestrator";
  if (agent.startsWith("lead-")) return `${agent.slice(5)} lead`;
  return agent;
}
