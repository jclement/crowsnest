// useEventStream: one EventSource on /api/v1/events per app, with reconnect and a status the UI
// can show as a banner. The server sends `hello` on every connect; since it never replays, the
// app treats hello as "refetch everything you show" (DESIGN.md section 9). Later milestones add
// handlers for fleet, message, question, orchestrator, poke and say events.
import { useEffect, useRef, useState } from "react";
import { endpoints } from "./api";

export type StreamStatus = "connecting" | "open" | "reconnecting";

export type EventHandlers = Record<string, (data: unknown) => void>;

const EVENTS_URL = "/api/v1/events";
const MAX_BACKOFF_MS = 30_000;

/**
 * Subscribes to the server's event stream while mounted. handlers maps event names to callbacks;
 * it may change between renders without reconnecting. Returns the connection status.
 */
export function useEventStream(handlers: EventHandlers): StreamStatus {
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const handlersRef = useRef(handlers);
  useEffect(() => {
    handlersRef.current = handlers;
  });

  useEffect(() => {
    let source: EventSource | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let stopped = false;
    const subscribed = new Set<string>();

    const dispatch = (name: string) => (event: MessageEvent) => {
      if (name === "hello") {
        attempt = 0;
        setStatus("open");
      }
      handlersRef.current[name]?.(parse(event.data));
    };

    const connect = () => {
      source = new EventSource(EVENTS_URL);
      subscribed.clear();
      for (const name of new Set(["hello", "heartbeat", ...Object.keys(handlersRef.current)])) {
        source.addEventListener(name, dispatch(name) as EventListener);
        subscribed.add(name);
      }
      source.onerror = () => {
        if (stopped || !source) return;
        setStatus("reconnecting");
        // CONNECTING: the browser is retrying by itself. CLOSED: it gave up (often a 401), so
        // check the session (a 401 routes to login via the API client) and retry with backoff.
        if (source.readyState === EventSource.CLOSED) scheduleReconnect();
      };
    };

    const scheduleReconnect = () => {
      source?.close();
      const delay = Math.min(1000 * 2 ** attempt, MAX_BACKOFF_MS);
      attempt += 1;
      retryTimer = setTimeout(() => {
        endpoints.me().then(
          () => !stopped && connect(),
          () => !stopped && scheduleReconnect(),
        );
      }, delay);
    };

    connect();
    return () => {
      stopped = true;
      clearTimeout(retryTimer);
      source?.close();
    };
  }, []);

  return status;
}

function parse(data: string): unknown {
  try {
    return JSON.parse(data);
  } catch {
    return data;
  }
}
