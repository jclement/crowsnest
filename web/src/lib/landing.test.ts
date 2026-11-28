import { describe, expect, it } from "vitest";
import { ageText, nextClock, parseUnified, pauseLine, queueText, verdictText, landable } from "./landing";

describe("parseUnified", () => {
  it("keeps hunks, numbers the lines and drops the file headers", () => {
    const diff = "diff --git a/x.go b/x.go\nindex 1..2 100644\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,4 @@\n package x\n-old\n+new\n+added\n tail\n";
    const lines = parseUnified(diff);
    expect(lines.map((line) => line.kind)).toEqual(["hunk", "context", "del", "add", "add", "context"]);
    expect(lines[2]).toMatchObject({ text: "old", old: "2", new: "" });
    expect(lines[3]).toMatchObject({ text: "new", old: "", new: "2" });
    expect(lines[5]).toMatchObject({ text: "tail", old: "3", new: "4" });
  });
  it("shows a rename's metadata", () => {
    expect(parseUnified("diff --git a/a b/b\nsimilarity index 100%\nrename from a\nrename to b\n").map((line) => line.kind)).toEqual(["meta", "meta", "meta"]);
  });
});

describe("words", () => {
  it("queue line", () => {
    expect(queueText({ running: 2, limit: 4, waiting: 1, paused: false, load_per_core: 0.5 })).toBe("2 of 4 running · 1 waiting");
    expect(queueText({ running: 4, limit: 4, waiting: 0, paused: true, load_per_core: 0 })).toBe("4 of 4 running · paused");
    expect(queueText({ running: 1, limit: 4, waiting: 0, paused: false, load_hold: "busy", load_per_core: 3 })).toBe("1 of 4 running · busy machine");
  });
  it("pause line", () => {
    expect(pauseLine({ paused: false, file: "", file_exists: false })).toBe("");
    expect(pauseLine({ paused: true, by: "you", reason: "deploy", file: "", file_exists: false })).toBe("Automation paused by you: deploy");
  });
  it("next clock time is today or tomorrow", () => {
    const now = new Date(2026, 10, 26, 18, 0);
    expect(new Date(nextClock("19:30", now)).getDate()).toBe(26);
    expect(new Date(nextClock("08:00", now)).getDate()).toBe(27);
    expect(nextClock("25:00", now)).toBe("");
    expect(nextClock("soon", now)).toBe("");
  });
  it("ages and verdicts", () => {
    expect(ageText(90)).toBe("2 min");
    expect(ageText(7200)).toBe("2 h");
    expect(ageText(3 * 86400)).toBe("3 d");
    expect(verdictText("approve")).toBe("Approved");
    expect(verdictText("")).toBe("Reviewing");
  });
});

describe("landable (R10 Opus B3)", () => {
  it("leaves out a job whose changes were requested: its worktree is the fix job's", () => {
    const item = (id: string, changes_state?: string) => ({ job: { id, title: id, state: "done", worktree_branch: "b", work_dir: "w", land_state: "", land_detail: "", changes_state }, age_seconds: 1, dirty: false });
    expect(landable([item("j_18", "requested"), item("j_25")]).map((entry) => entry.job.id)).toEqual(["j_25"]);
  });
});
