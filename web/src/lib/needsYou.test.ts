import { describe, expect, it } from "vitest";
import { fixtureApprovals } from "./approvalFixtures";
import { trayItems, trayTitle } from "./needsYou";
import { fixtureQuestions } from "./questionFixtures";
import type { Job } from "./jobs";
import type { Agent } from "./types";

const agent = (pane: string, needs: boolean): Agent => ({ pane_id: pane, name: `agent-${pane}`, needs_you: needs, workspace: "ws" }) as unknown as Agent;
const job = (id: string, fields: Partial<Job>): Job => ({ id, title: `job ${id}`, state: "running", stuck: false, cause: "", created_at: "2026-11-26T00:00:00Z", last_activity_at: null, ...fields }) as Job;

describe("trayItems", () => {
  it("puts the dangerous approval first and blocking questions before normal ones", () => {
    const items = trayItems({ approvals: fixtureApprovals, questions: fixtureQuestions });
    expect(items[0]?.id).toBe("approval:ap_3");
    const blocking = items.findIndex((item) => item.kind === "question" && item.question.urgency === "blocking");
    const normal = items.findIndex((item) => item.kind === "question" && item.question.urgency === "normal");
    expect(blocking).toBeGreaterThan(-1);
    expect(normal).toBeGreaterThan(blocking);
  });

  it("leaves out notices, settled questions and settled approvals", () => {
    const settled = { ...fixtureApprovals[0]!, id: "ap_9", status: "allowed" as const };
    const items = trayItems({ approvals: [settled], questions: fixtureQuestions });
    expect(items.some((item) => item.kind === "question" && (item.question.kind === "notice" || item.question.status !== "open"))).toBe(false);
    expect(items.some((item) => item.id === "approval:ap_9")).toBe(false);
  });

  it("shows an agent's dialog once: as its approval when one is pending", () => {
    const items = trayItems({ approvals: fixtureApprovals, agents: [agent("w1:p2", true), agent("w9:p9", true), agent("w8:p8", false)] });
    expect(items.some((item) => item.id === "dialog:w1:p2")).toBe(false);
    expect(items.some((item) => item.id === "dialog:w9:p9")).toBe(true);
    expect(items.some((item) => item.id === "dialog:w8:p8")).toBe(false);
  });

  it("includes stuck jobs and jobs only the human can fix, not ones waiting on other items", () => {
    const items = trayItems({
      jobs: [
        job("j_1", { stuck: true, cause: "limit" }),
        job("j_2", { state: "needs_you", cause: "auth" }),
        job("j_3", { state: "needs_you", cause: "approval-tool" }),
        job("j_4", { state: "done", stuck: true }),
      ],
    });
    expect(items.map((item) => item.id)).toEqual(["job:j_1", "job:j_2"]);
  });

  it("leaves out snoozed items until the snooze runs out", () => {
    const now = Date.now();
    const snoozed = { "approval:ap_1": now + 60_000, "approval:ap_2": now - 1 };
    const ids = trayItems({ approvals: fixtureApprovals }, snoozed, now).map((item) => item.id);
    expect(ids).not.toContain("approval:ap_1");
    expect(ids).toContain("approval:ap_2");
  });

  it("titles each item in a line", () => {
    const [item] = trayItems({ approvals: [fixtureApprovals[0]!] });
    expect(trayTitle(item!)).toBe("Bash: go test ./internal/fleet/...");
  });
});
