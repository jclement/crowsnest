// useEventStream: one EventSource on /api/v1/events per app, reconnecting with exponential
// backoff, and a state the UI shows as the reconnect banner. The server sends `hello` on every
// connect and never replays, so the app treats hello as "refetch everything you show"
// (DESIGN.md section 10); lib/live.ts maps the other events to query invalidations.
//
// Reconnection is ours, not the browser's: on any error the source is closed and a new one is
// opened after 1 s, 2 s, 4 s … up to 30 s. Before each attempt the session is checked with
// /auth/me, so an expired session lands on the login page (through the API client's 401
// handler) instead of retrying forever. "Retry now" skips the wait.
import { useCallback, useEffect, useRef, useState } from "react";
import { endpoints, fixturesEnabled } from "./api";
import { eventNames } from "./types";

export type StreamStatus = "connecting" | "open" | "reconnecting";

export interface StreamState {
  status: StreamStatus;
  /** Consecutive failed attempts since the stream was last open. */
  attempt: number;
  /** When the next attempt starts (epoch ms), while one is scheduled. */
  retryAt: number | null;
  /** Reconnect immediately instead of waiting out the backoff. */
  retryNow: () => void;
}

export type EventHandlers = Partial<Record<string, (data: unknown) => void>>;

const EVENTS_URL = "/api/v1/events";
export const INITIAL_BACKOFF_MS = 1_000;
export const MAX_BACKOFF_MS = 30_000;
/**
 * How long a reconnect's session probe may take, and how long a new stream may take to say hello,
 * before the attempt counts as failed and another is scheduled. Without them, a request sitting on
 * a connection nobody answers (a server still starting, or one that died mid-reply) left the probe
 * pending for good: no retry was ever scheduled and Retry now did nothing.
 */
export const PROBE_TIMEOUT_MS = 8_000;
export const HELLO_TIMEOUT_MS = 12_000;

/** The wait before reconnect attempt number `attempt` (0-based). */
export function backoffDelay(attempt: number): number {
  return Math.min(INITIAL_BACKOFF_MS * 2 ** attempt, MAX_BACKOFF_MS);
}

/**
 * Subscribes to the server's event stream while mounted. handlers maps event names to callbacks;
 * it may change between renders without reconnecting. In fixtures mode there is no server, so
 * the stream reports itself open and never connects.
 */
export function useEventStream(handlers: EventHandlers): StreamState {
  const fixtures = fixturesEnabled();
  const [state, setState] = useState<Omit<StreamState, "retryNow">>({
    status: fixtures ? "open" : "connecting",
    attempt: 0,
    retryAt: null,
  });
  const handlersRef = useRef(handlers);
  const retryNowRef = useRef<() => void>(() => {});
  useEffect(() => {
    handlersRef.current = handlers;
  });

  useEffect(() => {
    if (fixtures) return;
    let source: EventSource | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let helloTimer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let stopped = false;
    // A session probe in flight. Reconnects are serialised on it: a second attempt while one is
    // pending (the timer firing as Retry now is pressed, say) would open a second EventSource
    // that nothing tracks or closes (R1-C08).
    let probing = false;

    // Every handler is bound to the EventSource it was registered on, and does nothing once that
    // source is no longer the current one, so a late error from an old stream cannot close the
    // new one.
    const dispatch = (own: EventSource, name: string) => (event: MessageEvent) => {
      if (stopped || source !== own) return;
      if (name === "hello") {
        clearTimeout(helloTimer);
        attempt = 0;
        setState({ status: "open", attempt: 0, retryAt: null });
      }
      handlersRef.current[name]?.(parse(event.data));
    };

    const connect = () => {
      if (stopped) return;
      source?.close();
      const own = new EventSource(EVENTS_URL);
      source = own;
      for (const name of new Set<string>([...eventNames, ...Object.keys(handlersRef.current)])) {
        own.addEventListener(name, dispatch(own, name) as EventListener);
      }
      const fail = () => {
        own.close();
        if (stopped || source !== own) return;
        source = null;
        scheduleReconnect();
      };
      own.onerror = fail;
      // A stream that never says hello is a failed attempt too: back to retrying.
      clearTimeout(helloTimer);
      helloTimer = setTimeout(fail, HELLO_TIMEOUT_MS);
    };

    const scheduleReconnect = () => {
      clearTimeout(retryTimer);
      const delay = backoffDelay(attempt);
      attempt += 1;
      setState({ status: "reconnecting", attempt, retryAt: Date.now() + delay });
      retryTimer = setTimeout(attemptReconnect, delay);
    };

    const attemptReconnect = () => {
      if (stopped || probing || source) return;
      probing = true;
      clearTimeout(retryTimer);
      setState((previous) => ({ ...previous, retryAt: null }));
      // One probe at a time (R1-C08), and never longer than PROBE_TIMEOUT_MS: a late answer of
      // a probe that already timed out is ignored, so it cannot start a second stream.
      let settled = false;
      const done = (ok: boolean) => {
        if (settled) return;
        settled = true;
        clearTimeout(probeTimer);
        probing = false;
        if (stopped) return;
        if (ok) connect();
        else scheduleReconnect();
      };
      const probeTimer = setTimeout(() => done(false), PROBE_TIMEOUT_MS);
      endpoints.me().then(
        () => done(true),
        () => done(false),
      );
    };

    retryNowRef.current = attemptReconnect;

    connect();
    return () => {
      stopped = true;
      clearTimeout(retryTimer);
      clearTimeout(helloTimer);
      source?.close();
    };
  }, [fixtures]);

  const retryNow = useCallback(() => retryNowRef.current(), []);
  return { ...state, retryNow };
}

function parse(data: string): unknown {
  try {
    return JSON.parse(data);
  } catch {
    return data;
  }
}
