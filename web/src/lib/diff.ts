// Line diffs for the `files` of a question (DESIGN.md 8.1): the `diff` package computes the line
// changes, and this module shapes them for the two layouts DiffView draws: unified (one column,
// on a phone) and split (old at left, new at right, from lg). Long unchanged runs collapse to a
// "N unchanged lines" gap so a two-line change in a long file stays readable.
import { diffLines } from "diff";

export interface DiffLine {
  kind: "add" | "del" | "same";
  text: string;
  /** Line number in the original (del, same). */
  oldNo?: number;
  /** Line number in the final (add, same). */
  newNo?: number;
}

export interface DiffGap {
  kind: "gap";
  /** How many unchanged lines are hidden. */
  count: number;
  /** The hidden lines, for expanding in place. */
  lines: DiffLine[];
}

export type DiffItem = DiffLine | DiffGap;

/** One row of the split view: the original line at left, the final at right (either may be absent). */
export interface SplitRow {
  left?: DiffLine;
  right?: DiffLine;
}

/** Unchanged lines kept around each change. */
export const CONTEXT_LINES = 3;

/** The line-by-line diff of original → final. */
export function lineDiff(original: string, final: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldNo = 1;
  let newNo = 1;
  for (const change of diffLines(original, final)) {
    for (const text of splitLines(change.value)) {
      if (change.added) out.push({ kind: "add", text, newNo: newNo++ });
      else if (change.removed) out.push({ kind: "del", text, oldNo: oldNo++ });
      else out.push({ kind: "same", text, oldNo: oldNo++, newNo: newNo++ });
    }
  }
  return out;
}

/** A chunk's lines, without the empty string after its final newline. */
function splitLines(value: string): string[] {
  const lines = value.split("\n");
  if (lines.at(-1) === "") lines.pop();
  return lines;
}

/** Counts of added and removed lines. */
export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const line of lines) {
    if (line.kind === "add") added++;
    if (line.kind === "del") removed++;
  }
  return { added, removed };
}

/** Replaces unchanged runs longer than 2×context (+1) with gaps, keeping context lines by each change. */
export function collapse(lines: DiffLine[], context = CONTEXT_LINES): DiffItem[] {
  const out: DiffItem[] = [];
  let index = 0;
  while (index < lines.length) {
    if (lines[index]?.kind !== "same") {
      out.push(lines[index] as DiffLine);
      index++;
      continue;
    }
    let end = index;
    while (end < lines.length && lines[end]?.kind === "same") end++;
    const run = lines.slice(index, end);
    const keepBefore = index === 0 ? 0 : context;
    const keepAfter = end === lines.length ? 0 : context;
    if (run.length > keepBefore + keepAfter + 1) {
      out.push(...run.slice(0, keepBefore));
      const hidden = run.slice(keepBefore, run.length - keepAfter);
      out.push({ kind: "gap", count: hidden.length, lines: hidden });
      out.push(...run.slice(run.length - keepAfter));
    } else {
      out.push(...run);
    }
    index = end;
  }
  return out;
}

/** Pairs a unified diff into split rows: a run of deletions sits beside the additions that follow it. */
export function splitRows(items: DiffItem[]): (SplitRow | DiffGap)[] {
  const rows: (SplitRow | DiffGap)[] = [];
  let index = 0;
  while (index < items.length) {
    const item = items[index] as DiffItem;
    if (item.kind === "gap") {
      rows.push(item);
      index++;
    } else if (item.kind === "same") {
      rows.push({ left: item, right: item });
      index++;
    } else {
      const dels: DiffLine[] = [];
      const adds: DiffLine[] = [];
      while (items[index]?.kind === "del") dels.push(items[index++] as DiffLine);
      while (items[index]?.kind === "add") adds.push(items[index++] as DiffLine);
      for (let row = 0; row < Math.max(dels.length, adds.length); row++) rows.push({ left: dels[row], right: adds[row] });
    }
  }
  return rows;
}
