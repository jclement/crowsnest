// nextStep: the one chip a job card shows (R8), what happens next and who owns it. First the
// table of the cases the review named and every hold reason; then every combination of the
// fields it reads that the API can produce, each of which must give a known label, a tone and an
// owner, and agree with the board's section (a chip that says "Waiting on you" is in the
// attention section; a finished job's chip never does).
import { describe, expect, it } from "vitest";
import type { AcceptState } from "./accept";
import { nextOwnerText, nextStep, type JobCause, type JobState, type NextStepInput } from "./jobs";

const base: NextStepInput = { state: "running", stuck: false, cause: "", cause_detail: "", worktree_branch: "" };

const cases: Array<[string, Partial<NextStepInput>, string, string]> = [
  ["a running job", {}, "Running", "agent"],
  ["a check failed and the agent is fixing it", { accept_state: "failed" }, "Fixing check", "agent"],
  ["crowsnest is running the check", { state: "in_review", accept_state: "checking" }, "Checking", "crowsnest"],
  ["a reviewer has it", { state: "in_review" }, "In review", "reviewer"],
  ["a done worktree job, accepted", { state: "done", worktree_branch: "job/j_1", accept_state: "accepted" }, "Ready to land", "you"],
  ["a done worktree job with no check", { state: "done", worktree_branch: "job/j_1", accept_state: "unverified" }, "Ready to land", "you"],
  ["a done worktree job behind its base", { state: "done", worktree_branch: "job/j_1", accept_state: "needs_rebase" }, "Needs rebase", "you"],
  ["evidence older than the branch", { state: "done", worktree_branch: "job/j_1", accept_state: "stale" }, "Re-check to land", "you"],
  ["merged", { state: "done", worktree_branch: "job/j_1", land_state: "merged" }, "Merged", "nobody"],
  ["a pull request", { state: "done", worktree_branch: "job/j_1", land_state: "pr" }, "PR open", "nobody"],
  ["discarded", { state: "done", worktree_branch: "job/j_1", land_state: "discarded" }, "Discarded", "nobody"],
  ["a reviewer's own job is just done", { state: "done", worktree_branch: "job/j_1", review_of: "j_9" }, "Done", "nobody"],
  ["a torn-down worktree", { state: "done", worktree_branch: "job/j_1", torn_down_at: "2026-11-27T00:00:00Z" }, "Done", "nobody"],
  ["no worktree", { state: "done" }, "Done", "nobody"],
  ["a question", { state: "needs_you", cause: "questions" }, "Waiting on you", "you"],
  ["a dialog", { state: "needs_you", cause: "dialog" }, "Waiting on you", "you"],
  ["a tool call waiting for approval", { state: "needs_you", cause: "approval-tool" }, "Waiting on you", "you"],
  ["over its budget", { state: "needs_you", cause: "budget" }, "Held: budget", "you"],
  ["the check still fails", { state: "needs_you", cause: "check" }, "Check failed", "you"],
  ["the reviewer still objects", { state: "needs_you", cause: "review", worktree_branch: "job/j_1" }, "Review objects", "you"],
  ["asked to start beyond its own directory", { state: "queued", cause: "approval" }, "Approve to start", "you"],
  ["paused", { state: "queued", cause: "held", cause_detail: "crowsnest's automation is paused; it starts when you resume (or Start anyway)" }, "Held: paused", "you"],
  ["the fleet's usage limit", { state: "queued", cause: "held", cause_detail: "usage limit: resets 3pm" }, "Held: usage limit", "crowsnest"],
  ["the spend ceiling", { state: "queued", cause: "held", cause_detail: "spend ceiling: $40 of $40 today" }, "Held: spend ceiling", "you"],
  ["the machine is busy", { state: "queued", cause: "held", cause_detail: "waiting: load 40 on 16 cores" }, "Held: machine busy", "crowsnest"],
  ["a hold the human lifts", { state: "queued", cause: "held", cause_detail: "held: the brief names a protected host" }, "Held for you", "you"],
  ["no free slot", { state: "queued", cause: "held", cause_detail: "waiting for a slot: 4 of 4 running" }, "Waiting for a slot", "crowsnest"],
  ["queued", { state: "queued" }, "Queued", "crowsnest"],
  ["a usage limit while running", { stuck: true, cause: "limit", cause_detail: "usage limit reached; resets 3pm" }, "Held: usage limit", "crowsnest"],
  ["a rate limit, retried", { stuck: true, cause: "limit", cause_detail: "429" }, "Retrying", "crowsnest"],
  ["a login to fix", { stuck: true, cause: "auth" }, "Waiting on you", "you"],
  ["three nudges with no progress", { stuck: true, cause: "unresponsive" }, "Stuck", "crowsnest"],
  ["waiting on the human by a declared wait", { state: "blocked", wake_kind: "human" }, "Waiting on you", "you"],
  ["waiting on another job", { state: "blocked", wake_kind: "job" }, "Waiting on a job", "agent"],
  ["waiting until a time", { state: "blocked", wake_kind: "time" }, "Waiting", "crowsnest"],
  ["blocked", { state: "blocked" }, "Blocked", "agent"],
  ["failed", { state: "failed" }, "Failed", "you"],
  ["cancelled", { state: "cancelled" }, "Cancelled", "nobody"],
];

describe("nextStep", () => {
  it.each(cases)("%s: %s", (_what, fields, label, owner) => {
    const step = nextStep({ ...base, ...fields });
    expect(step.label).toBe(label);
    expect(step.owner).toBe(owner);
  });

  it("says who owns the next step in words", () => {
    expect(nextOwnerText("you")).toBe("Waiting on you");
    expect(nextOwnerText("nobody")).toBe("Nothing left to do");
  });

  // Every value of every field nextStep reads, as the Go types define them (internal/jobs/types.go).
  const states: JobState[] = ["queued", "running", "needs_you", "blocked", "in_review", "done", "failed", "cancelled"];
  const causes: JobCause[] = ["", "questions", "dialog", "unresponsive", "permissions", "limit", "server_error", "auth", "silent", "idle", "exited", "launch", "config", "approval", "approval-tool", "held", "review", "check", "budget", "spinning"];
  const accepts: AcceptState[] = ["", "checking", "accepted", "unverified", "failed", "stale", "needs_rebase"];
  const lands = ["", "merged", "pr", "discarded"] as const;
  const wakes = ["", "time", "job", "human"] as const;
  const details = ["", "usage limit: soon", "crowsnest's automation is paused", "spend ceiling: x", "waiting: load", "held: why", "waiting for a slot: 1 of 1"];
  const labels = new Set(cases.map(([, , label]) => label));

  it("gives every combination the API can produce a known label, a tone and an owner", { timeout: 30_000 }, () => {
    let combinations = 0;
    const wrong: string[] = [];
    for (const state of states)
      for (const stuck of [false, true])
        for (const cause of causes)
          for (const accept_state of accepts)
            for (const land_state of lands)
              for (const wake_kind of wakes)
                for (const worktree of ["", "job/j_1"])
                  for (const review_of of ["", "j_9"])
                    for (const cause_detail of details) {
                      const step = nextStep({ state, stuck, cause, cause_detail, accept_state, land_state, wake_kind, worktree_branch: worktree, review_of });
                      combinations++;
                      const where = `${state}/${stuck}/${cause}/${accept_state}/${land_state}/${wake_kind}: ${step.label}`;
                      if (!labels.has(step.label) || !step.tone) wrong.push(where);
                      // A finished job is never the agent's to move on.
                      if ((state === "done" || state === "cancelled" || state === "failed") && step.owner === "agent") wrong.push(`${where} (agent)`);
                      // "Waiting on you" is always the human's.
                      if (step.label === "Waiting on you" && step.owner !== "you") wrong.push(`${where} (${step.owner})`);
                    }
    expect(wrong.slice(0, 10)).toEqual([]);
    expect(combinations).toBeGreaterThan(100_000);
  });
});
