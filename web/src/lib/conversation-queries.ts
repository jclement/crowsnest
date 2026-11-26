// TanStack Query hooks for conversations and the orchestrator (M4). They sit beside
// lib/queries.ts rather than in it to keep that file's edits small while other milestones add
// to it. Data still comes only through lib/api.ts.
//
// A conversation query is incremental: each fetch asks for what changed after the cursor already
// in the cache and merges it, so a `message` event (which invalidates ["agents", …]) costs one
// small request rather than the whole conversation.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { endpoints, queryKeys } from "./api";
import { mergePage, type ConversationState, type PromptRequest } from "./conversation";

/** Events are the update path; this is the safety net for a transcript conversation. */
const TRANSCRIPT_SAFETY_POLL_MS = 15_000;
/** Pane text has no events of its own, so the fallback polls while shown. */
const PANE_POLL_MS = 3_000;
/** A page reporting `more` is followed at most this many times per fetch. */
const MAX_FOLLOW_PAGES = 20;

/** One agent's conversation (`target` is a pane id, or "orchestrator"). */
export function useAgentMessages(target: string | undefined) {
  const queryClient = useQueryClient();
  const key = queryKeys.agentMessages(target ?? "");
  return useQuery({
    queryKey: key,
    enabled: !!target,
    queryFn: async (): Promise<ConversationState> => {
      let state = queryClient.getQueryData<ConversationState>(key);
      for (let page = 0; page < MAX_FOLLOW_PAGES; page++) {
        const next = await endpoints.messages(target ?? "", state?.cursor);
        state = mergePage(state, next);
        if (!next.more) break;
      }
      return state as ConversationState;
    },
    refetchInterval: (query) => (query.state.data?.source === "pane" ? PANE_POLL_MS : TRANSCRIPT_SAFETY_POLL_MS),
  });
}

/** Sends a prompt; the conversation and fleet refresh whatever the outcome. */
export function useSendPrompt(target: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: PromptRequest) => endpoints.prompt(target, request),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.agent(target) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.orchestrator }),
        queryClient.invalidateQueries({ queryKey: queryKeys.fleet }),
      ]),
  });
}

/** The orchestrator's status, refreshed by `orchestrator` events. */
export function useOrchestrator() {
  return useQuery({ queryKey: queryKeys.orchestrator, queryFn: endpoints.orchestrator, refetchInterval: 30_000 });
}

/** Restart (same conversation) or New conversation. */
export function useOrchestratorAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: "restart" | "new") => (action === "restart" ? endpoints.orchestratorRestart() : endpoints.orchestratorNew()),
    onSuccess: (status) => queryClient.setQueryData(queryKeys.orchestrator, status),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.agents }),
  });
}
