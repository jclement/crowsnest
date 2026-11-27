// Line comments on a diff (DESIGN.md 6.21): the TypeScript mirror of internal/changes' comments,
// and the pure parts of the viewer's comment UI: a selection of lines on one side of one file
// (click or tap a line number, then shift-click, drag, or on a phone tap again, to extend), where a
// note shows in the rows (after the last line it covers, on the side it is on, only while the file
// still holds the version it was written against), and the words used for a note's place.
import type { DiffFile, DiffLine } from "./changes";

export type CommentSide = "old" | "new" | "file";
export type CommentStatus = "draft" | "sent" | "outdated";

export interface DiffComment {
  id: number;
  path: string;
  side: CommentSide;
  line_start: number;
  line_end: number;
  quote: string;
  body: string;
  scope: string;
  hash: string;
  old_blob: string;
  new_blob: string;
  status: CommentStatus;
  created_at: string;
  updated_at: string;
  sent_at?: string;
  /** It goes with the next send: a draft, or an outdated note not sent since. */
  pending: boolean;
}

export interface CommentList {
  subject: string;
  kind: "job" | "project" | "orchestrator";
  label: string;
  job_id?: string;
  job_state?: string;
  comments: DiffComment[];
  drafts: number;
  sent: number;
  outdated: number;
  last_sent_at?: string;
}

export interface NewComment {
  path: string;
  scope: string;
  side: CommentSide;
  line_start: number;
  line_end: number;
  body: string;
  hash: string;
}

export interface SendResult {
  message: string;
  count: number;
  hidden: number;
  mixed?: string[];
  sent: boolean;
  how?: "typed" | "queued" | "waiting" | "fix_job";
  why?: string;
  fix_job?: string;
  attached?: string;
  at?: string;
}

// ---- Selection ----------------------------------------------------------------------------------

/** Lines selected on one side of one file: anchor is where it started, end where it was extended to. */
export interface Selection {
  path: string;
  side: "old" | "new";
  anchor: number;
  end: number;
}

export function selectionRange(selection: Selection): { start: number; end: number } {
  return { start: Math.min(selection.anchor, selection.end), end: Math.max(selection.anchor, selection.end) };
}

export function inSelection(selection: Selection | null, path: string, side: "old" | "new", line: number): boolean {
  if (!selection || selection.path !== path || selection.side !== side || line <= 0) return false;
  const { start, end } = selectionRange(selection);
  return line >= start && line <= end;
}

/**
 * The selection after a click (or tap) on line `line` of `side`. extend is shift-click on a
 * desktop, or any tap on a phone while a selection on the same side of the same file is open
 * (a phone has no shift): it moves the selection's end. Tapping the only selected line again
 * clears it. Anything else starts a new one-line selection.
 */
export function clickLine(selection: Selection | null, path: string, side: "old" | "new", line: number, extend: boolean): Selection | null {
  const same = selection && selection.path === path && selection.side === side;
  if (same && selection.anchor === line && selection.end === line && !extend) return null;
  if (same && extend) {
    if (selection.anchor === line && selection.end === line) return null;
    return { ...selection, end: line };
  }
  return { path, side, anchor: line, end: line };
}

/** The side a line number on a row belongs to: a removed line's is old, an added line's new; an unchanged one is new (its old number shows too). */
export function sideOf(line: DiffLine, column: "old" | "new"): "old" | "new" {
  if (line.kind === "del") return "old";
  if (line.kind === "add") return "new";
  return column;
}

export function lineOn(line: DiffLine, side: "old" | "new"): number {
  return side === "old" ? line.old : line.new;
}

/** A hunk's range for "Ask for change here": its new lines, or its old ones when it only removes. */
export function hunkRange(hunk: { oldStart: number; oldLines: number; newStart: number; newLines: number }): { side: "old" | "new"; start: number; end: number } {
  if (hunk.newLines > 0) return { side: "new", start: hunk.newStart, end: hunk.newStart + hunk.newLines - 1 };
  return { side: "old", start: hunk.oldStart, end: hunk.oldStart + Math.max(hunk.oldLines, 1) - 1 };
}

// ---- Placing notes ------------------------------------------------------------------------------

/**
 * Whether a note can be drawn in the rows of file as shown now: it is on lines, not the whole
 * file, and the side it is on still holds the blob it was written against. Everything else
 * (whole-file notes, outdated ones, notes from another comparison) is listed under the file's
 * header instead.
 */
export function anchored(comment: DiffComment, file: DiffFile): boolean {
  if (comment.side === "file" || comment.path !== file.path) return false;
  return comment.side === "old" ? comment.old_blob === file.old_blob : comment.new_blob === file.new_blob;
}

/** Notes keyed by "side:last line" for the rows of one file, and the rest. */
export function placeComments(comments: DiffComment[], file: DiffFile): { inline: Map<string, DiffComment[]>; loose: DiffComment[] } {
  const inline = new Map<string, DiffComment[]>();
  const loose: DiffComment[] = [];
  for (const comment of comments) {
    if (comment.path !== file.path) continue;
    if (!anchored(comment, file)) {
      loose.push(comment);
      continue;
    }
    const key = `${comment.side}:${comment.line_end}`;
    inline.set(key, [...(inline.get(key) ?? []), comment]);
  }
  return { inline, loose };
}

/** Lines of one side that carry a note, for the gutter marker. */
export function markedLines(comments: DiffComment[], file: DiffFile, side: "old" | "new"): Set<number> {
  const lines = new Set<number>();
  for (const comment of comments) {
    if (comment.side !== side || !anchored(comment, file)) continue;
    for (let line = comment.line_start; line <= comment.line_end; line++) lines.add(line);
  }
  return lines;
}

/** Where a note is, in words ("lines 12–14", "line 3 (removed)", "the whole file"). */
export function whereText(comment: Pick<DiffComment, "side" | "line_start" | "line_end">): string {
  if (comment.side === "file") return "the whole file";
  const lines = comment.line_start === comment.line_end ? `line ${comment.line_start}` : `lines ${comment.line_start}–${comment.line_end}`;
  return comment.side === "old" ? `${lines} (removed side)` : lines;
}

export const statusText: Record<CommentStatus, string> = { draft: "draft", sent: "sent", outdated: "outdated" };

/** What a send did, for a toast. */
export function sentText(result: SendResult): string {
  const n = `${result.count} comment${result.count === 1 ? "" : "s"}`;
  switch (result.how) {
    case "fix_job":
      return `${n} went to ${result.fix_job}, a fix job in this worktree; the job is checked again when it is done.`;
    case "waiting":
      return `${n} ${result.count === 1 ? "waits" : "wait"} until the job may continue: ${result.why ?? "it is held"}.`;
    case "queued":
      return `${n} queued for the agent behind the turn it is in.`;
    default:
      return `${n} sent to the agent.`;
  }
}

/** How many notes the next send carries, as the button says it. */
export function sendLabel(drafts: number): string {
  return `Send ${drafts} comment${drafts === 1 ? "" : "s"} to the agent`;
}
