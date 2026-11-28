// The typed API client. Every call goes through api(): it adds the X-Crowsnest header the server
// requires on state-changing requests (DESIGN.md 4.4), unwraps the {"data": ...} envelope, turns
// {"error": ...} into an ApiError, and on a 401 calls the registered handler so the app can send
// the user to the login page. Later milestones add endpoints here, not new fetch calls.
//
// Fixtures mode (development builds only): open the app with `?fixtures=1` and every call is
// answered in memory from lib/fixtures.ts, so the UI can be worked on, and screenshotted,
// without a server. `?fixtures=0` turns it off. The choice is kept for the tab's lifetime
// (sessionStorage) because client-side navigation drops the query string.
import type { SpendGroup, SpendPeriod, SpendReport } from "./spend";
import { trackRequest } from "./pending";
import type { AgentScreen, Fleet, KeysResponse, SendKeysRequest } from "./types";
import type { MessagePage, OrchestratorStatus, PromptRequest, PromptResult } from "./conversation";
import type { AnswerRequest, InboxSummary, Question } from "./questions";
import type { Approval, DecideRequest } from "./approvals";
import type { AttentionSnapshot, NowResponse, SinceResponse } from "./attention";
import type { CreateJobRequest, DirEntry, Job, JobDetail, RoleInfo } from "./jobs";
import type { BrowseOptions, BrowsePlaces, FolderFacts, Listing } from "./browse";
import type { BudgetInput, FleetLimit, ProjectAccept, WaitsView } from "./accept";
import type {
  Digest,
  DigestSettings,
  DigestStart,
  FileDiffView,
  HeavyStatus,
  LandAction,
  LandResult,
  PauseState,
  PresenceState,
  QueueLine,
  ReviewView,
  SweepItem,
  ThrottleSettings,
  ThrottleView,
} from "./landing";
import type { JournalKind, JournalPage, PokeResult } from "./journal";
import type { ContextInfo, ScheduleInput, ScheduleView } from "./schedule";
import type { EngineInfo } from "./engines";
import type { LeadAction, LeadStatus, ProjectCreateInput, ProjectInput, ProjectList, ProjectView } from "./projects";
import type { AgentInstructions, InstructionsList, LayerDetail, LayerScope } from "./instructions";
import type { AttachmentLimits } from "./attachments";
import type { PushDevice, PushKind, PushSettings, PushSubscriptionJSON, PushTestResult } from "./push";

/** An error response from the API, carrying the envelope's code and user-facing message. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** Machine-readable detail some errors carry (the dialog or draft that refused a prompt). */
  readonly detail: unknown;

  constructor(status: number, code: string, message: string, detail?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.detail = detail;
  }
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  signal?: AbortSignal;
  /** Do not call the 401 handler: for the login call, where 401 means "wrong password". */
  skipAuthRedirect?: boolean;
}

const API_BASE = "/api/v1";

let unauthorizedHandler: (() => void) | null = null;

/** Registers what happens when any call comes back 401. Returns an unregister function. */
export function onUnauthorized(handler: () => void): () => void {
  unauthorizedHandler = handler;
  return () => {
    if (unauthorizedHandler === handler) unauthorizedHandler = null;
  };
}

const FIXTURES_KEY = "crowsnest-fixtures";

/** True when this page answers API calls from fixtures. Always false in a production build. */
export function fixturesEnabled(): boolean {
  if (!import.meta.env.DEV || typeof window === "undefined") return false;
  try {
    const flag = new URLSearchParams(window.location.search).get("fixtures");
    if (flag === "1") sessionStorage.setItem(FIXTURES_KEY, "1");
    if (flag === "0") sessionStorage.removeItem(FIXTURES_KEY);
    return sessionStorage.getItem(FIXTURES_KEY) === "1";
  } catch {
    return false;
  }
}

/** Calls the API and returns the envelope's data. Throws ApiError on any non-2xx response. */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  if (import.meta.env.DEV && fixturesEnabled()) {
    const { fixtureApi } = await import("./fixtures");
    return (await fixtureApi(path, options.method ?? "GET", options.body)) as T;
  }
  const headers: Record<string, string> = { Accept: "application/json", "X-Crowsnest": "1" };
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  let response: Response;
  // Tracked so the heartbeat can tell a server that stopped answering (lib/pending.ts).
  const settled = trackRequest();
  try {
    response = await fetch(API_BASE + path, {
      method: options.method ?? "GET",
      headers,
      credentials: "same-origin",
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw error;
    throw new ApiError(0, "NETWORK_ERROR", "Cannot reach crowsnest. Check the connection.");
  } finally {
    settled();
  }
  const payload = await readJSON(response);
  if (!response.ok) {
    if (response.status === 401 && !options.skipAuthRedirect) unauthorizedHandler?.();
    const error = (payload as { error?: { code?: string; message?: string; detail?: unknown } } | null)?.error;
    throw new ApiError(response.status, error?.code ?? "HTTP_" + response.status, error?.message ?? response.statusText, error?.detail);
  }
  return (payload as { data: T }).data;
}

async function readJSON(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

// ---- Endpoints ---------------------------------------------------------------

export interface Health {
  status: string;
  version: string;
  /** True while crowsnest is locked waiting for first-run setup. */
  setup_required?: boolean;
  update_available: boolean;
  /** Set with update_available: the newer release's tag and its GitHub page. */
  latest_version?: string;
  release_url?: string;
}

export interface Features {
  auth: boolean;
  voice: boolean;
  demo: boolean;
  session: string;
  /** This instance's name (--name, else the short hostname): header, page title. */
  name?: string;
  /** Upload limits; absent when attachments are off (DESIGN.md 8.6). */
  attachments?: AttachmentLimits;
  /** True while the first-run checklist is unfinished: the app opens /welcome. */
  onboarding?: boolean;
}

export interface Me {
  username: string;
  features: Features;
}

/** GET /setup: whether first-run setup is waiting, and what the form starts with. */
export interface SetupInfo {
  required: boolean;
  default_name?: string;
  min_password_length: number;
}

export interface SetupRequest {
  token: string;
  username: string;
  password: string;
  name: string;
}

/** GET /account: the Account sheet. */
export interface Account {
  username: string;
  /** "database": chosen at setup, changeable here; "config": set by flags or env, read-only; "none": --no-auth. */
  source: "database" | "config" | "none";
  name: string;
  /** The name is set by --name or CROWSNEST_NAME: read-only here. */
  name_managed: boolean;
  min_password_length: number;
}

/** The API's answer while crowsnest is locked for first-run setup. */
export const SETUP_REQUIRED = "SETUP_REQUIRED";

/** True for the error every route but setup gives while crowsnest waits for first-run setup. */
export function isSetupRequired(error: unknown): boolean {
  return error instanceof ApiError && error.code === SETUP_REQUIRED;
}

export const endpoints = {
  health: () => api<Health>("/health"),
  me: () => api<Me>("/auth/me"),
  login: (username: string, password: string) =>
    api<Me>("/auth/login", { method: "POST", body: { username, password }, skipAuthRedirect: true }),
  logout: () => api<{ signed_out: boolean }>("/auth/logout", { method: "POST" }),
  // ---- First-run setup and the Account sheet ----
  setupInfo: () => api<SetupInfo>("/setup"),
  /** Completes setup; the response sets the session cookie. */
  setup: (request: SetupRequest) =>
    api<{ signed_in: boolean; username: string }>("/setup", { method: "POST", body: request, skipAuthRedirect: true }),
  account: () => api<Account>("/account"),
  /** 403 WRONG_PASSWORD when the current password is wrong; signs every other session out. */
  changePassword: (currentPassword: string, newPassword: string) =>
    api<{ changed: boolean }>("/account/password", { method: "POST", body: { current_password: currentPassword, new_password: newPassword } }),
  setName: (name: string) => api<{ name: string }>("/account/name", { method: "PUT", body: { name } }),
  fleet: () => api<Fleet>("/fleet"),
  agentScreen: (target: string) => api<AgentScreen>(`/agents/${encodeURIComponent(target)}/screen`),
  /** Sends keys to an agent, bound to the screen they answer: 409 CONFLICT if it changed. */
  sendKeys: (target: string, request: SendKeysRequest) =>
    api<KeysResponse>(`/agents/${encodeURIComponent(target)}/keys`, { method: "POST", body: request }),
  // ---- M4: conversations and the orchestrator ----
  /** A page of the agent's conversation: changes after `after`, or the newest messages. */
  messages: (target: string, after?: string) =>
    api<MessagePage>(`/agents/${encodeURIComponent(target)}/messages${after ? `?after=${encodeURIComponent(after)}` : ""}`),
  /** Types a prompt into the agent. 409 BLOCKED (a dialog) or DRAFT (text at the terminal). */
  prompt: (target: string, request: PromptRequest) =>
    api<PromptResult>(`/agents/${encodeURIComponent(target)}/prompt`, { method: "POST", body: request }),
  orchestrator: () => api<OrchestratorStatus>("/orchestrator"),
  orchestratorRestart: () => api<OrchestratorStatus>("/orchestrator/restart", { method: "POST" }),
  orchestratorNew: () => api<OrchestratorStatus>("/orchestrator/new", { method: "POST" }),
  // ---- M6: the inbox ----
  /** Questions with a status ("open", "answered,expired,cancelled", or "all"), without file contents. */
  questions: (status: string) => api<Question[]>(`/questions?status=${encodeURIComponent(status)}`),
  /** One question, with its files. */
  question: (id: string) => api<Question>(`/questions/${encodeURIComponent(id)}`),
  answerQuestion: (id: string, answer: AnswerRequest) =>
    api<Question>(`/questions/${encodeURIComponent(id)}/answer`, { method: "POST", body: answer }),
  cancelQuestion: (id: string) => api<Question>(`/questions/${encodeURIComponent(id)}`, { method: "DELETE" }),
  approvals: (status: "pending" | "all") => api<Approval[]>(`/approvals?status=${status}`),
  approval: (id: string) => api<Approval>(`/approvals/${encodeURIComponent(id)}`),
  decideApproval: (id: string, request: DecideRequest) =>
    api<Approval>(`/approvals/${encodeURIComponent(id)}/decide`, { method: "POST", body: request }),
  inboxSummary: () => api<InboxSummary>("/inbox/summary"),
  // ---- The one attention queue and the Now view (DESIGN.md 22) ----
  attention: () => api<AttentionSnapshot>("/attention"),
  /** Hides an item from the count for 10 minutes, on every device; 404 when it was resolved meanwhile. */
  snoozeItem: (id: string) => api<AttentionSnapshot>("/attention/snooze", { method: "POST", body: { id } }),
  unsnoozeItem: (id: string) => api<AttentionSnapshot>("/attention/unsnooze", { method: "POST", body: { id } }),
  /** A failed job leaves the queue (it stays on the board); 409 for anything else. */
  dismissItem: (id: string) => api<AttentionSnapshot>("/attention/dismiss", { method: "POST", body: { id } }),
  now: () => api<NowResponse>("/now"),
  nowSince: (browser: string) => api<SinceResponse>(`/now/since?browser=${encodeURIComponent(browser)}`),
  nowBeat: (browser: string) => api<{ ok: boolean }>("/now/beat", { method: "POST", body: { browser } }),
  nowSeen: (browser: string) => api<{ ok: boolean }>("/now/seen", { method: "POST", body: { browser } }),
  // ---- M7: jobs ----
  jobs: () => api<Job[]>("/jobs"),
  job: (id: string) => api<JobDetail>(`/jobs/${encodeURIComponent(id)}`),
  createJob: (request: CreateJobRequest) => api<Job>("/jobs", { method: "POST", body: request }),
  /** Types the human's message into the job's agent; errors like a Chat send (409 BLOCKED, DRAFT). */
  jobMessage: (id: string, text: string, forceOverDraft = false, attachments?: string[]) =>
    api<PromptResult>(`/jobs/${encodeURIComponent(id)}/message`, {
      method: "POST",
      body: { text, force_over_draft: forceOverDraft, ...(attachments && attachments.length > 0 ? { attachments } : {}) },
    }),
  jobAction: (id: string, action: "cancel" | "nudge" | "resume") => api<Job>(`/jobs/${encodeURIComponent(id)}/${action}`, { method: "POST" }),
  roles: () => api<RoleInfo[]>("/roles"),
  /** What each engine gives a job (DESIGN.md 20.1). */
  engines: () => api<EngineInfo[]>("/engines"),
  dirs: () => api<DirEntry[]>("/dirs"),
  // ---- The folder picker (DESIGN.md 4.23) ----
  browse: (path: string, options: BrowseOptions = {}) =>
    api<Listing>(`/browse?path=${encodeURIComponent(path)}${options.files ? "&files=1" : ""}${options.hidden ? "&hidden=1" : ""}`),
  browseFacts: (path: string) => api<FolderFacts>(`/browse/facts?path=${encodeURIComponent(path)}`),
  browsePlaces: () => api<BrowsePlaces>("/browse/places"),
  // ---- Landing, throttling, the pause, presence, the digest (DESIGN.md 6.11 to 6.14, 7.12) ----
  review: (id: string) => api<ReviewView>(`/jobs/${encodeURIComponent(id)}/review`),
  reviewDiff: (id: string, path: string) => api<FileDiffView>(`/jobs/${encodeURIComponent(id)}/review/diff?path=${encodeURIComponent(path)}`),
  /** head is the commit the Review tab showed (R5-S4): what lands is what was reviewed. */
  land: (id: string, action: LandAction, head?: string) =>
    api<LandResult>(`/jobs/${encodeURIComponent(id)}/land`, { method: "POST", body: { action, confirm: true, ...(head ? { expected_head: head } : {}) } }),
  teardown: (id: string) => api<LandResult>(`/jobs/${encodeURIComponent(id)}/teardown`, { method: "POST", body: { confirm: true } }),
  startAnyway: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/start-anyway`, { method: "POST" }),
  // ---- Acceptance, budgets, wake-ups, the usage limit, receipt (DESIGN.md 6.15 to 6.19) ----
  landOverride: (id: string, action: LandAction, head?: string) =>
    api<LandResult>(`/jobs/${encodeURIComponent(id)}/land`, { method: "POST", body: { action, confirm: true, override: true, ...(head ? { expected_head: head } : {}) } }),
  recheck: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/recheck`, { method: "POST" }),
  rebase: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/rebase`, { method: "POST" }),
  resolveChanges: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/changes/resolve`, { method: "POST" }),
  setBudget: (id: string, budget: BudgetInput) => api<Job>(`/jobs/${encodeURIComponent(id)}/budget`, { method: "PUT", body: budget }),
  finishStep: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/budget/finish-step`, { method: "POST" }),
  wake: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/wake`, { method: "POST" }),
  limits: () => api<FleetLimit[]>("/limits"),
  clearLimit: (engine: string) => api<{ engine: string; released: number }>(`/limits/${encodeURIComponent(engine)}/clear`, { method: "POST" }),
  waits: () => api<WaitsView>("/system/waits"),
  projectAccept: (id: number) => api<ProjectAccept>(`/projects/${id}/accept`),
  setProjectAccept: (id: number, value: Pick<ProjectAccept, "check_command" | "budget_usd" | "budget_minutes" | "check_isolated" | "check_prepare">) =>
    api<ProjectAccept>(`/projects/${id}/accept`, { method: "PUT", body: value }),
  reconcileAnswer: (id: string) => api<Question>(`/questions/${encodeURIComponent(id)}/reconcile`, { method: "POST" }),
  retypeAnswer: (id: string) => api<Question>(`/questions/${encodeURIComponent(id)}/retype`, { method: "POST", body: { confirm: true } }),
  sweep: () => api<SweepItem[]>("/sweep"),
  sweepDiscard: (ids: string[]) =>
    api<Array<{ id: string; ok: boolean; torn_down: boolean; error?: string }>>("/sweep/discard", { method: "POST", body: { ids, confirm: true } }),
  queue: () => api<QueueLine>("/queue"),
  pause: () => api<PauseState>("/pause"),
  setPause: (until: string, reason: string) => api<PauseState>("/pause", { method: "POST", body: { ...(until ? { until } : {}), reason } }),
  resume: () => api<PauseState>("/resume", { method: "POST" }),
  throttle: () => api<ThrottleView>("/throttle"),
  saveThrottle: (settings: ThrottleSettings) => api<ThrottleSettings>("/throttle", { method: "PUT", body: settings }),
  resetThrottle: () => api<ThrottleSettings>("/throttle", { method: "DELETE" }),
  heavy: () => api<HeavyStatus>("/system/heavy"),
  presence: () => api<PresenceState>("/presence"),
  heartbeat: (beat: { client: string; visible: boolean; focused: boolean; idle_seconds: number }) =>
    api<PresenceState>("/presence", { method: "POST", body: beat }),
  setDND: (until: string) => api<PresenceState>("/presence/dnd", { method: "PUT", body: { until: until || "0001-01-01T00:00:00Z" } }),
  digests: () => api<Digest[]>("/digests"),
  digestSettings: () => api<DigestSettings>("/digest/settings"),
  saveDigestSettings: (settings: DigestSettings) => api<DigestSettings>("/digest/settings", { method: "PUT", body: settings }),
  digestNow: () => api<DigestStart>("/digest/now", { method: "POST" }),
  // ---- M8: pokes and the journal ----
  /** "Poke now": the orchestrator is poked if anything is due and the gate allows it. */
  poke: () => api<PokeResult>("/orchestrator/poke", { method: "POST" }),
  /** A page of the journal, newest first; `before` is the previous page's next_before. */
  journal: (kinds: JournalKind[], before?: number) => {
    const query = new URLSearchParams();
    if (kinds.length > 0) query.set("kind", kinds.join(","));
    if (before) query.set("before", String(before));
    const text = query.toString();
    return api<JournalPage>(`/journal${text ? `?${text}` : ""}`);
  },
  /** "Restart with new instructions": the agent resumes with a fresh prompt file. */
  jobRestartAgent: (id: string) => api<Job>(`/jobs/${encodeURIComponent(id)}/restart-agent`, { method: "POST" }),
  // ---- Projects and their leads (21.1, 21.2) ----
  projects: (archived = false) => api<ProjectList>(`/projects${archived ? "?archived=1" : ""}`),
  /** One project by id or slug; `opened` when the human opens it (applies lead_start=open). */
  project: (ref: string, opened = false) => api<ProjectView>(`/projects/${encodeURIComponent(ref)}${opened ? "?open=1" : ""}`),
  createProject: (input: ProjectCreateInput) => api<ProjectView>("/projects", { method: "POST", body: input }),
  trustProject: (ref: string) => api<ProjectView>(`/projects/${encodeURIComponent(ref)}/trust`, { method: "POST" }),
  /** Allows the project folder's own .codex/ for Codex jobs, as the page showed it (R9-S5). */
  allowCodexConfig: (ref: string, fingerprint: string) =>
    api<ProjectView>(`/projects/${encodeURIComponent(ref)}/codex-config`, { method: "POST", body: { fingerprint } }),
  updateProject: (ref: string, input: ProjectInput) => api<ProjectView>(`/projects/${encodeURIComponent(ref)}`, { method: "PUT", body: input }),
  leads: () => api<LeadStatus[]>("/leads"),
  /** Start, stop, restart, new conversation: the lead's status. Poke: a PokeResult. */
  leadAction: (ref: string, action: LeadAction) =>
    api<LeadStatus | PokeResult>(`/projects/${encodeURIComponent(ref)}/lead/${action}`, { method: "POST" }),
  // ---- Instruction layers (21.3) ----
  instructions: () => api<InstructionsList>("/instructions"),
  /** One layer with its revisions; key "-" for global, the project's id for a project. */
  instructionLayer: (scope: LayerScope, key: string) => api<LayerDetail>(`/instructions/${scope}/${encodeURIComponent(key)}`),
  /** 409 for a role that comes from a file (read-only). Empty text clears a global or project layer. */
  saveInstruction: (scope: LayerScope, key: string, body: string) =>
    api<LayerDetail>(`/instructions/${scope}/${encodeURIComponent(key)}`, { method: "PUT", body: { body_md: body } }),
  /** Back to the built-in text (roles only). */
  roleFileSeen: (name: string, hash: string) => api<LayerDetail>(`/instructions/role/${encodeURIComponent(name)}/file-seen`, { method: "POST", body: { hash } }),
  resetRole: (name: string) => api<LayerDetail>(`/instructions/role/${encodeURIComponent(name)}`, { method: "DELETE" }),
  /** Let a layer a restore brought back reach agents (R7-S4); hash is the restored_hash shown. 409 when it changed. */
  applyRestoredInstruction: (scope: LayerScope, key: string, hash: string) =>
    api<LayerDetail>(`/instructions/${scope}/${encodeURIComponent(key)}/apply-restored`, { method: "POST", body: { hash } }),
  restoreInstruction: (scope: LayerScope, key: string, revision: number) =>
    api<LayerDetail>(`/instructions/${scope}/${encodeURIComponent(key)}/restore`, { method: "POST", body: { revision } }),
  /** What an agent was started with: j_<n>, orchestrator or lead:<slug>. */
  agentInstructions: (subject: string) => api<AgentInstructions>(`/agents-instructions/${encodeURIComponent(subject)}`),
  // ---- Scheduled work and the orchestrator's context (7.8, 7.9) ----
  /** Every task, or one project's (0: the orchestrator's and unfiled tasks). */
  schedule: (projectId?: number) => api<ScheduleView>(`/schedule${projectId === undefined ? "" : `?project=${projectId}`}`),
  /** A new app schedule; 409 when the name is taken. Answers with the full list. */
  scheduleCreate: (input: ScheduleInput) => api<ScheduleView>("/schedule", { method: "POST", body: input }),
  /** The full edit of an app schedule (files are 409), with its switch. */
  scheduleUpdate: (name: string, input: ScheduleInput & { enabled?: boolean }) =>
    api<ScheduleView>(`/schedule/${encodeURIComponent(name)}`, { method: "PUT", body: input }),
  scheduleDelete: (name: string) => api<ScheduleView>(`/schedule/${encodeURIComponent(name)}`, { method: "DELETE" }),
  /** Run now: a poke task goes with the next poke, a job task starts its job. */
  scheduleApprove: (name: string, hash: string) => api<ScheduleView>(`/schedule/${encodeURIComponent(name)}/approve`, { method: "POST", body: { hash } }),
  scheduleRun: (name: string) => api<ScheduleView>(`/schedule/${encodeURIComponent(name)}/run`, { method: "POST" }),
  scheduleSet: (name: string, enabled: boolean) => api<ScheduleView>(`/schedule/${encodeURIComponent(name)}`, { method: "PUT", body: { enabled } }),
  orchestratorContext: () => api<ContextInfo>("/orchestrator/context"),
  // ---- Spend (7.11) ----
  spendSummary: () => api<SpendPeriod>("/spend/summary"),
  spendReport: (group: SpendGroup, from?: string, to?: string) => {
    const query = new URLSearchParams({ group });
    if (from) query.set("from", from);
    if (to) query.set("to", to);
    return api<SpendReport>(`/spend?${query.toString()}`);
  },
  /** Compact now: 409 CONFLICT when the orchestrator is busy or has a draft. */
  orchestratorCompact: () => api<ContextInfo>("/orchestrator/compact", { method: "POST" }),
  // ---- M11: talk mode (every route is 404 when voice is off) ----
  /** Uploads one recording as it came from MediaRecorder; returns what was said. */
  transcribe: (audio: Blob) => uploadAudio(audio),
  /** A question's title and options, made speakable; play it through speechUrl. */
  readAloud: (questionId: string) => api<{ text_id: string }>("/voice/read", { method: "POST", body: { question_id: questionId } }),
  /** A reply's SAY line, made playable (Chat's play button). 404 when it has none. */
  replySay: (target: string, messageId: string) =>
    api<{ text_id: string; text: string }>("/voice/reply", { method: "POST", body: { target, message_id: messageId } }),
  voiceSettings: () => api<VoiceSettings>("/voice/settings"),
  saveVoiceSettings: (settings: VoiceSettingsChange) => api<VoiceSettings>("/voice/settings", { method: "PUT", body: settings }),
  voices: () => api<VoiceInfo[]>("/voice/voices"),
  /** The standing agents and the voice each speaks in ("" = the instance voice). */
  voiceAgents: () => api<AgentVoices>("/voice/agents"),
  setAgentVoice: (agent: string, voiceId: string) => api<AgentVoices>(`/voice/agents/${encodeURIComponent(agent)}`, { method: "PUT", body: { voice_id: voiceId } }),
  /** A sample sentence in a voice, speed and model not yet saved. */
  previewVoice: (settings: VoiceSettingsChange) => api<{ text_id: string; playback_rate: number }>("/voice/preview", { method: "POST", body: settings }),
  // ---- Web Push (DESIGN.md 8.5) ----
  pushKey: () => api<{ public_key: string }>("/push/key"),
  pushSettings: () => api<PushSettings>("/push/settings"),
  updatePushSettings: (kinds: Partial<Record<PushKind, boolean>>) => api<PushSettings>("/push/settings", { method: "PUT", body: { kinds } }),
  pushSubscribe: (subscription: PushSubscriptionJSON) => api<PushDevice>("/push/subscriptions", { method: "POST", body: subscription }),
  /** This browser turning notifications off (by its endpoint). */
  pushUnsubscribe: (endpoint: string) => api<{ removed: boolean }>("/push/subscriptions", { method: "DELETE", body: { endpoint } }),
  /** Removes a device from the list (any device). */
  pushRemoveDevice: (id: number) => api<{ removed: boolean }>(`/push/subscriptions/${id}`, { method: "DELETE" }),
  /** A test notification: to this browser when endpoint is given, else to every device. */
  pushTest: (endpoint?: string) => api<PushTestResult>("/push/test", { method: "POST", body: endpoint ? { endpoint } : {} }),
};

/** GET/PUT /voice/settings: how replies are spoken, and where the voice came from. */
export interface VoiceSettings {
  voice_id: string;
  /** "settings" when chosen in the app; else the configuration that set the default. */
  voice_source: string;
  default_voice_id: string;
  speed: number;
  speed_min: number;
  speed_max: number;
  /** The browser's share of speed: ElevenLabs speaks at most 1.2x, the rest is playbackRate. */
  playback_rate: number;
  model: string;
  models: { id: string; model_id: string; label: string }[];
}

/** PUT /voice/settings: an empty voice_id means the configured default. */
export interface VoiceSettingsChange {
  voice_id: string;
  speed: number;
  model: string;
}

/** GET /voice/agents: each standing agent's own voice. */
export interface AgentVoices {
  agents: AgentVoice[];
  /** The instance voice, which an agent without its own speaks in. */
  default_voice: string;
}

export interface AgentVoice {
  /** "orchestrator" or "lead-<slug>". */
  agent: string;
  label: string;
  /** "" means the instance voice. */
  voice_id: string;
}

/** One ElevenLabs voice of the account. */
export interface VoiceInfo {
  voice_id: string;
  name: string;
  category?: string;
  description?: string;
  labels?: Record<string, string>;
}

/** Where an <audio> element fetches a server-held text's speech. */
export function speechUrl(textId: string): string {
  return `${API_BASE}/voice/tts?text_id=${encodeURIComponent(textId)}`;
}

/**
 * POST /voice/stt: the body is the raw recording with its own content type, so it cannot go
 * through api(), which sends JSON. The headers and error handling are the same.
 */
async function uploadAudio(audio: Blob): Promise<{ text: string }> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE}/voice/stt`, {
      method: "POST",
      headers: { Accept: "application/json", "X-Crowsnest": "1", "Content-Type": audio.type || "audio/webm" },
      credentials: "same-origin",
      body: audio,
    });
  } catch {
    throw new ApiError(0, "NETWORK_ERROR", "Cannot reach crowsnest. Check the connection.");
  }
  const payload = await readJSON(response);
  if (!response.ok) {
    if (response.status === 401) unauthorizedHandler?.();
    const error = (payload as { error?: { code?: string; message?: string } } | null)?.error;
    throw new ApiError(response.status, error?.code ?? "HTTP_" + response.status, error?.message ?? response.statusText);
  }
  // A body without its text (a proxy's HTML page, a truncated reply) is a failure the recorder
  // can show, not a TypeError that left "Transcribing…" up for good (R10 Fable).
  const data = (payload as { data?: { text?: unknown } } | null)?.data;
  if (!data || typeof data.text !== "string") {
    throw new ApiError(response.status, "BAD_RESPONSE", "The transcription came back unreadable. Try again.");
  }
  return { text: data.text };
}

/** The terminal WebSockets (lib/termSocket.ts opens them). */
export const terminalPaths = {
  tui: `${API_BASE}/term/tui`,
  pane: (paneId: string) => `${API_BASE}/term/pane/${encodeURIComponent(paneId)}`,
};

/**
 * TanStack Query keys, in one place so invalidation never misses a spelling. Everything about
 * one agent sits under ["agents", target] so a single invalidation refreshes all of it.
 */
export const queryKeys = {
  health: ["health"] as const,
  me: ["me"] as const,
  account: ["account"] as const,
  fleet: ["fleet"] as const,
  agents: ["agents"] as const,
  agent: (target: string) => ["agents", target] as const,
  agentScreen: (target: string) => ["agents", target, "screen"] as const,
  agentMessages: (target: string) => ["agents", target, "messages"] as const,
  orchestrator: ["orchestrator"] as const,
  /** Under ["orchestrator"], so an `orchestrator` event refreshes it too. */
  orchestratorContext: ["orchestrator", "context"] as const,
  schedule: ["schedule"] as const,
  /** Under ["schedule"], so a `schedule` event refreshes it too. */
  scheduleProject: (projectId: number) => ["schedule", "project", projectId] as const,
  /** Everything about projects sits under ["projects"]; `project`, `lead` and `job` events refresh it. */
  projects: ["projects"] as const,
  projectList: (archived: boolean) => ["projects", "list", archived] as const,
  project: (ref: string) => ["projects", "one", ref] as const,
  leads: ["projects", "leads"] as const,
  /** Everything about instruction layers sits under ["instructions"]; an `instructions` event refreshes it. */
  instructions: ["instructions"] as const,
  instructionList: ["instructions", "list"] as const,
  instructionLayer: (scope: string, key: string) => ["instructions", "layer", scope, key] as const,
  agentInstructions: (subject: string) => ["instructions", "agent", subject] as const,
  voiceAgents: ["voice", "agents"] as const,
  /** Everything about jobs sits under ["jobs"], so one `job` event refreshes the board and the detail. */
  jobs: ["jobs"] as const,
  jobList: ["jobs", "list"] as const,
  job: (id: string) => ["jobs", "one", id] as const,
  review: (id: string) => ["jobs", "review", id] as const,
  reviewDiff: (id: string, path: string) => ["jobs", "review", id, "diff", path] as const,
  sweep: ["jobs", "sweep"] as const,
  queue: ["jobs", "queue"] as const,
  pause: ["pause"] as const,
  throttle: ["throttle"] as const,
  limits: ["limits"] as const,
  waits: ["system", "waits"] as const,
  projectAccept: (id: number) => ["projects", "accept", id] as const,
  presence: ["presence"] as const,
  digests: ["digests"] as const,
  digestSettings: ["digests", "settings"] as const,
  roles: ["roles"] as const,
  engines: ["engines"] as const,
  dirs: ["dirs"] as const,
  browse: (path: string, options: BrowseOptions) => ["browse", path, !!options.files, !!options.hidden] as const,
  browseFacts: (path: string) => ["browse-facts", path] as const,
  browsePlaces: ["browse-places"] as const,
  /** Everything about questions sits under ["questions"], so one `question` event refreshes it all. */
  questions: ["questions"] as const,
  questionList: (status: string) => ["questions", "list", status] as const,
  question: (id: string) => ["questions", "one", id] as const,
  inboxSummary: ["questions", "summary"] as const,
  /** Everything about approvals sits under ["approvals"]; an `approval` event refreshes it. */
  approvals: ["approvals"] as const,
  approvalList: (status: string) => ["approvals", "list", status] as const,
  approval: (id: string) => ["approvals", "one", id] as const,
  /** Everything about the journal sits under ["journal"]; a `journal` or `poke` event refreshes it. */
  journal: ["journal"] as const,
  journalPage: (kinds: string) => ["journal", kinds] as const,
  /** Everything about spend sits under ["spend"]; a `spend` event refreshes it (and the jobs). */
  spend: ["spend"] as const,
  /** The attention queue: written from the `attention` event, never refetched on other events. */
  attention: ["attention"] as const,
  /** The Now view's rows and "since you left"; refreshed by job, question, approval and lead events. */
  now: ["now"] as const,
  nowSince: (browser: string) => ["now", "since", browser] as const,
  spendSummary: ["spend", "summary"] as const,
  spendReport: (group: string, from: string, to: string) => ["spend", "report", group, from, to] as const,
  /** Everything about attachments sits under ["attachments"]; an `attachment` event refreshes it. */
  attachments: ["attachments"] as const,
  attachmentList: (owner: string, role = "") => ["attachments", "list", owner, role] as const,
  attachmentMeta: (id: string) => ["attachments", "meta", id] as const,
};
