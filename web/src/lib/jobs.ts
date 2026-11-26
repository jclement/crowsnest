// Jobs (DESIGN.md section 6, the Jobs rows of 10 and 12): the TypeScript mirror of the Go shapes
// in internal/jobs/types.go, and the pure helpers the Jobs view uses: board sections and their
// order, state labels and tones, and which one action fits a stuck job.
import type { ChipTone } from "../components/ui/Chip";
import type { SpendSummary, SpendTurn } from "./spend";

export type JobState = "queued" | "running" | "needs_you" | "blocked" | "in_review" | "done" | "failed" | "cancelled";

export type JobCause =
  | ""
  | "questions"
  | "dialog"
  | "unresponsive"
  | "permissions"
  | "limit"
  | "server_error"
  | "auth"
  | "silent"
  | "idle"
  | "exited"
  | "launch"
  | "config"
  | "approval";

/** A job as GET /jobs and GET /jobs/{id} return it. */
export interface Job {
  id: string;
  title: string;
  brief_md: string;
  done_when: string;
  role: string;
  dir: string;
  worktree_branch: string;
  work_dir: string;
  engine: string;
  model: string;
  parent_id?: string;
  state: JobState;
  stuck: boolean;
  cause: JobCause;
  cause_text: string;
  cause_detail: string;
  blocked_reason: string;
  waiting: boolean;
  progress: string;
  doing_now: string;
  last_tool: string;
  result_md: string;
  verified: string[];
  not_verified: string[];
  fail_reason: string;
  pane_id: string;
  agent_name: string;
  claude_session_id: string;
  created_at: string;
  started_at: string | null;
  settled_at: string | null;
  last_activity_at: string | null;
  nudges: number;
  restarts: number;
  retry_at: string | null;
  retry_count: number;
  open_questions: number;
  isolate: boolean;
  /** Who created it: "" the human, "agent" a job agent, "orchestrator" (R2-H1). Optional in fixtures. */
  origin?: "" | "agent" | "orchestrator";
  /** What the job's agent has used (7.11); absent on a server that measures nothing. */
  spend?: SpendSummary;
}

export interface JobEvent {
  id: number;
  at: string;
  kind: string;
  summary: string;
  detail?: Record<string, unknown>;
}

export interface JobQuestion {
  id: string;
  title: string;
  kind: string;
  status: string;
  urgency: string;
  wait: boolean;
  answer_value: unknown;
  answer_note: string;
  created_at: string;
  /** The choices (key and label), so an answer shows by its label. Empty for ack, manual, notice. */
  options: Array<{ key: string; label: string }>;
}

export interface JobDetail extends Job {
  timeline: JobEvent[];
  questions: JobQuestion[];
  /** The latest model calls, oldest first, for the Spend chart. */
  spend_turns?: SpendTurn[];
}

/** The `job` SSE event's data. */
export interface JobEventData {
  id: string;
  state: JobState;
  stuck: boolean;
  cause: JobCause;
  title: string;
  change: string;
}

export interface CreateJobRequest {
  brief: string;
  dir?: string;
  role?: string;
  done_when?: string;
  engine?: string;
  model?: string;
  /** Uploads (a_…) given with the job: copied into its inbox and listed in its brief (8.6). */
  attachments?: string[];
}

export interface RoleInfo {
  name: string;
  summary: string;
  custom: boolean;
}

export interface DirEntry {
  path: string;
  label: string;
  source: "remembered" | "workspace";
}

// ---- Labels --------------------------------------------------------------------------------

export const stateLabels: Record<JobState, string> = {
  queued: "Queued",
  running: "Running",
  needs_you: "Needs you",
  blocked: "Blocked",
  in_review: "In review",
  done: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

/** The chip for a job: a stuck running job shows "Stuck" in warning colours. */
export function stateChip(job: Pick<Job, "state" | "stuck">): { label: string; tone: ChipTone } {
  if (job.stuck && !isFinished(job.state)) return { label: "Stuck", tone: "warning" };
  const tones: Record<JobState, ChipTone> = {
    queued: "neutral",
    running: "accent",
    needs_you: "danger",
    blocked: "warning",
    in_review: "accent",
    done: "success",
    failed: "danger",
    cancelled: "neutral",
  };
  return { label: stateLabels[job.state], tone: tones[job.state] };
}

export function isFinished(state: JobState): boolean {
  return state === "done" || state === "failed" || state === "cancelled";
}

// ---- The board -----------------------------------------------------------------------------

export type BoardSection = "attention" | "running" | "today" | "older";

export const sectionLabels: Record<BoardSection, string> = {
  attention: "Needs you or stuck",
  running: "In progress",
  today: "Finished today",
  older: "Older",
};

/** Which section of the board a job sits in (DESIGN.md 12: needs-you and stuck first, then
 * running, then finished today; older finished ones behind a toggle). */
export function sectionOf(job: Job, now: Date = new Date()): BoardSection {
  if (isFinished(job.state)) {
    const settled = new Date(job.settled_at ?? job.created_at);
    return sameDay(settled, now) ? "today" : "older";
  }
  // A job an agent asked for that waits on the human's approval needs them as much as needs_you.
  if (job.state === "needs_you" || job.stuck || job.cause === "approval") return "attention";
  return "running";
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

/** Groups and orders the board. Attention: needs-you before stuck, oldest wait first; running:
 * most recently active first; finished: newest first. */
export function boardSections(jobs: Job[], now: Date = new Date()): Record<BoardSection, Job[]> {
  const sections: Record<BoardSection, Job[]> = { attention: [], running: [], today: [], older: [] };
  for (const job of jobs) sections[sectionOf(job, now)].push(job);
  const time = (iso: string | null | undefined) => (iso ? Date.parse(iso) : 0);
  sections.attention.sort((a, b) => {
    if ((a.state === "needs_you") !== (b.state === "needs_you")) return a.state === "needs_you" ? -1 : 1;
    return time(a.last_activity_at ?? a.created_at) - time(b.last_activity_at ?? b.created_at);
  });
  sections.running.sort((a, b) => time(b.last_activity_at ?? b.created_at) - time(a.last_activity_at ?? a.created_at));
  const settled = (a: Job, b: Job) => time(b.settled_at ?? b.created_at) - time(a.settled_at ?? a.created_at);
  sections.today.sort(settled);
  sections.older.sort(settled);
  return sections;
}

// ---- Stuck: the one action that fits ---------------------------------------------------------

export type JobAction = "terminal" | "retry" | "nudge" | "inbox" | "resume";

/** The one action that fits why a job is stuck or needs Jeff, or null when none is needed. */
export function actionFor(job: Pick<Job, "state" | "stuck" | "cause">): JobAction | null {
  if (isFinished(job.state)) return null;
  switch (job.cause) {
    case "dialog":
    case "auth":
    case "config":
    case "launch":
    case "silent":
      return "terminal";
    case "limit":
    case "server_error":
      return "retry";
    case "unresponsive":
    case "idle":
    case "permissions":
      return "nudge";
    case "questions":
    case "approval":
      return "inbox";
    case "exited":
      return "resume";
    default:
      return null;
  }
}

export const actionLabels: Record<JobAction, string> = {
  terminal: "Open terminal",
  retry: "Retry now",
  nudge: "Nudge",
  inbox: "Answer in Inbox",
  resume: "Restart agent",
};

/** "Retry in 4 min" for a scheduled retry, or "" when none is due. */
export function retryLabel(job: Pick<Job, "retry_at" | "stuck">, now: number = Date.now()): string {
  if (!job.stuck || !job.retry_at) return "";
  const seconds = Math.round((Date.parse(job.retry_at) - now) / 1000);
  if (seconds <= 0) return "Retrying now";
  if (seconds < 60) return `Retry in ${seconds} s`;
  if (seconds < 3600) return `Retry in ${Math.round(seconds / 60)} min`;
  return `Retry in ${Math.round(seconds / 3600)} h`;
}

/** The answer of a job question in words. */
/**
 * A settled question of a job in a few words, worded as the Inbox words it (lib/questions.ts):
 * the chosen options by label, the note after them; a manual answer is its note.
 */
export function jobAnswerText(question: JobQuestion): string {
  switch (question.status) {
    case "answered":
      break;
    case "expired":
      return "expired with no answer";
    default:
      return question.status;
  }
  if (question.kind === "manual") return question.answer_note;
  const keys = Array.isArray(question.answer_value) ? question.answer_value.map(String) : question.answer_value ? [String(question.answer_value)] : [];
  const fixed: Record<string, string> = { ack: "Acknowledged", notice: "Dismissed" };
  const value = fixed[question.kind] ?? keys.map((key) => question.options?.find((option) => option.key === key)?.label ?? key).join(", ");
  return question.answer_note ? `${value} (note: ${question.answer_note})` : value;
}

/** A card's second line: why it is stuck, else what it is doing, else how it ended. */
export function cardLine(job: Job): string {
  if (job.state === "failed") return job.fail_reason;
  if (job.state === "done") return firstLine(job.result_md);
  if (job.state === "blocked") return `Blocked: ${job.blocked_reason}`;
  if ((job.stuck || job.state === "needs_you" || job.cause === "approval") && job.cause_text)
    return job.cause_text + (job.cause_detail ? `: ${job.cause_detail}` : "");
  return job.doing_now;
}

function firstLine(markdown: string): string {
  return markdown.split("\n").find((line) => line.trim())?.replace(/^[#>*\-\s]+/, "") ?? "";
}
