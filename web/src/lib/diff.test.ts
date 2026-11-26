import { describe, expect, it } from "vitest";
import { collapse, diffStats, lineDiff, splitRows, type DiffGap, type SplitRow } from "./diff";

describe("lineDiff", () => {
  it("numbers lines on both sides and marks changes", () => {
    const lines = lineDiff("a\nb\nc\n", "a\nB\nc\nd\n");
    expect(lines.map((line) => `${line.kind}:${line.text}:${line.oldNo ?? "-"}:${line.newNo ?? "-"}`)).toEqual([
      "same:a:1:1",
      "del:b:2:-",
      "add:B:-:2",
      "same:c:3:3",
      "add:d:-:4",
    ]);
    expect(diffStats(lines)).toEqual({ added: 2, removed: 1 });
  });

  it("handles a new file and identical files", () => {
    expect(diffStats(lineDiff("", "x\ny\n"))).toEqual({ added: 2, removed: 0 });
    expect(diffStats(lineDiff("same\n", "same\n"))).toEqual({ added: 0, removed: 0 });
  });
});

describe("collapse and split", () => {
  const original = Array.from({ length: 30 }, (_, index) => `line ${index + 1}`).join("\n") + "\n";
  const final = original.replace("line 15\n", "line fifteen\n");

  it("folds long unchanged runs, keeping three lines of context", () => {
    const items = collapse(lineDiff(original, final));
    const gaps = items.filter((item): item is DiffGap => item.kind === "gap");
    expect(gaps.map((gap) => gap.count)).toEqual([11, 12]);
    expect(items.filter((item) => item.kind !== "gap")).toHaveLength(3 + 1 + 1 + 3);
  });

  it("pairs deletions beside additions in split rows", () => {
    const rows = splitRows(collapse(lineDiff(original, final)));
    const changed = rows.find((row): row is SplitRow => !("kind" in row) && row.left?.kind === "del");
    expect(changed?.left?.text).toBe("line 15");
    expect(changed?.right?.text).toBe("line fifteen");
  });
});
