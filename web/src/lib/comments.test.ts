import { describe, expect, it } from "vitest";
import type { DiffFile } from "./changes";
import { anchored, clickLine, hunkRange, inSelection, markedLines, placeComments, selectionRange, sentText, sideOf, whereText, type DiffComment } from "./comments";

const file: DiffFile = {
  path: "a.go", status: "modified", added: 2, deleted: 1, binary: false, old_blob: "o1", new_blob: "n1", hash: "h1", group: "server", churn: false,
  viewed: false, changed_since_viewed: false, since_available: false,
};

const note = (extra: Partial<DiffComment>): DiffComment => ({
  id: 1, path: "a.go", side: "new", line_start: 3, line_end: 4, quote: "x", body: "b", scope: "all", hash: "h1", old_blob: "o1", new_blob: "n1",
  status: "draft", created_at: "", updated_at: "", pending: true, ...extra,
});

describe("selecting lines", () => {
  it("starts on a click, extends on shift (or a phone's second tap), and clears on a second plain click", () => {
    let selection = clickLine(null, "a.go", "new", 5, false);
    expect(selection).toEqual({ path: "a.go", side: "new", anchor: 5, end: 5 });
    selection = clickLine(selection, "a.go", "new", 2, true);
    expect(selectionRange(selection!)).toEqual({ start: 2, end: 5 });
    expect(inSelection(selection, "a.go", "new", 3)).toBe(true);
    expect(inSelection(selection, "a.go", "old", 3)).toBe(false);
    expect(inSelection(selection, "b.go", "new", 3)).toBe(false);
    // A plain click elsewhere starts again; another side or file starts again even when extending.
    expect(clickLine(selection, "a.go", "new", 9, false)).toEqual({ path: "a.go", side: "new", anchor: 9, end: 9 });
    expect(clickLine(selection, "a.go", "old", 3, true)).toEqual({ path: "a.go", side: "old", anchor: 3, end: 3 });
    expect(clickLine(selection, "b.go", "new", 3, true)).toEqual({ path: "b.go", side: "new", anchor: 3, end: 3 });
    // The one selected line clicked again clears it, either way.
    const one = clickLine(null, "a.go", "new", 5, false);
    expect(clickLine(one, "a.go", "new", 5, false)).toBeNull();
    expect(clickLine(one, "a.go", "new", 5, true)).toBeNull();
  });

  it("puts a removed line on the old side, an added one on the new, an unchanged one on the column clicked", () => {
    expect(sideOf({ kind: "del", old: 3, new: 0, text: "" }, "new")).toBe("old");
    expect(sideOf({ kind: "add", old: 0, new: 3, text: "" }, "old")).toBe("new");
    expect(sideOf({ kind: "context", old: 3, new: 4, text: "" }, "old")).toBe("old");
    expect(sideOf({ kind: "context", old: 3, new: 4, text: "" }, "new")).toBe("new");
  });

  it("takes a hunk's new lines, or its old ones when it only removes", () => {
    expect(hunkRange({ oldStart: 10, oldLines: 3, newStart: 12, newLines: 4 })).toEqual({ side: "new", start: 12, end: 15 });
    expect(hunkRange({ oldStart: 10, oldLines: 3, newStart: 9, newLines: 0 })).toEqual({ side: "old", start: 10, end: 12 });
  });
});

describe("placing notes", () => {
  it("draws a note beside its lines only while its side still holds the blob it was written against", () => {
    expect(anchored(note({}), file)).toBe(true);
    expect(anchored(note({ new_blob: "n0", status: "outdated" }), file)).toBe(false);
    expect(anchored(note({ side: "old", old_blob: "o1", new_blob: "n0" }), file)).toBe(true);
    expect(anchored(note({ side: "file", line_start: 0, line_end: 0 }), file)).toBe(false);
  });

  it("keys inline notes by side and last line, lists the rest, and marks every line a note covers", () => {
    const comments = [note({ id: 1 }), note({ id: 2, side: "old", line_start: 2, line_end: 2 }), note({ id: 3, new_blob: "n0" }), note({ id: 4, path: "b.go" })];
    const placed = placeComments(comments, file);
    expect(placed.inline.get("new:4")?.map((c) => c.id)).toEqual([1]);
    expect(placed.inline.get("old:2")?.map((c) => c.id)).toEqual([2]);
    expect(placed.loose.map((c) => c.id)).toEqual([3]);
    expect([...markedLines(comments, file, "new")]).toEqual([3, 4]);
    expect([...markedLines(comments, file, "old")]).toEqual([2]);
  });

  it("says where a note is and what a send did", () => {
    expect(whereText({ side: "new", line_start: 3, line_end: 3 })).toBe("line 3");
    expect(whereText({ side: "old", line_start: 3, line_end: 5 })).toBe("lines 3–5 (removed side)");
    expect(whereText({ side: "file", line_start: 0, line_end: 0 })).toBe("the whole file");
    expect(sentText({ message: "", count: 2, hidden: 0, sent: true, how: "fix_job", fix_job: "j_9" })).toContain("j_9, a fix job");
    expect(sentText({ message: "", count: 1, hidden: 0, sent: true, how: "waiting", why: "crowsnest is paused" })).toBe("1 comment waits until the job may continue: crowsnest is paused.");
  });
});
