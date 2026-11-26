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
import type { AgentScreen, Fleet, KeysResponse, SendKeysRequest } from "./types";
import type { MessagePage, OrchestratorStatus, PromptRequest, PromptResult } from "./conversation";
import type { AnswerRequest, InboxSummary, Question } from "./questions";
import type { CreateJobRequest, DirEntry, Job, JobDetail, RoleInfo } from "./jobs";
import type { JournalKind, JournalPage, PokeResult } from "./journal";
import type { ContextInfo, ScheduleView } from "./schedule";
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
  inboxSummary: () => api<InboxSummary>("/inbox/summary"),
  // ---- M7: jobs ----
  jobs: () => api<Job[]>("/jobs"),
  job: (id: string) => api<JobDetail>(`/jobs/${encodeURIComponent(id)}`),
  createJob: (request: CreateJobRequest) => api<Job>("/jobs", { method: "POST", body: request }),
  /** Types the human's message into the job's agent; errors like a Chat send (409 BLOCKED, DRAFT). */
  jobMessage: (id: string, text: string, forceOverDraft = false) =>
    api<PromptResult>(`/jobs/${encodeURIComponent(id)}/message`, { method: "POST", body: { text, force_over_draft: forceOverDraft } }),
  jobAction: (id: string, action: "cancel" | "nudge" | "resume") => api<Job>(`/jobs/${encodeURIComponent(id)}/${action}`, { method: "POST" }),
  roles: () => api<RoleInfo[]>("/roles"),
  dirs: () => api<DirEntry[]>("/dirs"),
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
  // ---- Scheduled work and the orchestrator's context (7.8, 7.9) ----
  schedule: () => api<ScheduleView>("/schedule"),
  /** Run now: a poke task goes with the next poke, a job task starts its job. */
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
  return (payload as { data: { text: string } }).data;
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
  /** Everything about jobs sits under ["jobs"], so one `job` event refreshes the board and the detail. */
  jobs: ["jobs"] as const,
  jobList: ["jobs", "list"] as const,
  job: (id: string) => ["jobs", "one", id] as const,
  roles: ["roles"] as const,
  dirs: ["dirs"] as const,
  /** Everything about questions sits under ["questions"], so one `question` event refreshes it all. */
  questions: ["questions"] as const,
  questionList: (status: string) => ["questions", "list", status] as const,
  question: (id: string) => ["questions", "one", id] as const,
  inboxSummary: ["questions", "summary"] as const,
  /** Everything about the journal sits under ["journal"]; a `journal` or `poke` event refreshes it. */
  journal: ["journal"] as const,
  journalPage: (kinds: string) => ["journal", kinds] as const,
  /** Everything about spend sits under ["spend"]; a `spend` event refreshes it (and the jobs). */
  spend: ["spend"] as const,
  spendSummary: ["spend", "summary"] as const,
  spendReport: (group: string, from: string, to: string) => ["spend", "report", group, from, to] as const,
};
