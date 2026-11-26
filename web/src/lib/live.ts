// Keeps TanStack Query in step with the event stream. Events are invalidations, not data: the
// server says "something changed" and the client refetches what it shows (DESIGN.md section 10).
// The mapping lives here as data so it is tested without a browser and later milestones extend
// one table instead of adding handlers across the app.
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useMemo } from "react";
import { queryKeys } from "./api";
import { useEventStream, type StreamState } from "./sse";
import type { EventName } from "./types";

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
  question: [queryKeys.questions, queryKeys.fleet],
  orchestrator: [queryKeys.orchestrator, queryKeys.fleet, queryKeys.agents],
  poke: [],
  say: [],
};

/** Opens the app's one event stream and wires it to the query cache. Returns its state. */
export function useLiveQueries(): StreamState {
  const queryClient = useQueryClient();
  const handlers = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(invalidations).map(([name, keys]) => [
          name,
          () => {
            if (keys === null) void queryClient.invalidateQueries();
            else for (const queryKey of keys) void queryClient.invalidateQueries({ queryKey });
          },
        ]),
      ),
    [queryClient],
  );
  return useEventStream(handlers);
}
