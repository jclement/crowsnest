// TanStack Query hooks: the one place components get server data from. Each hook names its key
// from queryKeys so the event stream (lib/live.ts) can invalidate exactly what changed.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { endpoints, queryKeys } from "./api";
import type { CreateJobRequest } from "./jobs";
import type { JournalKind } from "./journal";
import type { ScheduleView } from "./schedule";
import type { AnswerRequest } from "./questions";
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

// ---- Questions (M6) ----------------------------------------------------------------

/** How often question data is refetched without events: a safety net for expiries. */
const QUESTIONS_SAFETY_POLL_MS = 60_000;

/** Questions with a status; refetched on every `question` event. */
export function useQuestions(status: string) {
  return useQuery({ queryKey: queryKeys.questionList(status), queryFn: () => endpoints.questions(status), refetchInterval: QUESTIONS_SAFETY_POLL_MS });
}

/** One question with its files. */
export function useQuestion(id: string | undefined) {
  return useQuery({ queryKey: queryKeys.question(id ?? ""), queryFn: () => endpoints.question(id ?? ""), enabled: !!id });
}

/** The open count for the badge and the tab title. */
export function useInboxSummary() {
  return useQuery({ queryKey: queryKeys.inboxSummary, queryFn: endpoints.inboxSummary, refetchInterval: QUESTIONS_SAFETY_POLL_MS });
}

/** Answers a question; every question query is refreshed afterwards, whatever happened. */
export function useAnswerQuestion(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (answer: AnswerRequest) => endpoints.answerQuestion(id, answer),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.questions }),
  });
}

/** Cancels a question from the inbox; the asker is told. */
export function useCancelQuestion(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => endpoints.cancelQuestion(id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.questions }),
  });
}

// ---- Jobs (M7) -------------------------------------------------------------------------------

/** A safety poll: `job` events are the update path. It also keeps "active 2m ago" fresh. */
const JOBS_SAFETY_POLL_MS = 30_000;

/** The board. Refetched on every `job` event. */
export function useJobs() {
  return useQuery({ queryKey: queryKeys.jobList, queryFn: endpoints.jobs, refetchInterval: JOBS_SAFETY_POLL_MS });
}

/** One job with its timeline and questions. */
export function useJob(id: string | undefined) {
  return useQuery({ queryKey: queryKeys.job(id ?? ""), queryFn: () => endpoints.job(id ?? ""), enabled: !!id, refetchInterval: JOBS_SAFETY_POLL_MS });
}

export function useRoles() {
  return useQuery({ queryKey: queryKeys.roles, queryFn: endpoints.roles, staleTime: 5 * 60_000 });
}

export function useDirs() {
  return useQuery({ queryKey: queryKeys.dirs, queryFn: endpoints.dirs });
}

/** Creates a job from the text box; the board and the directory list refresh afterwards. */
export function useCreateJob() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: CreateJobRequest) => endpoints.createJob(request),
    onSettled: () =>
      Promise.all([queryClient.invalidateQueries({ queryKey: queryKeys.jobs }), queryClient.invalidateQueries({ queryKey: queryKeys.dirs })]),
  });
}

/** Cancel, nudge or resume a job. */
export function useJobAction(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: "cancel" | "nudge" | "resume") => endpoints.jobAction(id, action),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

/** Messages a job's agent. */
export function useJobMessage(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ text, force, attachments }: { text: string; force?: boolean; attachments?: string[] }) => endpoints.jobMessage(id, text, force, attachments),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

// ---- M8: pokes and the journal ----------------------------------------------------------

/** The journal, newest first, one page at a time; `journal` and `poke` events refresh it. */
export function useJournal(kinds: JournalKind[]) {
  return useInfiniteQuery({
    queryKey: queryKeys.journalPage(kinds.join(",")),
    queryFn: ({ pageParam }) => endpoints.journal(kinds, pageParam || undefined),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });
}

/** "Poke now". The journal refreshes whatever the outcome. */
export function usePokeNow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: endpoints.poke,
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.journal }),
  });
}

// ---- Scheduled work and the orchestrator's context (7.8, 7.9) ------------------------------

/** The scheduled tasks; `schedule` and `job` events refresh them, a minute's poll keeps "next due" fresh. */
export function useSchedule(enabled = true) {
  return useQuery({ queryKey: queryKeys.schedule, queryFn: endpoints.schedule, enabled, refetchInterval: 60_000 });
}

/** Run now, or the on/off switch. The answer is the new list, written straight into the cache. */
export function useScheduleAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: { name: string; run: true } | { name: string; enabled: boolean }) =>
      "run" in action ? endpoints.scheduleRun(action.name) : endpoints.scheduleSet(action.name, action.enabled),
    onSuccess: (view: ScheduleView) => queryClient.setQueryData(queryKeys.schedule, view),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.journal }),
  });
}

/** The orchestrator's context size, while the menu that shows it is open. */
export function useOrchestratorContext(enabled: boolean) {
  return useQuery({ queryKey: queryKeys.orchestratorContext, queryFn: endpoints.orchestratorContext, enabled, refetchInterval: enabled ? 15_000 : false });
}

/** Compact now. */
export function useCompactNow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: endpoints.orchestratorCompact,
    onSettled: () =>
      Promise.all([queryClient.invalidateQueries({ queryKey: queryKeys.orchestratorContext }), queryClient.invalidateQueries({ queryKey: queryKeys.journal })]),
  });
}
