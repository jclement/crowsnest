// Scheduled work and the orchestrator's context (DESIGN.md 7.8, 7.9 and 10): the TypeScript
// mirror of internal/schedule's View and internal/orch's ContextInfo, and the small pure helpers
// the Schedule panel and the Chat menu draw with.

/** One task of GET /schedule (internal/schedule TaskView). */
export interface ScheduleTask {
  name: string;
  title: string;
  path: string;
  origin: "dir" | "user";
  every: string;
  after?: string;
  /** The cadence in words: "every 15m", "every 1d after 17:00", "with every poke". */
  schedule: string;
  as: "poke" | "job";
  dir?: string;
  role?: string;
  enabled: boolean;
  last_run_at: string | null;
  /** Null: rides on pokes, waits for a change, or is switched off. */
  next_due_at: string | null;
  last_outcome?: string;
  job_id?: string;
  job_state?: string;
  /** Run now was pressed and has not been carried out yet. */
  run_now: boolean;
}

/** A task file that could not be used. */
export interface ScheduleProblem {
  path: string;
  error: string;
}

/** GET /schedule. */
export interface ScheduleView {
  tasks: ScheduleTask[];
  problems: ScheduleProblem[];
  /** Where task files are read from, in order. */
  dirs: string[];
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
