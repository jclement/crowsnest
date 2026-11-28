// Projects and their leads (DESIGN.md 21): the TypeScript mirror of internal/projects (Project,
// Counts, LeadStatus) and internal/httpd/projects.go (the views GET /projects returns), plus the
// pure helpers the Projects view, the Jobs view's project switcher, Chat's target switcher and
// the command palette share: a lead's agent name, its state in words, a name suggested from a
// directory, and which jobs a project selection keeps.
import type { ChipTone } from "../components/ui/Chip";
import type { OrchestratorState } from "./conversation";
import type { Job } from "./jobs";
import type { RemoteControlFields } from "./remote";

/** When a project's lead starts by itself: never (only Start lead), when opened, or on a job for it. */
export type LeadStart = "manual" | "open" | "job";

/** A project (internal/projects Project). */
export interface Project {
  id: number;
  slug: string;
  name: string;
  /** Where its jobs run by default. */
  dir: string;
  /** The repository root the directory sits in, when it is in one. */
  root_dir: string;
  description: string;
  default_role: string;
  default_engine: string;
  default_model: string;
  /** null: the role's own default. */
  default_isolate: boolean | null;
  permission_mode: string;
  lead_start: LeadStart;
  archived: boolean;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
}

/** Jobs counted per project (internal/projects Counts). */
export interface ProjectCounts {
  needs_you: number;
  running: number;
  total: number;
}

/** A lead's state: the orchestrator's states, plus "off" when it is not running. */
export type LeadState = OrchestratorState | "off" | "stopped";

/** A lead's status (internal/projects LeadStatus: orch.Status plus the project). */
export interface LeadStatus extends RemoteControlFields {
  state: LeadState;
  /** Its herdr agent name: lead-<slug>. */
  name: string;
  pane_id: string;
  session_id: string;
  dir: string;
  model?: string;
  detail?: string;
  restarts: number;
  updated_at: string;
  project_id: number;
  slug: string;
  running: boolean;
}

/** A project as the browser sees it: with its lead and its job counts. */
export interface ProjectView extends Project {
  lead: LeadStatus;
  counts: ProjectCounts;
  /** Whether Claude Code trusts the project's folder, so its agents start without asking (8.2.1). */
  folder_trusted?: boolean;
  /** What recording the folder's trust did, on a create or POST …/trust that asked. */
  trust?: FolderTrustResult[];
  /** What the folder carries that an agent runs or reads at its start (R9-S4). */
  agent_start?: AgentStartFinding[];
  /** The folder's own .codex/ and whether it is allowed for Codex jobs as it is now (R9-S5). */
  codex_config?: CodexConfigState;
}

/** One thing in a folder an agent runs (hooks, MCP servers, Codex config) or reads at its start. */
export interface AgentStartFinding {
  path: string;
  kind: "hooks" | "mcp" | "codex" | "instructions";
  runs: boolean;
}

/** A folder's .codex/ as a Codex job would load it (internal/jobs/codexconfig.go). */
export interface CodexConfigState {
  files: string[] | null;
  fingerprint: string;
  seen: boolean;
  error?: string;
}

const findingWhat: Record<AgentStartFinding["kind"], string> = {
  hooks: "hooks",
  mcp: "MCP servers",
  codex: "Codex config",
  instructions: "instructions",
};

/** "path (what)" for a finding, as the trust box and banner list it. */
export function findingLabel(finding: AgentStartFinding): string {
  return `${finding.path} (${findingWhat[finding.kind]})`;
}

/** One agent's folder-trust record after crowsnest was asked to write it (internal/agenttrust). */
export interface FolderTrustResult {
  agent: "claude" | "codex";
  status: "trusted" | "already" | "skipped" | "failed";
  detail?: string;
}

/** GET /projects. */
export interface ProjectList {
  projects: ProjectView[];
  /** Jobs in no project. */
  unfiled: ProjectCounts;
}

/** POST /projects and PUT /projects/{ref}: every field optional on an edit. */
export interface ProjectInput {
  name?: string;
  dir?: string;
  /** Create only; derived from the name when empty. */
  slug?: string;
  description?: string;
  default_role?: string;
  default_engine?: string;
  default_model?: string;
  /** -1 the role's default, 0 no, 1 yes. */
  default_isolate?: -1 | 0 | 1;
  permission_mode?: string;
  lead_start?: LeadStart;
  archived?: boolean;
}

/** POST /projects: a project, and whether its agents may work in its folder without asking. */
export interface ProjectCreateInput extends ProjectInput {
  trust_folder?: boolean;
}

/** The lead actions the server offers (POST /projects/{ref}/lead/{action}). */
export type LeadAction = "start" | "stop" | "restart" | "new" | "poke";

/** The `project` SSE event's data. */
export interface ProjectEventData {
  id: number;
  change: string;
}

/** Claude Code's permission modes a project's agents can start in ("" is Claude's default). */
export const permissionModes = ["", "default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"] as const;

export const leadStartLabels: Record<LeadStart, string> = {
  manual: "Only when I start it",
  open: "When I open the project (or give it a job)",
  job: "When the project is given a job for its lead",
};

// ---- Leads -----------------------------------------------------------------------------------

/** The prefix of a lead's herdr agent name. */
export const LEAD_PREFIX = "lead-";

/** A lead's agent name, which is also its Chat target and its voice agent: lead-<slug>. */
export function leadAgentName(slug: string): string {
  return LEAD_PREFIX + slug;
}

/** The project slug of a lead's agent name, or null for anything else. */
export function slugOfLead(agent: string): string | null {
  return agent.startsWith(LEAD_PREFIX) && agent.length > LEAD_PREFIX.length ? agent.slice(LEAD_PREFIX.length) : null;
}

/** A lead is running (has a pane to talk to) unless it is off, stopped or failed. */
export function leadRunning(lead: Pick<LeadStatus, "state" | "running">): boolean {
  return lead.running && lead.state !== "off" && lead.state !== "stopped";
}

/** The chip for a lead's state. */
export function leadChip(lead: Pick<LeadStatus, "state" | "running">): { label: string; tone: ChipTone } {
  if (!lead.running || lead.state === "off" || lead.state === "stopped") return { label: "Lead off", tone: "neutral" };
  switch (lead.state) {
    case "idle":
      return { label: "Lead idle", tone: "success" };
    case "working":
      return { label: "Lead working", tone: "accent" };
    case "blocked":
      return { label: "Lead blocked", tone: "danger" };
    case "needs_terminal":
      return { label: "Lead needs terminal", tone: "warning" };
    case "starting":
    case "restarting":
      return { label: "Lead starting", tone: "neutral" };
    case "failed":
      return { label: "Lead failed", tone: "danger" };
    default:
      return { label: `Lead ${lead.state}`, tone: "neutral" };
  }
}

// ---- New project: a name from a directory --------------------------------------------------------

/** The last element of a path ("~/src/web-site/" → "web-site"). */
export function dirBasename(dir: string): string {
  return dir.trim().replace(/\/+$/, "").split("/").filter(Boolean).pop() ?? "";
}

/** A project name suggested from a directory: its basename, words capitalised ("web-site" → "Web site"). */
export function suggestName(dir: string): string {
  const base = dirBasename(dir).replace(/^\./, "");
  if (!base || base === "~") return "";
  const words = base.split(/[-_\s.]+/).filter(Boolean);
  if (words.length === 0) return "";
  const text = words.join(" ");
  return text.charAt(0).toUpperCase() + text.slice(1);
}

// ---- The Jobs view's project switcher -------------------------------------------------------------

/** Which jobs the board shows: every job, one project's (by slug), or the unfiled ones. */
export type ProjectFilter = { kind: "all" } | { kind: "unfiled" } | { kind: "project"; slug: string };

export const ALL_PROJECTS: ProjectFilter = { kind: "all" };

/** The localStorage key the switcher remembers its choice under (per browser). */
export const PROJECT_FILTER_KEY = "crowsnest-jobs-project";

/** The filter as one word for the URL and storage: "", "unfiled" or the slug. */
export function filterToParam(filter: ProjectFilter): string {
  return filter.kind === "all" ? "" : filter.kind === "unfiled" ? "unfiled" : filter.slug;
}

/** The filter a URL or storage word names ("" or "all" is every job). */
export function filterFromParam(param: string | null | undefined): ProjectFilter {
  const value = (param ?? "").trim();
  if (!value || value === "all") return ALL_PROJECTS;
  if (value === "unfiled") return { kind: "unfiled" };
  return { kind: "project", slug: value };
}

/** The remembered filter, or All when nothing (or nothing readable) is stored. */
export function loadProjectFilter(storage: Pick<Storage, "getItem"> | undefined = safeStorage()): ProjectFilter {
  try {
    return filterFromParam(storage?.getItem(PROJECT_FILTER_KEY));
  } catch {
    return ALL_PROJECTS;
  }
}

export function saveProjectFilter(filter: ProjectFilter, storage: Pick<Storage, "setItem"> | undefined = safeStorage()): void {
  try {
    storage?.setItem(PROJECT_FILTER_KEY, filterToParam(filter));
  } catch {
    // Storage blocked: the choice lasts for this page only.
  }
}

/**
 * The filter to use: the URL's when it names one, else the remembered one. A remembered project
 * that no longer exists (deleted, archived, renamed) falls back to All, so the board is never
 * empty for a reason the switcher does not show. A URL's project is kept even before the
 * project list has loaded.
 */
export function resolveProjectFilter(urlParam: string | undefined, remembered: ProjectFilter, projects: Pick<Project, "slug">[] | undefined): ProjectFilter {
  if (urlParam) return filterFromParam(urlParam);
  if (remembered.kind === "project" && projects && !projects.some((project) => project.slug === remembered.slug)) return ALL_PROJECTS;
  return remembered;
}

/** The project id a filter keeps: null for All, 0 for Unfiled, undefined for an unknown slug. */
export function filterProjectId(filter: ProjectFilter, projects: Pick<Project, "id" | "slug">[] | undefined): number | null | undefined {
  if (filter.kind === "all") return null;
  if (filter.kind === "unfiled") return 0;
  return projects?.find((project) => project.slug === filter.slug)?.id;
}

/** The jobs a filter keeps. An unknown project keeps none. */
export function filterJobs(jobs: Job[], filter: ProjectFilter, projects: Pick<Project, "id" | "slug">[] | undefined): Job[] {
  const id = filterProjectId(filter, projects);
  if (id === null) return jobs;
  if (id === undefined) return [];
  return jobs.filter((job) => (job.project_id ?? 0) === id);
}

/** Projects by id, for chips on job cards. */
export function projectsById<T extends Pick<Project, "id">>(projects: T[] | undefined): Map<number, T> {
  return new Map((projects ?? []).map((project) => [project.id, project]));
}

function safeStorage(): Storage | undefined {
  try {
    return typeof localStorage === "undefined" ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

// ---- Confirmations -----------------------------------------------------------------------------------

/** The words a lead's interrupting actions ask with, worded as the orchestrator's are. */
export function leadConfirmations(name: string): Record<"restart" | "new" | "stop", { title: string; body: string; button: string }> {
  return {
    restart: {
      title: `Restart ${name}'s lead?`,
      body: "Claude is stopped (anything the lead is doing is interrupted) and started again on the same conversation, with the instructions as they are now.",
      button: "Restart",
    },
    new: {
      title: `Start a new conversation for ${name}'s lead?`,
      body: "Claude is stopped and started with an empty conversation. The current one stays in its transcript file but leaves Chat.",
      button: "New conversation",
    },
    stop: {
      title: `Stop ${name}'s lead?`,
      body: "Claude is stopped and its pane closed. Its conversation is kept, so Start lead picks it up again. Jobs it holds wait until then.",
      button: "Stop lead",
    },
  };
}

// ---- Chat's target ------------------------------------------------------------------------------------

/** The orchestrator's Chat target (and API target: the server resolves the name to its pane). */
export const CHAT_ORCHESTRATOR = "orchestrator";

/** Who Chat talks to when the URL does not say: the orchestrator, else the first running lead. */
export function defaultChatTarget(orchestrator: { state: string } | undefined, projects: ProjectView[]): string {
  if (orchestrator && orchestrator.state !== "disabled") return CHAT_ORCHESTRATOR;
  const running = projects.find((project) => leadRunning(project.lead)) ?? projects[0];
  return running ? leadAgentName(running.slug) : CHAT_ORCHESTRATOR;
}
