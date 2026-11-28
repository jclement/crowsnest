// Keeps TanStack Query in step with the event stream. Events are invalidations, not data: the
// server says "something changed" and the client refetches what it shows (DESIGN.md section 10).
// The mapping lives here as data so it is tested without a browser and later milestones extend
// one table instead of adding handlers across the app.
import { useQueryClient, type Query, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { noteBeat } from "./pending";
import { useMemo } from "react";
import { queryKeys } from "./api";
import type { ApprovalEvent } from "./approvals";
import { isSnapshot } from "./attention";
import { notifyQuestion } from "./notifications";
import { onTrayArrival } from "./traySound";
import type { QuestionEvent } from "./questions";
import { useEventStream, type StreamState } from "./sse";
import { onSayEvent } from "./voice/speak";
import type { EventName, Fleet } from "./types";
import { systemKeys } from "./system";
import { appendSample, isSample, sysKeys, type SysStats } from "./sysstats";

/**
 * What each event invalidates. `null` means everything (hello: a fresh connection may have
 * missed any number of events). Events that carry no server state (heartbeat, say) map to [].
 */
export const invalidations: Record<EventName, QueryKey[] | null> = {
  hello: null,
  heartbeat: [],
  fleet: [queryKeys.fleet, queryKeys.agents],
  job: [queryKeys.jobs, queryKeys.schedule, queryKeys.projects, queryKeys.now],
  message: [queryKeys.agents, queryKeys.orchestratorContext],
  question: [queryKeys.questions, queryKeys.fleet, queryKeys.jobs, queryKeys.now],
  orchestrator: [queryKeys.orchestrator, queryKeys.fleet, queryKeys.agents],
  poke: [queryKeys.journal],
  journal: [queryKeys.journal, queryKeys.orchestratorContext, queryKeys.digests],
  say: [],
  schedule: [queryKeys.schedule],
  spend: [queryKeys.spend, queryKeys.jobs],
  attachment: [queryKeys.attachments],
  // Projects (21.1): a project changed (its counts and lead ride along in the list), or a lead's
  // status did (its conversation and pane are in the fleet and agent queries).
  project: [queryKeys.projects, queryKeys.instructions, queryKeys.voiceAgents],
  lead: [queryKeys.projects, queryKeys.fleet, queryKeys.agents, queryKeys.now],
  instructions: [queryKeys.instructions],
  approval: [queryKeys.approvals, queryKeys.jobs],
  // Landing, throttling and the pause (6.11 to 6.14): the switch's banner, the presence chip,
  // and the queue line and limits.
  pause: [queryKeys.pause, queryKeys.jobs],
  presence: [queryKeys.presence],
  throttle: [queryKeys.throttle, queryKeys.jobs],
  // The fleet's usage limit (6.17): the banner and the held cards.
  limits: [queryKeys.limits, queryKeys.jobs],
  // sys carries a machine sample, appended to the stats in useLiveQueries; nothing to refetch.
  sys: [],
  system: [systemKeys.system, systemKeys.onboarding],
  // The attention queue (DESIGN.md 22) arrives whole; the Now rows' waiting counts come from it.
  attention: [queryKeys.attention, queryKeys.now],
  // A diff changed or its marks did (6.21): the summaries refetch; a file's own diff is keyed by
  // its content hash, so only files that changed are fetched again.
  // A comment change publishes one too (comments-queries.ts): the comment lists refetch.
  changes: [["changes", "summary"], ["changes", "project"], ["changes", "progress"], ["changes", "comments"]],
};

/**
 * Events whose data is the new state itself, so the cache is written instead of refetched. A
 * `fleet` event carries the whole fleet (the same shape as GET /fleet); anything that does not
 * look like one falls back to the invalidation above.
 */
export const payloads: Partial<Record<EventName, { key: QueryKey; accept: (data: unknown) => boolean }>> = {
  fleet: { key: queryKeys.fleet, accept: isFleet },
  attention: { key: queryKeys.attention, accept: isSnapshot },
};

/**
 * Effects beyond the cache: a newly asked question raises a browser notification when the tab is
 * hidden; a `say` is spoken (or chimes) when talk mode is on in this tab.
 */
export const sideEffects: Partial<Record<EventName, (data: unknown) => void>> = {
  say: onSayEvent,
  question: (data) => {
    if (data && typeof data === "object" && "change" in data) {
      notifyQuestion(data as QuestionEvent);
      if ((data as QuestionEvent).change === "asked") onTrayArrival();
    }
  },
  approval: (data) => {
    if (data && typeof data === "object" && (data as ApprovalEvent).change === "requested") onTrayArrival();
  },
};

/** True when data has the shape of a Fleet (enough of it to render). */
export function isFleet(data: unknown): data is Fleet {
  const candidate = data as Partial<Fleet> | null;
  return !!candidate && Array.isArray(candidate.workspaces) && Array.isArray(candidate.agents) && typeof candidate.revision === "number";
}

/**
 * Fired on window for every event the stream delivers: the header's heartbeat (components/brand/
 * Heartbeat.tsx) beats on it. The machine samples arrive every two seconds while the stream is
 * open, so a live session beats steadily and a stalled one goes flat.
 */
export const BEAT_EVENT = "crowsnest-beat";

/** Queries already waiting for their first fetch to settle before fetching again. */
const refetchPending = new WeakSet<Query>();

/**
 * Invalidates queryKey, and makes sure the change is fetched. TanStack folds an invalidation into a
 * query's first fetch when that is still in flight (it cancels and restarts only a query that
 * already has data: query-core's Query.fetch), so a change made after the first fetch had read the
 * server stayed off screen until the next poll: a job opened the moment it was given showed "Brief
 * sent" but not "queued → running" for 30 s (CI, WebKit). Such a query is fetched again once its
 * first fetch settles.
 */
export function invalidate(queryClient: QueryClient, queryKey: QueryKey): void {
  const cache = queryClient.getQueryCache();
  for (const query of cache.findAll({ queryKey })) {
    if (query.state.data !== undefined || query.state.fetchStatus !== "fetching" || refetchPending.has(query)) continue;
    refetchPending.add(query);
    const unsubscribe = cache.subscribe((event) => {
      if (event.query !== query) return;
      if (event.type === "removed") {
        refetchPending.delete(query);
        unsubscribe();
      } else if (event.type === "updated" && query.state.fetchStatus === "idle") {
        refetchPending.delete(query);
        unsubscribe();
        void queryClient.invalidateQueries({ queryKey: query.queryKey, exact: true });
      }
    });
  }
  void queryClient.invalidateQueries({ queryKey });
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
            noteBeat();
            window.dispatchEvent(new Event(BEAT_EVENT));
            if (name === "sys") {
              if (isSample(data)) queryClient.setQueryData<SysStats>(sysKeys.stats, (previous) => appendSample(previous, data));
              return;
            }
            const payload = payloads[name as EventName];
            const written = !!payload && payload.accept(data);
            if (written) queryClient.setQueryData(payload.key, data);
            sideEffects[name as EventName]?.(data);
            if (keys === null) invalidate(queryClient, []);
            else
              for (const queryKey of keys) {
                if (written && queryKey === payload.key) continue;
                invalidate(queryClient, queryKey);
              }
          },
        ]),
      ),
    [queryClient],
  );
  return useEventStream(handlers);
}
