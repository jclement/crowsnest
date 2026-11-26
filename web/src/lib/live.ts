// Keeps TanStack Query in step with the event stream. Events are invalidations, not data: the
// server says "something changed" and the client refetches what it shows (DESIGN.md section 10).
// The mapping lives here as data so it is tested without a browser and later milestones extend
// one table instead of adding handlers across the app.
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useMemo } from "react";
import { queryKeys } from "./api";
import { notifyQuestion } from "./notifications";
import type { QuestionEvent } from "./questions";
import { useEventStream, type StreamState } from "./sse";
import { onSayEvent } from "./voice/talk";
import type { EventName, Fleet } from "./types";

/**
 * What each event invalidates. `null` means everything (hello: a fresh connection may have
 * missed any number of events). Events that carry no server state (heartbeat, say) map to [].
 */
export const invalidations: Record<EventName, QueryKey[] | null> = {
  hello: null,
  heartbeat: [],
  fleet: [queryKeys.fleet, queryKeys.agents],
  job: [queryKeys.jobs],
  message: [queryKeys.agents],
  question: [queryKeys.questions, queryKeys.fleet, queryKeys.jobs],
  orchestrator: [queryKeys.orchestrator, queryKeys.fleet, queryKeys.agents],
  poke: [],
  say: [],
};

/**
 * Events whose data is the new state itself, so the cache is written instead of refetched. A
 * `fleet` event carries the whole fleet (the same shape as GET /fleet); anything that does not
 * look like one falls back to the invalidation above.
 */
export const payloads: Partial<Record<EventName, { key: QueryKey; accept: (data: unknown) => boolean }>> = {
  fleet: { key: queryKeys.fleet, accept: isFleet },
};

/**
 * Effects beyond the cache: a newly asked question raises a browser notification when the tab is
 * hidden; a `say` is spoken (or chimes) when talk mode is on in this tab.
 */
export const sideEffects: Partial<Record<EventName, (data: unknown) => void>> = {
  say: onSayEvent,
  question: (data) => {
    if (data && typeof data === "object" && "change" in data) notifyQuestion(data as QuestionEvent);
  },
};

/** True when data has the shape of a Fleet (enough of it to render). */
export function isFleet(data: unknown): data is Fleet {
  const candidate = data as Partial<Fleet> | null;
  return !!candidate && Array.isArray(candidate.workspaces) && Array.isArray(candidate.agents) && typeof candidate.revision === "number";
}

/** Opens the app's one event stream and wires it to the query cache. Returns its state. */
export function useLiveQueries(): StreamState {
  const queryClient = useQueryClient();
  const handlers = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(invalidations).map(([name, keys]) => [
          name,
          (data: unknown) => {
            const payload = payloads[name as EventName];
            const written = !!payload && payload.accept(data);
            if (written) queryClient.setQueryData(payload.key, data);
            sideEffects[name as EventName]?.(data);
            if (keys === null) void queryClient.invalidateQueries();
            else
              for (const queryKey of keys) {
                if (written && queryKey === payload.key) continue;
                void queryClient.invalidateQueries({ queryKey });
              }
          },
        ]),
      ),
    [queryClient],
  );
  return useEventStream(handlers);
}
