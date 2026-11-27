// Scheduled work and the orchestrator's context (DESIGN.md 7.8, 7.9 and 10): the TypeScript
// mirror of internal/schedule's View and internal/orch's ContextInfo, and the small pure helpers
// the Schedule panel and the Chat menu draw with.

/** Who a task speaks to: a poke for the orchestrator or a project's lead, or a job of its own. */
export type ScheduleTarget = "orchestrator" | "lead" | "job";

/** One run of a task, newest first in `runs`. */
export interface ScheduleRun {
  at: string;
  outcome: string;
  job_id?: string;
}

/** One task of GET /schedule (internal/schedule TaskView). */
export interface ScheduleTask {
  name: string;
  title: string;
  path: string;
  /** "app": made in the app (editable here); "dir"/"user": a task file (read-only here). */
  origin: "app" | "dir" | "user";
  /** An app schedule: the app may edit and delete it. Optional in older fixtures. */
  editable?: boolean;
  every: string;
  after?: string;
  /** The cadence in words: "every 15m", "every 1d after 17:00", "with every poke". */
  schedule: string;
  as: "poke" | "job";
  /** Optional in older fixtures, which predate projects. */
  target?: ScheduleTarget;
  project_id?: number;
  /** The project's slug. */
  project?: string;
  dir?: string;
  role?: string;
  engine?: string;
  /** App schedules, and task files waiting for review (what the human approves). */
  body_md?: string;
  /**
   * A task file that starts a job or pokes a lead runs only once the human approves its exact
   * content (R5-S3): "needed" (new or changed, does not run) or "approved". Absent: not reviewed.
   */
  review?: "needed" | "approved";
  /** The content hash Approve must name. */
  hash?: string;
  /** An app schedule hidden by a task file of the same name: it never runs. */
  shadowed?: boolean;
  enabled: boolean;
  last_run_at: string | null;
  /** Null: rides on pokes, waits for a change, or is switched off. */
  next_due_at: string | null;
  last_outcome?: string;
  job_id?: string;
  job_state?: string;
  /** Run now was pressed and has not been carried out yet. */
  run_now: boolean;
  /** The latest runs, newest first (at most 20). */
  runs?: ScheduleRun[];
}

/** A task file (or app schedule) that could not be used. */
export interface ScheduleProblem {
  path?: string;
  name?: string;
  error: string;
}

/** GET /schedule. */
export interface ScheduleView {
  tasks: ScheduleTask[];
  problems: ScheduleProblem[];
  /** Where task files are read from, in order. */
  dirs: string[];
}

/** POST /schedule and the full edit of PUT /schedule/{name} (internal/schedule ScheduleInput). */
export interface ScheduleInput {
  name: string;
  every: string;
  after: string;
  as: "poke" | "job";
  /** poke: 0 the orchestrator, else that project's lead; job: the job's project (0 none). */
  project_id: number;
  /** The instruction (poke) or the brief (job). */
  body_md: string;
  /** job only. */
  dir: string;
  role: string;
  engine: string;
}

// ---- The cadence picker ----------------------------------------------------------------------

/** The cadence in the form's words. */
export type Cadence =
  | { kind: "minutes"; count: number }
  | { kind: "hours"; count: number }
  | { kind: "days"; count: number }
  | { kind: "daily"; at: string }
  | { kind: "poke" }
  | { kind: "change" };

export type CadenceKind = Cadence["kind"];

/** The shortest interval the server accepts, in minutes. */
export const MIN_MINUTES = 5;

export const cadenceLabels: Record<CadenceKind, string> = {
  minutes: "Every N minutes",
  hours: "Every N hours",
  days: "Every N days",
  daily: "Daily at a time",
  poke: "With every poke",
  change: "When something changes",
};

/** The task-file grammar for a cadence: every N minutes → "Nm", daily at 17:00 → "1d" after "17:00". */
export function cadenceToEvery(cadence: Cadence): { every: string; after: string } {
  switch (cadence.kind) {
    case "minutes":
      return { every: `${cadence.count}m`, after: "" };
    case "hours":
      return { every: `${cadence.count}h`, after: "" };
    case "days":
      return { every: `${cadence.count}d`, after: "" };
    case "daily":
      return { every: "1d", after: cadence.at };
    case "poke":
      return { every: "poke", after: "" };
    case "change":
      return { every: "change", after: "" };
  }
}

/** The form's cadence for a task's every/after; an unreadable one reads as hourly. */
export function everyToCadence(every: string, after = ""): Cadence {
  const value = every.trim().toLowerCase();
  if (value === "poke") return { kind: "poke" };
  if (value === "change") return { kind: "change" };
  const match = /^(\d{1,4})([mhd])$/.exec(value);
  if (!match) return { kind: "hours", count: 1 };
  const count = Number(match[1]);
  if (match[2] === "m") return { kind: "minutes", count };
  if (match[2] === "h") return { kind: "hours", count };
  if (count === 1 && after.trim()) return { kind: "daily", at: after.trim() };
  return { kind: "days", count };
}

/** Why a cadence cannot be saved, in words, or null when it can. */
export function cadenceProblem(cadence: Cadence): string | null {
  switch (cadence.kind) {
    case "minutes":
      if (!Number.isInteger(cadence.count) || cadence.count < MIN_MINUTES) return `At least every ${MIN_MINUTES} minutes.`;
      return cadence.count > 9999 ? "That is too many minutes; use hours or days." : null;
    case "hours":
    case "days":
      return !Number.isInteger(cadence.count) || cadence.count < 1 || cadence.count > 9999 ? "A whole number from 1." : null;
    case "daily":
      return /^([01]?\d|2[0-3]):[0-5]\d$/.test(cadence.at) ? null : "A time of day as HH:MM.";
    default:
      return null;
  }
}

/** A task's target, for tasks from servers that predate projects (as: job is a job, else the orchestrator). */
export function targetOf(task: Pick<ScheduleTask, "target" | "as" | "project_id">): ScheduleTarget {
  if (task.target) return task.target;
  if (task.as === "job") return "job";
  return task.project_id ? "lead" : "orchestrator";
}

/** What the target picker maps to on the wire. */
export function targetToInput(target: ScheduleTarget, projectId: number): Pick<ScheduleInput, "as" | "project_id"> {
  if (target === "orchestrator") return { as: "poke", project_id: 0 };
  if (target === "lead") return { as: "poke", project_id: projectId };
  return { as: "job", project_id: projectId };
}

/** A task's name must be a file-name-like word: letters, digits, dashes and underscores. */
export function nameProblem(name: string): string | null {
  if (!name.trim()) return "Give it a name.";
  return /^[a-z0-9][a-z0-9_-]{0,63}$/.test(name.trim()) ? null : "Lower-case letters, digits, - and _ (up to 64), starting with a letter or digit.";
}

/** GET /orchestrator/context (internal/orch ContextInfo). */
export interface ContextInfo {
  tokens: number;
  known: boolean;
  at?: string;
  compacted_at?: string;
  human_at?: string;
  /** --compact-at; 0 when compaction is off. */
  threshold: number;
  last_compact_at?: string;
  /** --fresh-at as HH:MM; absent when off. */
  fresh_at?: string;
  fresh_day?: string;
}

/** "152k" for a token count. */
export function formatTokens(tokens: number): string {
  if (tokens < 1000) return String(tokens);
  if (tokens < 10_000) return `${(tokens / 1000).toFixed(1)}k`;
  return `${Math.round(tokens / 1000)}k`;
}

/**
 * How full the context is, 0 to 100, against the compaction threshold, else against a
 * 200k-token window. Null when the size is not known.
 */
export function contextPercent(info: ContextInfo): number | null {
  if (!info.known) return null;
  const scale = info.threshold > 0 ? info.threshold : 200_000;
  return Math.max(0, Math.min(100, Math.round((info.tokens / scale) * 100)));
}

/** One line for the menu: "152k tokens · compacts at 150k". */
export function contextLine(info: ContextInfo): string {
  const size = info.known ? `${formatTokens(info.tokens)} tokens` : "size not known yet";
  const rule = info.threshold > 0 ? `compacts at ${formatTokens(info.threshold)}` : "auto-compaction off";
  return `${size} · ${rule}`;
}

/** When a task runs next, in words relative to now. */
export function nextDueText(task: ScheduleTask, relative: (iso: string) => string): string {
  if (!task.enabled) return task.run_now ? "now (Run now)" : "off";
  if (task.run_now) return "now (Run now)";
  if (task.next_due_at) {
    const when = relative(task.next_due_at);
    return when === "just now" ? "due now" : when;
  }
  return task.every === "poke" ? "with the next poke" : task.every === "change" ? "on a change" : "—";
}
