// Landing, throttling, the pause, presence and the digest (DESIGN.md 6.11 to 6.14, 7.12): the
// TypeScript mirror of the Go shapes (internal/landing, internal/jobs/land.go, internal/pause,
// internal/throttle, internal/presence, internal/digest) and the pure helpers the views use.

/** One file the job's branch changed (internal/landing FileChange). */
export interface FileChange {
  path: string;
  old_path?: string;
  status: "added" | "modified" | "deleted" | "renamed" | "copied" | "type";
  added: number;
  deleted: number;
  binary: boolean;
  /** Reading-order group: schema, server, api, client, ui, tests, docs, config, churn. */
  group: string;
  churn: boolean;
}

export interface Commit {
  hash: string;
  subject: string;
  author: string;
  at: string;
}

/** What the branch holds against its base (internal/landing Produced). */
export interface Produced {
  branch: string;
  base: string;
  diff_base: string;
  head: string;
  ahead: number;
  behind: number;
  commits: Commit[];
  files: FileChange[];
  added: number;
  deleted: number;
  clean: boolean;
  dirty: string[];
  upstream?: string;
  pushed: boolean;
}

export type LandAction = "merge" | "pr" | "discard";
export const landActions: LandAction[] = ["merge", "pr", "discard"];
export const landLabels: Record<LandAction, string> = { merge: "Merge", pr: "Push and open PR", discard: "Discard" };

export interface LandOptions {
  state: "" | "merged" | "pr" | "discarded";
  detail: string;
  torn_down: boolean;
  base: string;
  branch: string;
  merge_mode: string;
  base_protected: boolean;
  allowed: Partial<Record<LandAction, boolean>>;
  why?: Partial<Record<LandAction, string>>;
  confirm: Record<LandAction, string>;
  request?: string;
}

export interface ReviewRound {
  job_id: string;
  round: number;
  engine: string;
  state: string;
  verdict: "" | "approve" | "changes" | "error";
  findings: string;
}

/** GET /jobs/{id}/review. */
export interface ReviewView {
  job_id: string;
  produced: Produced | null;
  inspect_error?: string;
  result_md: string;
  walkthrough_md: string;
  verified: string[];
  not_verified: string[];
  reviews: ReviewRound[];
  land: LandOptions;
}

/** GET /jobs/{id}/review/diff?path=. */
export interface FileDiffView {
  path: string;
  diff: string;
  binary: boolean;
  cut: boolean;
}

export interface LandResult {
  action: string;
  detail: string;
  torn_down: boolean;
  teardown_error?: string;
}

export interface SweepItem {
  job: { id: string; title: string; state: string; worktree_branch: string; work_dir: string; land_state: string; land_detail: string };
  age_seconds: number;
  dirty: boolean;
}

export interface QueueLine {
  running: number;
  limit: number;
  waiting: number;
  paused: boolean;
  load_hold?: string;
  load_per_core: number;
}

export interface PauseState {
  paused: boolean;
  by?: string;
  at?: string;
  until?: string;
  reason?: string;
  file: string;
  file_exists: boolean;
}

export interface ThrottleSettings {
  max_running: number;
  project_limits: Record<string, number> | null;
  load_per_core: number;
}

export interface HeavyOwner {
  pid: number;
  job?: string;
  command: string;
  dir: string;
  since: string;
  slot?: number;
}

export interface HeavyStatus {
  slots: number;
  running: HeavyOwner[];
  waiting: HeavyOwner[];
  deferred?: string;
}

export interface ThrottleView {
  settings: ThrottleSettings;
  defaults: ThrottleSettings;
  load: { per_core: number; threshold: number; holding: boolean; reason?: string; cores: number };
  queue?: QueueLine;
  heavy: HeavyStatus;
}

export type PresenceWord = "here" | "maybe" | "away";

export interface PresenceState {
  presence: PresenceWord;
  reason: string;
  browsers: number;
  dnd_until?: string;
}

export interface Digest {
  id: number;
  day: string;
  at: string;
  source: "orchestrator" | "crowsnest";
  body_md: string;
  question_id?: string;
}

export interface DigestSettings {
  enabled: boolean;
  at: string;
}

export interface DigestStart {
  day: string;
  source: "orchestrator" | "crowsnest";
  note: string;
}

// ---- Helpers --------------------------------------------------------------------------------

/** One line of a unified diff, classified for colour: only + and − lines are coloured. */
export interface UnifiedLine {
  kind: "add" | "del" | "hunk" | "meta" | "context";
  text: string;
  /** Line numbers in the old and new file ("" for the other side). */
  old: string;
  new: string;
}

/** Parses `git diff` output for one file. The file headers are dropped; hunks keep their @@. */
export function parseUnified(diff: string): UnifiedLine[] {
  const out: UnifiedLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;
  for (const line of diff.split("\n")) {
    if (line.startsWith("@@")) {
      const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
      oldLine = match ? Number(match[1]) : 0;
      newLine = match ? Number(match[2]) : 0;
      inHunk = true;
      out.push({ kind: "hunk", text: line, old: "", new: "" });
      continue;
    }
    if (!inHunk) {
      if (/^(similarity|rename|new file|deleted file|old mode|new mode)/.test(line)) out.push({ kind: "meta", text: line, old: "", new: "" });
      continue;
    }
    if (line.startsWith("+")) out.push({ kind: "add", text: line.slice(1), old: "", new: String(newLine++) });
    else if (line.startsWith("-")) out.push({ kind: "del", text: line.slice(1), old: String(oldLine++), new: "" });
    else if (line.startsWith("\\")) out.push({ kind: "meta", text: line, old: "", new: "" });
    else if (line !== "" || out.length > 0) out.push({ kind: "context", text: line.slice(1), old: String(oldLine++), new: String(newLine++) });
  }
  for (let last = out.at(-1); last && last.kind === "context" && last.text === ""; last = out.at(-1)) out.pop();
  return out;
}

/** A file is big when collapsed by default and loaded only on request. */
export const BIG_FILE_LINES = 400;

/** "3 h", "2 d": a sweep row's age. */
export function ageText(seconds: number): string {
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))} min`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} h`;
  return `${Math.round(seconds / 86400)} d`;
}

/** The pause banner's sentence. */
export function pauseLine(state: PauseState): string {
  if (!state.paused) return "";
  let line = "Automation paused";
  if (state.by) line += ` by ${state.by}`;
  if (state.at) line += ` at ${new Date(state.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  if (state.until) line += `, resumes at ${new Date(state.until).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  if (state.reason) line += `: ${state.reason}`;
  return line;
}

/** The next local time for HH:MM (today, or tomorrow once passed), as an ISO string; "" for none. */
export function nextClock(text: string, now: Date = new Date()): string {
  const match = /^(\d{1,2}):(\d{2})$/.exec(text.trim());
  if (!match) return "";
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (hour > 23 || minute > 59) return "";
  const at = new Date(now);
  at.setHours(hour, minute, 0, 0);
  if (at <= now) at.setDate(at.getDate() + 1);
  return at.toISOString();
}

/** "2 of 4 running · 1 waiting" for the board's header. */
export function queueText(line: QueueLine): string {
  let text = `${line.running} of ${line.limit} running`;
  if (line.waiting > 0) text += ` · ${line.waiting} waiting`;
  if (line.paused) text += " · paused";
  else if (line.load_hold) text += " · busy machine";
  return text;
}

/** The verdict in words. */
export function verdictText(verdict: ReviewRound["verdict"]): string {
  switch (verdict) {
    case "approve":
      return "Approved";
    case "changes":
      return "Changes requested";
    case "error":
      return "Could not run";
    default:
      return "Reviewing";
  }
}
