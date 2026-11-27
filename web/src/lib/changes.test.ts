import { describe, expect, it } from "vitest";
import {
  applyEmphasis,
  expandGap,
  gapsOf,
  languageFor,
  loadPrefs,
  parseHunks,
  splitLines,
  splitRows,
  statCells,
  step,
  unifiedRows,
  viewedText,
  visibleFiles,
  wordEmphasis,
  type DiffFile,
} from "./changes";
import { validateDiffSearch, whoFromSearch } from "./diffSearch";
import { highlightLines } from "./syntax";

const diff = `diff --git a/a.ts b/a.ts
index 1..2 100644
--- a/a.ts
+++ b/a.ts
@@ -2,3 +2,3 @@ export function f() {
 const a = 1;
-const total = price * count;
+const total = price * quantity;
 return a;
@@ -20,2 +20,3 @@
 x;
+y;
 z;
\\ No newline at end of file
`;

describe("parseHunks", () => {
  it("reads hunks with both sides' line numbers", () => {
    const hunks = parseHunks(diff);
    expect(hunks).toHaveLength(2);
    expect(hunks[0]!.header).toBe("export function f() {");
    expect(hunks[0]!.lines.map((line) => [line.kind, line.old, line.new])).toEqual([
      ["context", 2, 2],
      ["del", 3, 0],
      ["add", 0, 3],
      ["context", 4, 4],
    ]);
    expect(hunks[1]!.lines[2]).toMatchObject({ kind: "context", old: 21, new: 22, noNewline: true });
  });
});

describe("gaps and expansion", () => {
  const newText = Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join("\n") + "\n";
  const lines = splitLines(newText);
  const hunks = parseHunks(diff);

  it("finds the unchanged lines before, between and after the hunks", () => {
    const gaps = gapsOf(hunks, lines.length);
    expect(gaps.map((gap) => [gap.newStart, gap.newEnd, gap.offset])).toEqual([
      [1, 1, 0],
      [5, 19, 0],
      [23, 30, -1],
    ]);
  });

  it("expands 20 lines at a time from either end, or all of it, with the old side's numbers", () => {
    const gap = gapsOf(hunks, lines.length)[2]!;
    const some = expandGap(gap, { top: 2, bottom: 1 }, lines);
    expect(some.top.map((line) => [line.old, line.new, line.text])).toEqual([
      [22, 23, "line 23"],
      [23, 24, "line 24"],
    ]);
    expect(some.hidden).toBe(5);
    expect(some.bottom.map((line) => line.new)).toEqual([30]);
    expect(expandGap(gap, { top: 99, bottom: 0 }, lines).hidden).toBe(0);
  });

  it("builds unified rows with gaps, and splits a change side by side", () => {
    const gaps = gapsOf(hunks, lines.length);
    const unified = unifiedRows(hunks, gaps, {}, lines);
    expect(unified.filter((row) => row.type === "gap")).toHaveLength(3);
    const change = unified.filter((row) => row.type === "line" && row.change === 0);
    expect(change).toHaveLength(2);
    const split = splitRows(hunks, gaps, { 1: { top: 15, bottom: 0 } }, lines);
    const pair = split.find((row) => row.type === "pair" && row.change === 0);
    expect(pair && pair.type === "pair" && [pair.left?.text, pair.right?.text]).toEqual(["const total = price * count;", "const total = price * quantity;"]);
    // The middle gap is fully expanded: no gap row between the hunks.
    expect(split.filter((row) => row.type === "gap").map((row) => row.type === "gap" && row.gap.index)).toEqual([0, 2]);
  });

  it("shows a gap row asking for more context when the file's text is not available", () => {
    const rows = unifiedRows(hunks, gapsOf(hunks, null), {}, null);
    expect(rows.filter((row) => row.type === "gap").map((row) => row.type === "gap" && row.hidden)).toEqual([1, 15]);
  });
});

describe("word emphasis", () => {
  it("marks only the words that changed on a modified line", () => {
    const emphasis = wordEmphasis("const total = price * count;", "const total = price * quantity;");
    expect(emphasis).not.toBeNull();
    const oldText = "const total = price * count;";
    expect(emphasis!.old.map((range) => oldText.slice(range.start, range.end))).toEqual(["count"]);
    expect(emphasis!.new.map((range) => "const total = price * quantity;".slice(range.start, range.end))).toEqual(["quantity"]);
  });

  it("does not emphasise a rewrite with nothing in common", () => {
    expect(wordEmphasis("alpha beta gamma", "one two three")).toBeNull();
  });

  it("splits syntax tokens at the emphasis edges", () => {
    const pieces = applyEmphasis(
      [
        { text: "const ", className: "hljs-keyword" },
        { text: "count", className: "" },
      ],
      [{ start: 4, end: 8 }],
    );
    expect(pieces).toEqual([
      { text: "cons", className: "hljs-keyword", mark: false },
      { text: "t ", className: "hljs-keyword", mark: true },
      { text: "co", className: "", mark: true },
      { text: "unt", className: "", mark: false },
    ]);
  });
});

describe("syntax", () => {
  it("highlights the whole file so a multi-line comment colours every line", () => {
    const lines = highlightLines("/* one\ntwo */\nconst x = 1;\n", "typescript");
    expect(lines).toHaveLength(3);
    expect(lines[1]![0]).toMatchObject({ text: "two */", className: "hljs-comment" });
    expect(lines[2]!.some((token) => token.className === "hljs-keyword" && token.text === "const")).toBe(true);
  });

  it("knows the languages by extension", () => {
    expect(languageFor("server/checkout.go")).toBe("go");
    expect(languageFor("web/src/cart.ts")).toBe("typescript");
    expect(languageFor("styles/cart.css")).toBe("css");
    expect(languageFor("README.md")).toBe("markdown");
    expect(languageFor("logo.png")).toBe("");
  });
});

describe("the file list", () => {
  const file = (path: string, viewed: boolean): DiffFile => ({
    path, status: "modified", added: 1, deleted: 1, binary: false, old_blob: "", new_blob: "", hash: path, group: "server", churn: false,
    viewed, changed_since_viewed: false, since_available: false,
  });
  it("filters by path words and hides viewed files", () => {
    const files = [file("server/a.go", true), file("web/b.ts", false), file("web/c.css", false)];
    expect(visibleFiles(files, "", true).map((entry) => entry.path)).toEqual(["web/b.ts", "web/c.css"]);
    expect(visibleFiles(files, "web css", false).map((entry) => entry.path)).toEqual(["web/c.css"]);
    expect(viewedText(1, 3)).toBe("1 of 3 viewed");
  });
  it("draws diffstat cells and steps through the list", () => {
    expect(statCells(10, 0)).toEqual(["add", "add", "add", "add", "add"]);
    expect(statCells(1, 99)).toEqual(["add", "del", "del", "del", "del"]);
    expect(statCells(0, 0)).toEqual(["none", "none", "none", "none", "none"]);
    expect([step(-1, 1, 3), step(2, 1, 3), step(0, -1, 3), step(-1, -1, 3), step(0, 1, 0)]).toEqual([0, 2, 0, 2, -1]);
  });
  it("remembers the view settings", () => {
    const store = new Map<string, string>([["crowsnest-diff-prefs", JSON.stringify({ split: false })]]);
    expect(loadPrefs({ getItem: (key) => store.get(key) ?? null })).toMatchObject({ split: false, wrap: false, listOpen: true });
  });
});

describe("the standalone route's search", () => {
  it("reads who, scope and file, and keeps only an in-app return path", () => {
    const search = validateDiffSearch({ job: "j_12", scope: "uncommitted", file: "a.go", from: "https://evil.example/" });
    expect(search).toMatchObject({ job: "j_12", scope: "uncommitted", file: "a.go", from: undefined });
    expect(validateDiffSearch({ project: "web", scope: "bogus", from: "/jobs?job=j_1" })).toMatchObject({ scope: undefined, from: "/jobs?job=j_1" });
    expect(validateDiffSearch({ from: "//evil.example" }).from).toBeUndefined();
    expect(whoFromSearch({ project: "web" })).toEqual({ kind: "project", ref: "web" });
    expect(whoFromSearch({})).toBeNull();
  });
});
