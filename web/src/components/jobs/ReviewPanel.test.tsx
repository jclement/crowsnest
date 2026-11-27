import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { makeDetail } from "../../lib/jobFixtures";
import type { ReviewView } from "../../lib/landing";
import { renderWithClient, stubApi } from "../../test/render";
import { ReviewPanel } from "./ReviewPanel";

const view: ReviewView = {
  job_id: "j_1",
  produced: {
    branch: "job/1-retry",
    base: "main",
    diff_base: "abc",
    head: "def",
    ahead: 2,
    behind: 0,
    commits: [{ hash: "deadbeefcafe", subject: "Fetch: retry", author: "agent", at: "2026-11-26T20:00:00Z" }],
    files: [
      { path: "retry.go", status: "modified", added: 10, deleted: 2, binary: false, group: "server", churn: false },
      { path: "logo.png", status: "added", added: 0, deleted: 0, binary: true, group: "churn", churn: true },
    ],
    added: 10,
    deleted: 2,
    clean: true,
    dirty: [],
    pushed: false,
  },
  result_md: "Retries three times.",
  walkthrough_md: "## retry.go\nThe loop.",
  verified: ["go test"],
  not_verified: ["the real service"],
  reviews: [{ job_id: "j_2", round: 1, engine: "codex", state: "done", verdict: "approve", findings: "VERDICT: approve\nGood." }],
  land: {
    state: "",
    detail: "",
    torn_down: false,
    base: "main",
    branch: "job/1-retry",
    merge_mode: "merge",
    base_protected: false,
    allowed: { merge: true, pr: true, discard: true },
    confirm: { merge: "Merge job/1-retry into main with a merge commit.", pr: "Push it.", discard: "Throw it away." },
  },
};

function render(review: ReviewView = view) {
  const calls = stubApi((call) => {
    if (call.path === "/jobs/j_1/review") return { body: { data: review } };
    if (call.path.startsWith("/jobs/j_1/review/diff")) return { body: { data: { path: "retry.go", diff: "@@ -1,1 +1,2 @@\n-old\n+new\n+more\n", binary: false, cut: false } } };
    if (call.path === "/jobs/j_1/land") return { body: { data: { action: "merge", detail: "Merged into main", torn_down: true } } };
    return undefined;
  });
  renderWithClient(<ReviewPanel job={makeDetail({ worktree_branch: "job/1-retry", state: "done" })} />);
  return calls;
}

describe("ReviewPanel", () => {
  it("shows what the job produced with the agent's account and the review beside it", async () => {
    render();
    expect(await screen.findByText("2 commits ahead of main")).toBeInTheDocument();
    expect(screen.getByText("Fetch: retry")).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Review rounds" })).toHaveTextContent("Approved");
    expect(screen.getByText("The loop.")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Not verified" })).toHaveTextContent("the real service");
  });

  it("loads a file's diff only when opened, and never shows a binary file", async () => {
    const calls = render();
    const files = await screen.findByRole("list", { name: "Changed files" });
    expect(calls.some((call) => call.path.includes("/review/diff"))).toBe(false);
    await userEvent.click(within(files).getByRole("button", { name: /retry\.go/ }));
    expect(await screen.findByText("more")).toBeInTheDocument();
    await userEvent.click(within(files).getByRole("button", { name: /logo\.png/ }));
    expect(screen.getByText(/Binary file/)).toBeInTheDocument();
    expect(calls.filter((call) => call.path.includes("/review/diff")).length).toBe(1);
  });

  it("lands only after a confirmation that says what will happen", async () => {
    const calls = render();
    await userEvent.click(await screen.findByRole("button", { name: "Merge" }));
    const dialog = screen.getByRole("alertdialog", { name: "Confirm Merge" });
    expect(dialog).toHaveTextContent("Merge job/1-retry into main with a merge commit.");
    expect(calls.some((call) => call.path === "/jobs/j_1/land")).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Yes, merge" }));
    await waitFor(() => expect(calls.find((call) => call.path === "/jobs/j_1/land")?.body).toEqual({ action: "merge", confirm: true }));
  });

  it("says why an action is closed", async () => {
    render({ ...view, land: { ...view.land, base_protected: true, allowed: { pr: true, discard: true }, why: { merge: "main is a protected branch" } } });
    expect(await screen.findByRole("button", { name: "Merge" })).toBeDisabled();
    expect(screen.getByText(/Merge: main is a protected branch/)).toBeInTheDocument();
  });
});
