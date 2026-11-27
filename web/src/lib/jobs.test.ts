import { describe, expect, it } from "vitest";
import { makeBoard, makeJob } from "./jobFixtures";
import { actionFor, boardSections, cardLine, childrenOf, jobAnswerText, ownershipChain, retryLabel, sectionOf, stateChip, type Job } from "./jobs";

describe("the board", () => {
  const now = new Date("2026-11-26T12:00:00Z");

  it("puts needs-you and stuck first, then running, then finished today, older last", () => {
    const sections = boardSections(makeBoard(now.getTime()), now);
    expect(sections.attention.map((job) => job.id)).toEqual(["j_4", "j_3"]); // needs-you before stuck
    expect(sections.running.map((job) => job.id)).toEqual(["j_2", "j_1"]); // most recently active first
    expect(sections.today.map((job) => job.id)).toEqual(["j_5"]);
    expect(sections.older.map((job) => job.id)).toEqual(["j_6"]);
  });

  it("orders stuck jobs by how long they have waited", () => {
    const older = makeJob({ id: "j_a", stuck: true, cause: "idle", last_activity_at: "2026-11-26T10:00:00Z" });
    const newer = makeJob({ id: "j_b", stuck: true, cause: "idle", last_activity_at: "2026-11-26T11:00:00Z" });
    expect(boardSections([newer, older], now).attention.map((job) => job.id)).toEqual(["j_a", "j_b"]);
  });

  it("files a queued or blocked job with the running ones", () => {
    expect(sectionOf(makeJob({ state: "queued" }), now)).toBe("running");
    expect(sectionOf(makeJob({ state: "queued", cause: "approval" }), now)).toBe("attention");
    expect(sectionOf(makeJob({ state: "blocked" }), now)).toBe("running");
    expect(sectionOf(makeJob({ state: "cancelled", settled_at: "2026-11-26T08:00:00Z" }), now)).toBe("today");
  });
});

describe("card states", () => {
  it("labels a stuck running job Stuck and a finished one by its end", () => {
    expect(stateChip({ state: "running", stuck: true })).toEqual({ label: "Stuck", tone: "warning" });
    expect(stateChip({ state: "needs_you", stuck: false }).tone).toBe("danger");
    expect(stateChip({ state: "done", stuck: true }).label).toBe("Done");
  });

  it("says why, what it is doing, or how it ended", () => {
    expect(cardLine(makeJob({ stuck: true, cause: "limit", cause_text: "Hit a usage or rate limit", cause_detail: "resets at 2" }))).toBe("Hit a usage or rate limit: resets at 2");
    expect(cardLine(makeJob({ doing_now: "Running tests" }))).toBe("Running tests");
    expect(cardLine(makeJob({ state: "done", result_md: "# Fixed it\n\nmore" }))).toBe("Fixed it");
    expect(cardLine(makeJob({ state: "blocked", blocked_reason: "CI down" }))).toBe("Blocked: CI down");
  });

  it("picks the one action that fits each cause", () => {
    expect(actionFor({ state: "running", stuck: true, cause: "limit" })).toBe("retry");
    expect(actionFor({ state: "queued", stuck: false, cause: "approval" })).toBe("inbox");
    expect(actionFor({ state: "needs_you", stuck: false, cause: "dialog" })).toBe("terminal");
    expect(actionFor({ state: "running", stuck: true, cause: "unresponsive" })).toBe("nudge");
    expect(actionFor({ state: "needs_you", stuck: false, cause: "questions" })).toBe("inbox");
    expect(actionFor({ state: "running", stuck: true, cause: "exited" })).toBe("resume");
    expect(actionFor({ state: "done", stuck: false, cause: "" })).toBeNull();
  });

  it("counts down to a retry", () => {
    const now = Date.parse("2026-11-26T12:00:00Z");
    expect(retryLabel({ stuck: true, retry_at: "2026-11-26T12:04:00Z" }, now)).toBe("Retry in 4 min");
    expect(retryLabel({ stuck: true, retry_at: "2026-11-26T11:59:00Z" }, now)).toBe("Retrying now");
    expect(retryLabel({ stuck: false, retry_at: "2026-11-26T12:04:00Z" }, now)).toBe("");
  });
});

describe("jobAnswerText", () => {
  const base = { id: "q_8", title: "Ship v0.2 on Friday?", urgency: "normal", wait: false, answer_note: "", created_at: "2026-11-26T00:00:00Z" };
  it("shows a choice by its label, not its key (found by the M9 e2e suite)", () => {
    const options = [{ key: "fri", label: "Friday" }, { key: "mon", label: "Next Monday" }];
    expect(jobAnswerText({ ...base, kind: "choice", status: "answered", answer_value: "fri", options })).toBe("Friday");
    expect(jobAnswerText({ ...base, kind: "choice", status: "answered", answer_value: ["fri", "mon"], answer_note: "either", options })).toBe("Friday, Next Monday (note: either)");
  });
  it("words the other kinds as the Inbox does", () => {
    expect(jobAnswerText({ ...base, kind: "manual", status: "answered", answer_value: "Use SQLite", answer_note: "Use SQLite", options: [] })).toBe("Use SQLite");
    expect(jobAnswerText({ ...base, kind: "ack", status: "answered", answer_value: "ack", options: [] })).toBe("Acknowledged");
    expect(jobAnswerText({ ...base, kind: "choice", status: "expired", answer_value: null, options: [] })).toBe("expired with no answer");
    expect(jobAnswerText({ ...base, kind: "choice", status: "cancelled", answer_value: null, options: [] })).toBe("cancelled");
  });
});

describe("ownershipChain", () => {
  const base = { role: "builder", state: "running", created_at: "2026-11-26T00:00:00Z" } as const;
  const lead = { ...base, id: "j_1", role: "lead", title: "Plan" } as unknown as Job;
  const child = { ...base, id: "j_2", title: "Build", parent_id: "j_1" } as unknown as Job;
  const grandchild = { ...base, id: "j_3", role: "reviewer", title: "Review", parent_id: "j_2", created_at: "2026-11-26T01:00:00Z" } as unknown as Job;

  it("runs from the human through the project's lead and every ancestor to the job", () => {
    expect(ownershipChain(grandchild, [lead, child, grandchild], "Crowsnest").map((link) => link.label)).toEqual(["you", "lead Crowsnest", "lead j_1", "builder j_2", "reviewer j_3"]);
    expect(ownershipChain(lead, [lead], undefined).map((link) => link.label)).toEqual(["you", "orchestrator", "lead j_1"]);
  });

  it("lists a job's children", () => {
    expect(childrenOf(child, [lead, child, grandchild]).map((job) => job.id)).toEqual(["j_3"]);
  });
});
