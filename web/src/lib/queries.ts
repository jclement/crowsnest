// TanStack Query hooks: the one place components get server data from. Each hook names its key
// from queryKeys so the event stream (lib/live.ts) can invalidate exactly what changed.
import type { SpendGroup } from "./spend";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { endpoints, queryKeys } from "./api";
import type { CreateJobRequest } from "./jobs";
import type { BrowseOptions } from "./browse";
import type { BudgetInput, ProjectAccept } from "./accept";
import type { JournalKind } from "./journal";
import type { ScheduleInput, ScheduleView } from "./schedule";
import type { LeadAction, LeadStatus, ProjectInput, ProjectView } from "./projects";
import type { LayerScope } from "./instructions";
import type { PokeResult } from "./journal";
import type { AnswerRequest } from "./questions";
import type { DecideRequest } from "./approvals";
import type { SendKeysRequest } from "./types";
import type { DigestSettings, LandAction, ThrottleSettings } from "./landing";

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
/**
 * The one attention queue (DESIGN.md 22): every count and list of what needs the human reads
 * this. The `attention` event writes it; the poll is only a safety net for a dropped stream.
 */
export function useAttention() {
  return useQuery({ queryKey: queryKeys.attention, queryFn: endpoints.attention, refetchInterval: QUESTIONS_SAFETY_POLL_MS });
}

/** Snooze, unsnooze or dismiss an attention item; the answer is the new snapshot. */
export function useAttentionAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ action, id }: { action: "snooze" | "unsnooze" | "dismiss"; id: string }) =>
      action === "snooze" ? endpoints.snoozeItem(id) : action === "unsnooze" ? endpoints.unsnoozeItem(id) : endpoints.dismissItem(id),
    onSuccess: (snapshot) => queryClient.setQueryData(queryKeys.attention, snapshot),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: queryKeys.attention }),
  });
}

/** The Now view's rows. */
export function useNow() {
  return useQuery({ queryKey: queryKeys.now, queryFn: endpoints.now, refetchInterval: 60_000 });
}

/** What happened since this browser was last in use. */
export function useNowSince(browser: string) {
  return useQuery({ queryKey: queryKeys.nowSince(browser), queryFn: () => endpoints.nowSince(browser), refetchInterval: 60_000 });
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

// ---- The folder picker (DESIGN.md 4.23) -------------------------------------------------------

/** One directory's listing; off while path is empty. Errors (outside, unreadable) are not retried. */
export function useBrowse(path: string, options: BrowseOptions) {
  return useQuery({
    queryKey: queryKeys.browse(path, options),
    queryFn: () => endpoints.browse(path, options),
    enabled: path !== "",
    retry: false,
    staleTime: 10_000,
    placeholderData: (previous) => previous,
  });
}

/** The facts about one folder; off while path is empty. */
export function useFolderFacts(path: string) {
  return useQuery({ queryKey: queryKeys.browseFacts(path), queryFn: () => endpoints.browseFacts(path), enabled: path !== "", retry: false, staleTime: 30_000 });
}

/** Where the picker starts: home, favourites, roots. */
export function useBrowsePlaces(enabled = true) {
  return useQuery({ queryKey: queryKeys.browsePlaces, queryFn: endpoints.browsePlaces, enabled, staleTime: 30_000 });
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

// ---- Spend (7.11) ----------------------------------------------------------------------------

/** Today's and this week's totals, and the orchestrator's line. `spend` events refresh it. */
export function useSpendSummary(enabled = true) {
  return useQuery({ queryKey: queryKeys.spendSummary, queryFn: endpoints.spendSummary, refetchInterval: 60_000, enabled });
}

/** The breakdown for a range of the server's local days, grouped by job, model or day. */
export function useSpendReport(group: SpendGroup, from: string | undefined, to: string | undefined, enabled = true) {
  return useQuery({
    queryKey: queryKeys.spendReport(group, from ?? "", to ?? ""),
    queryFn: () => endpoints.spendReport(group, from, to),
    enabled: enabled && !!from && !!to,
    placeholderData: (previous) => previous,
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

/**
 * The scheduled tasks, or one project's (GET /schedule?project=id; 0 is the orchestrator's and
 * unfiled ones); `schedule` and `job` events refresh them, a minute's poll keeps "next due" fresh.
 */
export function useSchedule(enabled = true, projectId?: number) {
  return useQuery({
    queryKey: projectId === undefined ? queryKeys.schedule : queryKeys.scheduleProject(projectId),
    queryFn: () => endpoints.schedule(projectId),
    enabled,
    refetchInterval: 60_000,
  });
}

/** Writes a full schedule list into the unfiltered cache and refreshes the filtered ones. */
function useScheduleWrite() {
  const queryClient = useQueryClient();
  return {
    onSuccess: (view: ScheduleView) => {
      queryClient.setQueryData(queryKeys.schedule, view);
      void queryClient.invalidateQueries({ queryKey: queryKeys.schedule, predicate: (query) => query.queryKey.length > 1 });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.journal }),
  };
}

/** Run now, or the on/off switch. The answer is the new list, written straight into the cache. */
export function useScheduleAction() {
  const write = useScheduleWrite();
  return useMutation({
    mutationFn: (action: { name: string; run: true } | { name: string; enabled: boolean }) =>
      "run" in action ? endpoints.scheduleRun(action.name) : endpoints.scheduleSet(action.name, action.enabled),
    ...write,
  });
}

/** Approves a task file's content as the Schedule view showed it (R5-S3): hash names those bytes. */
export function useScheduleApprove() {
  const write = useScheduleWrite();
  return useMutation({
    mutationFn: (review: { name: string; hash: string }) => endpoints.scheduleApprove(review.name, review.hash),
    ...write,
  });
}

/** Creates, edits or deletes an app schedule (task files are read-only: 409). */
export function useScheduleEdit() {
  const write = useScheduleWrite();
  return useMutation({
    mutationFn: async (
      change: { kind: "create"; input: ScheduleInput; enabled: boolean } | { kind: "update"; input: ScheduleInput; enabled: boolean } | { kind: "delete"; name: string },
    ): Promise<ScheduleView> => {
      if (change.kind === "delete") return endpoints.scheduleDelete(change.name);
      if (change.kind === "update") return endpoints.scheduleUpdate(change.input.name, { ...change.input, enabled: change.enabled });
      const created = await endpoints.scheduleCreate(change.input);
      // A new schedule starts switched on; one made switched off is switched off right after.
      return change.enabled ? created : endpoints.scheduleSet(change.input.name, false);
    },
    ...write,
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

// ---- Projects and their leads (21.1, 21.2) -----------------------------------------------------

/** The projects with their leads and job counts; `project`, `lead` and `job` events refresh them. */
export function useProjects(archived = false) {
  return useQuery({ queryKey: queryKeys.projectList(archived), queryFn: () => endpoints.projects(archived), refetchInterval: 60_000 });
}

/** One project by slug or id. */
export function useProject(ref: string | undefined) {
  return useQuery({ queryKey: queryKeys.project(ref ?? ""), queryFn: () => endpoints.project(ref ?? ""), enabled: !!ref });
}

/** Creates a project; the list and the remembered directories refresh. */
export function useCreateProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ProjectInput) => endpoints.createProject(input),
    onSettled: () =>
      Promise.all([queryClient.invalidateQueries({ queryKey: queryKeys.projects }), queryClient.invalidateQueries({ queryKey: queryKeys.dirs })]),
  });
}

/** Edits, archives or unarchives a project. */
export function useUpdateProject(ref: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ProjectInput) => endpoints.updateProject(ref, input),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projects }),
        queryClient.invalidateQueries({ queryKey: queryKeys.instructions }),
        queryClient.invalidateQueries({ queryKey: queryKeys.voiceAgents }),
      ]),
  });
}

/** Start, stop, restart, new conversation or poke a project's lead. */
export function useLeadAction(ref: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: LeadAction): Promise<LeadStatus | PokeResult> => endpoints.leadAction(ref, action),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projects }),
        queryClient.invalidateQueries({ queryKey: queryKeys.fleet }),
        queryClient.invalidateQueries({ queryKey: queryKeys.agents }),
        queryClient.invalidateQueries({ queryKey: queryKeys.journal }),
        queryClient.invalidateQueries({ queryKey: queryKeys.instructions }),
      ]),
  });
}

/** The lead of a project from the cached list, without a request of its own. */
export function findProject(projects: ProjectView[] | undefined, slug: string | undefined): ProjectView | undefined {
  return slug ? projects?.find((project) => project.slug === slug) : undefined;
}

// ---- Instruction layers (21.3) ---------------------------------------------------------------------

/** Every layer, where its text comes from, and the size cap; `instructions` events refresh it. */
export function useInstructions(enabled = true) {
  return useQuery({ queryKey: queryKeys.instructionList, queryFn: endpoints.instructions, enabled });
}

/** One layer with its revisions. */
export function useInstructionLayer(scope: LayerScope, key: string, enabled = true) {
  return useQuery({ queryKey: queryKeys.instructionLayer(scope, key), queryFn: () => endpoints.instructionLayer(scope, key), enabled });
}

/** Save, reset to the built-in, restore a revision, mark a role file read (R5-S14), or apply a layer a restore brought back (R7-S4). Every instruction query refreshes after. */
export function useInstructionEdit(scope: LayerScope, key: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (
      change:
        | { kind: "save"; body: string }
        | { kind: "reset" }
        | { kind: "restore"; revision: number }
        | { kind: "file-seen"; hash: string }
        | { kind: "apply-restored"; hash: string },
    ) => {
      if (change.kind === "save") return endpoints.saveInstruction(scope, key, change.body);
      if (change.kind === "reset") return endpoints.resetRole(key);
      if (change.kind === "file-seen") return endpoints.roleFileSeen(key, change.hash);
      if (change.kind === "apply-restored") return endpoints.applyRestoredInstruction(scope, key, change.hash);
      return endpoints.restoreInstruction(scope, key, change.revision);
    },
    onSuccess: (detail) => queryClient.setQueryData(queryKeys.instructionLayer(scope, key), detail),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.instructions }),
  });
}

/** What an agent was started with, and whether its instructions changed since. */
export function useAgentInstructions(subject: string | undefined) {
  return useQuery({
    queryKey: queryKeys.agentInstructions(subject ?? ""),
    queryFn: () => endpoints.agentInstructions(subject ?? ""),
    enabled: !!subject,
    refetchInterval: 60_000,
  });
}

/** "Restart with new instructions": a job's agent, a lead, or the orchestrator. */
export function useRestartWithInstructions() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (target: { kind: "job"; id: string } | { kind: "lead"; slug: string } | { kind: "orchestrator" }): Promise<unknown> => {
      if (target.kind === "job") return endpoints.jobRestartAgent(target.id);
      if (target.kind === "lead") return endpoints.leadAction(target.slug, "restart");
      return endpoints.orchestratorRestart();
    },
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.instructions }),
        queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
        queryClient.invalidateQueries({ queryKey: queryKeys.projects }),
        queryClient.invalidateQueries({ queryKey: queryKeys.orchestrator }),
        queryClient.invalidateQueries({ queryKey: queryKeys.agents }),
      ]),
  });
}

// ---- Per-agent voices -----------------------------------------------------------------------------

/** Each standing agent's voice. Only when the server has voice on (every /voice route 404s else). */
export function useAgentVoices(enabled: boolean) {
  return useQuery({ queryKey: queryKeys.voiceAgents, queryFn: endpoints.voiceAgents, enabled, staleTime: 60_000, retry: false });
}

export function useSetAgentVoice() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ agent, voiceId }: { agent: string; voiceId: string }) => endpoints.setAgentVoice(agent, voiceId),
    onSuccess: (voices) => queryClient.setQueryData(queryKeys.voiceAgents, voices),
  });
}

// ---- Approvals (DESIGN.md 8.7) ----------------------------------------------------------------

/** Pending approvals, or every recent one ("all"); an `approval` event refreshes them. */
export function useApprovals(status: "pending" | "all" = "pending") {
  return useQuery({ queryKey: queryKeys.approvalList(status), queryFn: () => endpoints.approvals(status), refetchInterval: QUESTIONS_SAFETY_POLL_MS });
}

export function useApproval(id: string | undefined) {
  return useQuery({ queryKey: queryKeys.approval(id ?? ""), queryFn: () => endpoints.approval(id ?? ""), enabled: !!id });
}

export function useDecideApproval(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: DecideRequest) => endpoints.decideApproval(id, request),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.approvals });
      void queryClient.invalidateQueries({ queryKey: queryKeys.jobs });
    },
  });
}

// ---- Landing, throttling, the pause, presence, the digest (6.11 to 6.14, 7.12) -----------------

/** The Review tab: what the job produced. Refreshed by `job` events (it sits under ["jobs"]). */
export function useReview(id: string, enabled = true) {
  return useQuery({ queryKey: queryKeys.review(id), queryFn: () => endpoints.review(id), enabled });
}

/** One changed file's diff, fetched only when its row is opened. */
export function useReviewDiff(id: string, path: string, enabled: boolean) {
  return useQuery({ queryKey: queryKeys.reviewDiff(id, path), queryFn: () => endpoints.reviewDiff(id, path), enabled, staleTime: 30_000 });
}

/** Land: merge, push with a PR, or discard (the confirmation is the caller's). head is the commit
 * the Review tab showed: the server lands exactly it, or refuses (R5-S4). */
export function useLand(id: string, head?: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: LandAction) => endpoints.land(id, action, head),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

/** Land over crowsnest's evidence (stale, failed or missing check; a base that moved on): only ever
 * after the human ticked the explicit override. */
export function useLandOverride(id: string, head?: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: LandAction) => endpoints.landOverride(id, action, head),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

export function useTeardown() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => endpoints.teardown(id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

export function useStartAnyway() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => endpoints.startAnyway(id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

// ---- Acceptance, budgets, wake-ups, the usage limit, receipt (DESIGN.md 6.15 to 6.19) ----

/** The fleet's usage limits; a `limits` event refreshes them, and a minute's poll keeps the countdown honest. */
export function useLimits() {
  return useQuery({ queryKey: queryKeys.limits, queryFn: endpoints.limits, refetchInterval: 60_000 });
}

/** Live jobs that break "every wait has an owner and a wake-up" (the System page). */
export function useWaits() {
  return useQuery({ queryKey: queryKeys.waits, queryFn: endpoints.waits, refetchInterval: 60_000 });
}

export function useProjectAccept(id: number) {
  return useQuery({ queryKey: queryKeys.projectAccept(id), queryFn: () => endpoints.projectAccept(id), enabled: id > 0 });
}

export function useSetProjectAccept(id: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (value: Pick<ProjectAccept, "check_command" | "budget_usd" | "budget_minutes">) => endpoints.setProjectAccept(id, value),
    onSuccess: (view) => queryClient.setQueryData(queryKeys.projectAccept(id), view),
  });
}

export type GuardAction = "recheck" | "rebase" | "finish-step" | "wake";

/** The job view's acceptance, budget and wake-up actions. */
export function useJobGuard(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: GuardAction) => {
      switch (action) {
        case "recheck":
          return endpoints.recheck(id);
        case "rebase":
          return endpoints.rebase(id);
        case "finish-step":
          return endpoints.finishStep(id);
        default:
          return endpoints.wake(id);
      }
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

export function useSetBudget(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (budget: BudgetInput) => endpoints.setBudget(id, budget),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

/** An answer's reconcile ("did it arrive?") and the explicit retype. */
export function useAnswerReceipt(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: "reconcile" | "retype") => (action === "reconcile" ? endpoints.reconcileAnswer(id) : endpoints.retypeAnswer(id)),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.questions }),
  });
}

export function useSweep(enabled = true) {
  return useQuery({ queryKey: queryKeys.sweep, queryFn: endpoints.sweep, enabled });
}

export function useSweepDiscard() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (ids: string[]) => endpoints.sweepDiscard(ids),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

export function useQueue() {
  return useQuery({ queryKey: queryKeys.queue, queryFn: endpoints.queue, refetchInterval: 30_000 });
}

export function usePause() {
  return useQuery({ queryKey: queryKeys.pause, queryFn: endpoints.pause, refetchInterval: 60_000 });
}

export function useSetPause() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ paused, until, reason }: { paused: boolean; until?: string; reason?: string }) =>
      paused ? endpoints.setPause(until ?? "", reason ?? "") : endpoints.resume(),
    onSuccess: (state) => queryClient.setQueryData(queryKeys.pause, state),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.jobs }),
  });
}

export function useThrottle(enabled = true) {
  return useQuery({ queryKey: queryKeys.throttle, queryFn: endpoints.throttle, enabled, refetchInterval: enabled ? 10_000 : false });
}

export function useSaveThrottle() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (settings: ThrottleSettings | null) => (settings ? endpoints.saveThrottle(settings) : endpoints.resetThrottle()),
    onSettled: () => Promise.all([queryClient.invalidateQueries({ queryKey: queryKeys.throttle }), queryClient.invalidateQueries({ queryKey: queryKeys.jobs })]),
  });
}

export function usePresence(enabled = true) {
  return useQuery({ queryKey: queryKeys.presence, queryFn: endpoints.presence, enabled });
}

export function useSetDND() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (until: string) => endpoints.setDND(until),
    onSuccess: (state) => queryClient.setQueryData(queryKeys.presence, state),
  });
}

export function useDigests(enabled = true) {
  return useQuery({ queryKey: queryKeys.digests, queryFn: endpoints.digests, enabled });
}

export function useDigestSettings() {
  return useQuery({ queryKey: queryKeys.digestSettings, queryFn: endpoints.digestSettings });
}

export function useSaveDigestSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (settings: DigestSettings) => endpoints.saveDigestSettings(settings),
    onSuccess: (settings) => queryClient.setQueryData(queryKeys.digestSettings, settings),
  });
}

export function useDigestNow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => endpoints.digestNow(),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.digests }),
  });
}

/** The heavy-command slots (DESIGN.md 6.12): cheap file reads on the server, polled. */
export function useHeavy(intervalMs = 5_000) {
  return useQuery({ queryKey: ["heavy"], queryFn: endpoints.heavy, refetchInterval: intervalMs });
}
