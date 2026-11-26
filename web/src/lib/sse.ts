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
    let attempt = 0;
    let stopped = false;

    const dispatch = (name: string) => (event: MessageEvent) => {
      if (name === "hello") {
        attempt = 0;
        setState({ status: "open", attempt: 0, retryAt: null });
      }
      handlersRef.current[name]?.(parse(event.data));
    };

    const connect = () => {
      if (stopped) return;
      source = new EventSource(EVENTS_URL);
      for (const name of new Set<string>([...eventNames, ...Object.keys(handlersRef.current)])) {
        source.addEventListener(name, dispatch(name) as EventListener);
      }
      source.onerror = () => {
        if (stopped) return;
        source?.close();
        source = null;
        scheduleReconnect();
      };
    };

    const scheduleReconnect = () => {
      clearTimeout(retryTimer);
      const delay = backoffDelay(attempt);
      attempt += 1;
      setState({ status: "reconnecting", attempt, retryAt: Date.now() + delay });
      retryTimer = setTimeout(attemptReconnect, delay);
    };

    const attemptReconnect = () => {
      if (stopped) return;
      setState((previous) => ({ ...previous, retryAt: null }));
      endpoints.me().then(connect, () => !stopped && scheduleReconnect());
    };

    retryNowRef.current = () => {
      if (stopped || source) return;
      clearTimeout(retryTimer);
      attemptReconnect();
    };

    connect();
    return () => {
      stopped = true;
      clearTimeout(retryTimer);
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
