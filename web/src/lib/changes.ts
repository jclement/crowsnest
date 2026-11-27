// A diff at any time (DESIGN.md 6.21): the TypeScript mirror of internal/changes, and the pure
// model the viewer renders from: unified-diff hunks, rows for the unified and split views, the
// gaps between hunks that can be expanded from the file's whole text, word-level emphasis on
// modified lines, and syntax tokens per line. Everything here is pure and unit-tested; the
// components only lay it out.
import { diffWordsWithSpace } from "diff";

export type ChangeScope = "all" | "committed" | "uncommitted" | "since_viewed";
export const scopes: ChangeScope[] = ["all", "committed", "uncommitted", "since_viewed"];

/** Who a diff belongs to: a job, a project's main checkout, or the orchestrator's folder. */
export type DiffWho = { kind: "job"; id: string } | { kind: "project"; ref: string } | { kind: "orchestrator" };

export function whoPath(who: DiffWho): string {
  switch (who.kind) {
    case "job":
      return `/changes/jobs/${encodeURIComponent(who.id)}`;
    case "project":
      return `/changes/projects/${encodeURIComponent(who.ref)}`;
    default:
      return "/changes/orchestrator";
  }
}

export function whoKey(who: DiffWho): string {
  return who.kind === "job" ? who.id : who.kind === "project" ? `project:${who.ref}` : "orchestrator";
}

/** The standalone route's search for who (/diff?job=j_12, /diff?project=<slug>, /diff?orchestrator=1). */
export function whoSearch(who: DiffWho): { job?: string; project?: string; orchestrator?: string } {
  return who.kind === "job" ? { job: who.id } : who.kind === "project" ? { project: who.ref } : { orchestrator: "1" };
}

export interface DiffFile {
  path: string;
  old_path?: string;
  status: "added" | "modified" | "deleted" | "renamed" | "copied" | "type";
  added: number;
  deleted: number;
  binary: boolean;
  old_blob: string;
  new_blob: string;
  too_large?: boolean;
  note?: string;
  hash: string;
  group: string;
  churn: boolean;
  churn_why?: string;
  viewed: boolean;
  viewed_at?: string;
  changed_since_viewed: boolean;
  since_available: boolean;
}

export interface ScopeInfo {
  name: ChangeScope;
  label: string;
  files: number;
  added: number;
  deleted: number;
}

export interface BaseChoice {
  ref: string;
  label: string;
}

export interface DiffSummary {
  subject: string;
  kind: "job" | "project" | "orchestrator";
  label: string;
  root: string;
  job_id?: string;
  job_state?: string;
  project_id?: number;
  project?: string;
  scope: ChangeScope;
  scopes: ScopeInfo[];
  base: string;
  base_label: string;
  base_ref: string;
  base_choices?: BaseChoice[];
  head: string;
  commits: { hash: string; subject: string; author: string; at: string }[];
  files: DiffFile[];
  added: number;
  deleted: number;
  viewed: number;
  total: number;
  truncated: boolean;
  computed_at: string;
  revision: string;
}

export interface FileView {
  path: string;
  old_path?: string;
  status: string;
  hash: string;
  diff: string;
  binary: boolean;
  too_large: boolean;
  note?: string;
  cut: boolean;
  old_text: string | null;
  new_text: string | null;
  since: boolean;
  viewed_at?: string;
  context: number;
}

export interface ProjectChangeRow {
  subject: string;
  kind: "job" | "project";
  label: string;
  job_id?: string;
  job_state?: string;
  project?: string;
  files: number;
  added: number;
  deleted: number;
  viewed: number;
  error?: string;
}

export interface DiffProgress {
  viewed: number;
  total: number;
  added: number;
  deleted: number;
}

// ---- Parsing ---------------------------------------------------------------------------------

export type LineKind = "context" | "add" | "del";

export interface DiffLine {
  kind: LineKind;
  /** Line numbers on the old and new side (0 where the line is not on that side). */
  old: number;
  new: number;
  text: string;
  /** "\ No newline at end of file" followed this line. */
  noNewline?: boolean;
}

export interface Hunk {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  header: string;
  lines: DiffLine[];
}

const hunkHeader = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/;

/** The hunks of one file's unified diff (git's headers before the first @@ are skipped). */
export function parseHunks(diff: string): Hunk[] {
  const hunks: Hunk[] = [];
  let hunk: Hunk | null = null;
  let oldLine = 0;
  let newLine = 0;
  const lines = diff.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  for (const raw of lines) {
    const header = hunkHeader.exec(raw);
    if (header) {
      hunk = {
        oldStart: Number(header[1]),
        oldLines: header[2] === undefined ? 1 : Number(header[2]),
        newStart: Number(header[3]),
        newLines: header[4] === undefined ? 1 : Number(header[4]),
        header: (header[5] ?? "").trim(),
        lines: [],
      };
      hunks.push(hunk);
      oldLine = hunk.oldStart;
      newLine = hunk.newStart;
      continue;
    }
    if (!hunk) continue;
    if (raw.startsWith("\\")) {
      const last = hunk.lines[hunk.lines.length - 1];
      if (last) last.noNewline = true;
      continue;
    }
    const sign = raw[0];
    const text = raw.slice(1);
    if (sign === "+") hunk.lines.push({ kind: "add", old: 0, new: newLine++, text });
    else if (sign === "-") hunk.lines.push({ kind: "del", old: oldLine++, new: 0, text });
    else hunk.lines.push({ kind: "context", old: oldLine++, new: newLine++, text });
  }
  return hunks;
}

/** Splits a file's text into lines (no trailing empty line for a final newline). */
export function splitLines(text: string): string[] {
  const lines = text.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  return lines;
}

// ---- Gaps and expansion ----------------------------------------------------------------------

/** The unchanged lines between two hunks (or before the first, or after the last). */
export interface Gap {
  index: number;
  /** First and last new-side line of the gap (inclusive); end < start for an empty gap. */
  newStart: number;
  newEnd: number;
  /** old = new + offset inside the gap. */
  offset: number;
}

/** The gaps around hunks, given the new side's length (unknown: no gap after the last hunk). */
export function gapsOf(hunks: Hunk[], newLength: number | null): Gap[] {
  const gaps: Gap[] = [];
  let nextNew = 1;
  let offset = 0;
  hunks.forEach((hunk, index) => {
    const start = hunk.newLines === 0 ? hunk.newStart + 1 : hunk.newStart;
    gaps.push({ index, newStart: nextNew, newEnd: start - 1, offset });
    nextNew = start + hunk.newLines;
    const nextOld = hunk.oldLines === 0 ? hunk.oldStart + 1 : hunk.oldStart + hunk.oldLines;
    offset = nextOld - nextNew;
  });
  if (newLength !== null && hunks.length > 0) gaps.push({ index: hunks.length, newStart: nextNew, newEnd: newLength, offset });
  return gaps;
}

/** How much of a gap is shown: lines from its top and its bottom (all of it when they meet). */
export interface Expansion {
  top: number;
  bottom: number;
}

export const EXPAND_STEP = 20;

/** The rows a partly expanded gap shows, and how many stay hidden between them. */
export function expandGap(gap: Gap, expansion: Expansion | undefined, newLines: string[]): { top: DiffLine[]; hidden: number; bottom: DiffLine[] } {
  const size = Math.max(0, gap.newEnd - gap.newStart + 1);
  const top = Math.min(size, expansion?.top ?? 0);
  const bottom = Math.min(size - top, expansion?.bottom ?? 0);
  const line = (n: number): DiffLine => ({ kind: "context", old: n + gap.offset, new: n, text: newLines[n - 1] ?? "" });
  const topLines: DiffLine[] = [];
  for (let n = gap.newStart; n < gap.newStart + top; n++) topLines.push(line(n));
  const bottomLines: DiffLine[] = [];
  for (let n = gap.newEnd - bottom + 1; n <= gap.newEnd; n++) bottomLines.push(line(n));
  return { top: topLines, hidden: size - top - bottom, bottom: bottomLines };
}

// ---- Rows ------------------------------------------------------------------------------------

/** One rendered row of the unified view. */
export type UnifiedRow =
  | { type: "line"; line: DiffLine; change: number | null; emphasis?: Range[] }
  | { type: "gap"; gap: Gap; hidden: number; header?: string };

/** One rendered row of the split view: left is the old side, right the new. */
export type SplitRow =
  | { type: "pair"; left: DiffLine | null; right: DiffLine | null; change: number | null; leftEmphasis?: Range[]; rightEmphasis?: Range[] }
  | { type: "gap"; gap: Gap; hidden: number; header?: string };

/** A character range [start, end) of a line to emphasise (the words that changed). */
export interface Range {
  start: number;
  end: number;
}

/** Word-level emphasis for a removed line and the added line that replaced it. */
export function wordEmphasis(oldText: string, newText: string): { old: Range[]; new: Range[] } | null {
  if (oldText.length > 400 || newText.length > 400 || oldText === newText) return null;
  const parts = diffWordsWithSpace(oldText, newText);
  const oldRanges: Range[] = [];
  const newRanges: Range[] = [];
  let oldAt = 0;
  let newAt = 0;
  let common = 0;
  for (const part of parts) {
    const length = part.value.length;
    if (part.added) {
      newRanges.push({ start: newAt, end: newAt + length });
      newAt += length;
    } else if (part.removed) {
      oldRanges.push({ start: oldAt, end: oldAt + length });
      oldAt += length;
    } else {
      oldAt += length;
      newAt += length;
      common += part.value.trim().length;
    }
  }
  // Lines with almost nothing in common are a rewrite: emphasising every word says nothing.
  if (common < Math.min(oldText.trim().length, newText.trim().length) * 0.3) return null;
  return { old: mergeRanges(oldRanges), new: mergeRanges(newRanges) };
}

function mergeRanges(ranges: Range[]): Range[] {
  const out: Range[] = [];
  for (const range of ranges) {
    const last = out[out.length - 1];
    if (last && range.start <= last.end) last.end = Math.max(last.end, range.end);
    else out.push({ ...range });
  }
  return out;
}

/** Runs of removed lines followed by added lines, paired line by line for emphasis and split. */
interface ChangeRun {
  dels: DiffLine[];
  adds: DiffLine[];
}

function runsOf(lines: DiffLine[]): Array<DiffLine | ChangeRun> {
  const out: Array<DiffLine | ChangeRun> = [];
  let run: ChangeRun | null = null;
  for (const line of lines) {
    if (line.kind === "context") {
      if (run) out.push(run);
      run = null;
      out.push(line);
      continue;
    }
    if (!run || (line.kind === "del" && run.adds.length > 0)) {
      if (run) out.push(run);
      run = { dels: [], adds: [] };
    }
    if (line.kind === "del") run.dels.push(line);
    else run.adds.push(line);
  }
  if (run) out.push(run);
  return out;
}

function isRun(item: DiffLine | ChangeRun): item is ChangeRun {
  return "dels" in item;
}

/** Builds the unified rows for hunks with their gaps; expansions are by gap index. */
export function unifiedRows(hunks: Hunk[], gaps: Gap[], expansions: Record<number, Expansion>, newLines: string[] | null): UnifiedRow[] {
  const rows: UnifiedRow[] = [];
  let change = 0;
  const pushGap = (gap: Gap | undefined, header?: string) => {
    if (!gap || gap.newEnd < gap.newStart) return;
    if (!newLines) {
      rows.push({ type: "gap", gap, hidden: gap.newEnd - gap.newStart + 1, header });
      return;
    }
    const shown = expandGap(gap, expansions[gap.index], newLines);
    for (const line of shown.top) rows.push({ type: "line", line, change: null });
    if (shown.hidden > 0) rows.push({ type: "gap", gap, hidden: shown.hidden, header });
    for (const line of shown.bottom) rows.push({ type: "line", line, change: null });
  };
  hunks.forEach((hunk, index) => {
    pushGap(gaps[index], hunk.header);
    for (const item of runsOf(hunk.lines)) {
      if (!isRun(item)) {
        rows.push({ type: "line", line: item, change: null });
        continue;
      }
      const id = change++;
      const paired = item.dels.length === item.adds.length || Math.abs(item.dels.length - item.adds.length) <= 2;
      item.dels.forEach((line, i) => {
        const emphasis = paired && item.adds[i] ? wordEmphasis(line.text, item.adds[i].text)?.old : undefined;
        rows.push({ type: "line", line, change: id, emphasis });
      });
      item.adds.forEach((line, i) => {
        const emphasis = paired && item.dels[i] ? wordEmphasis(item.dels[i].text, line.text)?.new : undefined;
        rows.push({ type: "line", line, change: id, emphasis });
      });
    }
  });
  pushGap(gaps[hunks.length]);
  return rows;
}

/** Builds the split rows: unchanged lines on both sides, a change's lines side by side. */
export function splitRows(hunks: Hunk[], gaps: Gap[], expansions: Record<number, Expansion>, newLines: string[] | null): SplitRow[] {
  const rows: SplitRow[] = [];
  let change = 0;
  const pushGap = (gap: Gap | undefined, header?: string) => {
    if (!gap || gap.newEnd < gap.newStart) return;
    if (!newLines) {
      rows.push({ type: "gap", gap, hidden: gap.newEnd - gap.newStart + 1, header });
      return;
    }
    const shown = expandGap(gap, expansions[gap.index], newLines);
    for (const line of shown.top) rows.push({ type: "pair", left: line, right: line, change: null });
    if (shown.hidden > 0) rows.push({ type: "gap", gap, hidden: shown.hidden, header });
    for (const line of shown.bottom) rows.push({ type: "pair", left: line, right: line, change: null });
  };
  hunks.forEach((hunk, index) => {
    pushGap(gaps[index], hunk.header);
    for (const item of runsOf(hunk.lines)) {
      if (!isRun(item)) {
        rows.push({ type: "pair", left: item, right: item, change: null });
        continue;
      }
      const id = change++;
      const length = Math.max(item.dels.length, item.adds.length);
      for (let i = 0; i < length; i++) {
        const left = item.dels[i] ?? null;
        const right = item.adds[i] ?? null;
        const emphasis = left && right ? wordEmphasis(left.text, right.text) : null;
        rows.push({ type: "pair", left, right, change: id, leftEmphasis: emphasis?.old, rightEmphasis: emphasis?.new });
      }
    }
  });
  pushGap(gaps[hunks.length]);
  return rows;
}

// ---- Syntax ----------------------------------------------------------------------------------

/** highlight.js's language for a path ("" for plain text). */
export function languageFor(path: string): string {
  const lower = path.toLowerCase();
  const base = lower.split("/").pop() ?? lower;
  if (base === "dockerfile") return "dockerfile";
  if (base === "makefile") return "makefile";
  const ext = base.includes(".") ? base.slice(base.lastIndexOf(".") + 1) : "";
  const map: Record<string, string> = {
    go: "go", ts: "typescript", tsx: "typescript", mts: "typescript", js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
    css: "css", scss: "scss", less: "less", md: "markdown", mdx: "markdown", json: "json", yaml: "yaml", yml: "yaml", toml: "ini", ini: "ini",
    py: "python", rb: "ruby", rs: "rust", java: "java", kt: "kotlin", swift: "swift", c: "c", h: "c", cpp: "cpp", cc: "cpp", hpp: "cpp",
    cs: "csharp", sh: "bash", bash: "bash", zsh: "bash", sql: "sql", html: "xml", xml: "xml", svg: "xml", vue: "xml", php: "php",
    lua: "lua", diff: "diff", graphql: "graphql", r: "r", pl: "perl",
  };
  return map[ext] ?? "";
}

/** A run of text with its highlight classes ("" for none). */
export interface Token {
  text: string;
  className: string;
}

/** Text with emphasis applied to its tokens: each piece knows whether it is in a changed range. */
export interface Piece extends Token {
  mark: boolean;
}

/** Splits tokens at the emphasis ranges' edges. */
export function applyEmphasis(tokens: Token[], ranges: Range[] | undefined): Piece[] {
  if (!ranges || ranges.length === 0) return tokens.map((token) => ({ ...token, mark: false }));
  const pieces: Piece[] = [];
  let at = 0;
  for (const token of tokens) {
    let start = 0;
    while (start < token.text.length) {
      const position = at + start;
      const inside = ranges.find((range) => position >= range.start && position < range.end);
      const next = inside
        ? inside.end
        : Math.min(...ranges.filter((range) => range.start > position).map((range) => range.start), at + token.text.length);
      const end = Math.min(next, at + token.text.length) - at;
      pieces.push({ text: token.text.slice(start, end), className: token.className, mark: !!inside });
      start = end;
    }
    at += token.text.length;
  }
  return pieces;
}

// ---- Viewed and the file list ----------------------------------------------------------------

/** The files the list shows: filtered by the box (path words) and, with hide on, not viewed. */
export function visibleFiles(files: DiffFile[], filter: string, hideViewed: boolean): DiffFile[] {
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  return files.filter((file) => {
    if (hideViewed && file.viewed) return false;
    const path = (file.path + " " + (file.old_path ?? "")).toLowerCase();
    return words.every((word) => path.includes(word));
  });
}

/** "12 of 30 viewed". */
export function viewedText(viewed: number, total: number): string {
  return `${viewed} of ${total} viewed`;
}

/** The diffstat bar's five cells: how many are added, removed and unchanged (GitHub's style). */
export function statCells(added: number, deleted: number, cells = 5): ("add" | "del" | "none")[] {
  const total = added + deleted;
  if (total === 0) return Array(cells).fill("none");
  const addCells = Math.round((added / total) * cells);
  const out: ("add" | "del" | "none")[] = [];
  for (let i = 0; i < cells; i++) out.push(i < addCells ? "add" : "del");
  if (added > 0 && addCells === 0) out[0] = "add";
  if (deleted > 0 && addCells === cells) out[cells - 1] = "del";
  return out;
}

export const statusLetter: Record<DiffFile["status"], string> = { added: "A", modified: "M", deleted: "D", renamed: "R", copied: "C", type: "T" };

/** Moves from index by step through a list of length n, clamped (−1 when the list is empty). */
export function step(index: number, by: number, n: number): number {
  if (n === 0) return -1;
  return Math.max(0, Math.min(n - 1, (index < 0 ? (by > 0 ? -1 : n) : index) + by));
}

// ---- Remembered preferences ------------------------------------------------------------------

export interface ViewerPrefs {
  split: boolean;
  wrap: boolean;
  ignoreWhitespace: boolean;
  hideViewed: boolean;
  listOpen: boolean;
}

const PREFS_KEY = "crowsnest-diff-prefs";
export const defaultPrefs: ViewerPrefs = { split: true, wrap: false, ignoreWhitespace: false, hideViewed: false, listOpen: true };

export function loadPrefs(storage: Pick<Storage, "getItem"> | undefined = globalThis.localStorage): ViewerPrefs {
  try {
    const raw = storage?.getItem(PREFS_KEY);
    return raw ? { ...defaultPrefs, ...(JSON.parse(raw) as Partial<ViewerPrefs>) } : defaultPrefs;
  } catch {
    return defaultPrefs;
  }
}

export function savePrefs(prefs: ViewerPrefs, storage: Pick<Storage, "setItem"> | undefined = globalThis.localStorage): void {
  try {
    storage?.setItem(PREFS_KEY, JSON.stringify(prefs));
  } catch {
    // private mode: not remembered
  }
}

/** Where a reader was in a diff, kept across fullscreen and a new tab (sessionStorage). */
export interface Position {
  file?: string;
  offset?: number;
}

export function positionKey(who: DiffWho): string {
  return "crowsnest-diff-pos:" + whoKey(who);
}
