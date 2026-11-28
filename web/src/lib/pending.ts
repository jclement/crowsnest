// Requests still waiting on the server, so the header's heartbeat can notice a server that has
// stopped answering while the event stream stays connected (R10 Fable B3: a frozen server kept
// "Live" beside an answer that never went through). lib/api.ts registers every call here; the
// heartbeat asks how long the oldest one has waited, and how long since the stream last beat.

/** A request waiting longer than this makes the heartbeat say the server is slow to answer. */
export const LATE_REQUEST_MS = 5_000;

/**
 * The stream carries a machine sample every two seconds while it is open (internal/sysstat), so
 * this long without any event means the server has stopped talking even though the socket is up.
 */
export const STREAM_SILENT_MS = 10_000;

const started = new Map<number, number>();
let nextId = 1;
let lastBeat = Date.now();

/** Marks a request as started; call the returned function when it settles, either way. */
export function trackRequest(now: () => number = Date.now): () => void {
  const id = nextId++;
  started.set(id, now());
  return () => {
    started.delete(id);
  };
}

/** How long the oldest unsettled request has waited, in ms (0 when none is waiting). */
export function oldestPendingMs(now: number = Date.now()): number {
  let oldest = 0;
  for (const at of started.values()) oldest = Math.max(oldest, now - at);
  return oldest;
}

/** Records that the stream delivered an event (lib/live.ts calls it with every beat). */
export function noteBeat(now: number = Date.now()): void {
  lastBeat = now;
}

/** How long since the stream last delivered an event, in ms. */
export function sinceLastBeatMs(now: number = Date.now()): number {
  return now - lastBeat;
}

/**
 * True when the server looks frozen from here: a request has waited past LATE_REQUEST_MS, or the
 * open stream has been silent past STREAM_SILENT_MS.
 */
export function serverStalled(now: number = Date.now()): boolean {
  return oldestPendingMs(now) > LATE_REQUEST_MS || sinceLastBeatMs(now) > STREAM_SILENT_MS;
}

/** Forgets every request and resets the beat clock (tests only). */
export function resetPending(now: number = Date.now()): void {
  started.clear();
  lastBeat = now;
}
