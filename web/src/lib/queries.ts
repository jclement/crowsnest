// TanStack Query hooks: the one place components get server data from. Each hook names its key
// from queryKeys so the event stream (lib/live.ts) can invalidate exactly what changed.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { endpoints, queryKeys } from "./api";
import type { SendKeysRequest } from "./types";

/** How often the fleet is refetched even without events: a safety net, not the update path. */
const FLEET_SAFETY_POLL_MS = 30_000;
/** A screen changes with every byte of output and has no event of its own, so an open preview
 * polls. Only mounted previews poll; closing the panel stops it. */
const SCREEN_POLL_MS = 2_000;

export function useMe() {
  return useQuery({ queryKey: queryKeys.me, queryFn: endpoints.me });
}

/** The fleet snapshot (GET /fleet). Refetched on every `fleet` and `hello` event. */
export function useFleet() {
  return useQuery({ queryKey: queryKeys.fleet, queryFn: endpoints.fleet, refetchInterval: FLEET_SAFETY_POLL_MS });
}

/** One agent's current screen, polled while mounted. `target` is the agent's pane id. */
export function useAgentScreen(target: string | undefined) {
  return useQuery({
    queryKey: queryKeys.agentScreen(target ?? ""),
    queryFn: () => endpoints.agentScreen(target ?? ""),
    enabled: !!target,
    refetchInterval: SCREEN_POLL_MS,
  });
}

/**
 * Sends keys to an agent, bound to a screen hash. Whatever the outcome, the agent's screen and
 * the fleet are refetched: on success they changed, and on a 409 CONFLICT the caller needs the
 * new screen to show.
 */
export function useSendKeys(target: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: SendKeysRequest) => endpoints.sendKeys(target, request),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.agent(target) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.fleet }),
      ]),
  });
}
