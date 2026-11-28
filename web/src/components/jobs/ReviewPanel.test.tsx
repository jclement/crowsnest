import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
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

// The live diff (components/changes) the Review tab embeds: two committed files, one viewed.
const diffFile = (path: string, viewed: boolean) => ({
  path, status: "modified", added: 3, deleted: 1, binary: false, old_blob: "a".repeat(40), new_blob: "b".repeat(40), hash: path + "-h",
  group: "server", churn: false, viewed, changed_since_viewed: false, since_available: false,
});
const summary = (scope: string) => ({
  subject: "j_1", kind: "job", label: "j_1: Retry", root: "/w", job_id: "j_1", scope, base: "abc", base_label: "where it left main", base_ref: "",
  head: "def", commits: [], files: [diffFile("retry.go", true), diffFile("store.go", false)], added: 6, deleted: 2, viewed: 1, total: 2,
  truncated: false, computed_at: "2026-11-27T10:00:00Z", revision: "r1",
  scopes: ["all", "committed", "uncommitted", "since_viewed"].map((name) => ({ name, label: name, files: 2, added: 6, deleted: 2 })),
});

function render(review: ReviewView = view, landRefusal?: string) {
  const calls = stubApi((call) => {
    const changes = /^\/changes\/jobs\/j_1\?scope=(\w+)/.exec(call.path);
    if (changes) return { body: { data: summary(changes[1] ?? "all") } };
    if (call.path.startsWith("/changes/jobs/j_1/file")) return { body: { data: { path: "store.go", status: "modified", hash: "store.go-h", diff: "@@ -1,1 +1,1 @@\n-old\n+new\n", binary: false, too_large: false, cut: false, old_text: "old\n", new_text: "new\n", since: false, context: 0 } } };
    if (call.path === "/jobs/j_1/review") return { body: { data: review } };
    if (call.path.startsWith("/jobs/j_1/review/diff")) return { body: { data: { path: "retry.go", diff: "@@ -1,1 +1,2 @@\n-old\n+new\n+more\n", binary: false, cut: false } } };
    if (call.path === "/jobs/j_1/land" && landRefusal) return { status: 409, body: { error: { code: "LAND_REFUSED", message: landRefusal } } };
    if (call.path === "/jobs/j_1/land") return { body: { data: { action: "merge", detail: "Merged into main", torn_down: true } } };
    return undefined;
  });
  const root = createRootRoute();
  const home = createRoute({ getParentRoute: () => root, path: "/", component: () => <ReviewPanel job={makeDetail({ worktree_branch: "job/1-retry", state: "done" })} /> });
  const router = createRouter({ routeTree: root.addChildren([home]), history: createMemoryHistory({ initialEntries: ["/"] }) });
  renderWithClient(<RouterProvider router={router} />);
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

  it("embeds the live diff viewer and says how many committed files are not viewed", async () => {
    render();
    expect(await screen.findByRole("region", { name: "Changes: j_1: Retry" })).toBeInTheDocument();
    expect(await screen.findByText("1 of 2 committed files not viewed")).toBeInTheDocument();
  });

  it("lands only after a confirmation that says what will happen", async () => {
    const calls = render();
    await userEvent.click(await screen.findByRole("button", { name: "Merge" }));
    const dialog = screen.getByRole("alertdialog", { name: "Confirm Merge" });
    // Short lines first (R10 Fable F9); the server's exact sentence under "Exactly what happens".
    expect(dialog).toHaveTextContent("Merges job/1-retry into main.");
    expect(dialog).toHaveTextContent("Merge job/1-retry into main with a merge commit.");
    expect(dialog).toHaveTextContent("1 of 2 committed files are not marked viewed.");
    expect(calls.some((call) => call.path === "/jobs/j_1/land")).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Yes, merge" }));
    await waitFor(() => expect(calls.find((call) => call.path === "/jobs/j_1/land")?.body).toEqual({ action: "merge", confirm: true }));
  });

  it("a refused land says why in the card, with Try again, not only in a toast (R10 Opus B7)", async () => {
    render(view, "the main checkout (/w) has uncommitted changes (README.md); commit or stash them there first. Nothing was merged");
    await userEvent.click(await screen.findByRole("button", { name: "Merge" }));
    await userEvent.click(within(screen.getByRole("alertdialog", { name: "Confirm Merge" })).getByRole("button", { name: "Yes, merge" }));
    const refused = await screen.findByTestId("land-refused");
    expect(refused).toHaveTextContent("Merge refused: the main checkout (/w) has uncommitted changes (README.md)");
    await userEvent.click(within(refused).getByRole("button", { name: "Try again" }));
    expect(screen.getByRole("alertdialog", { name: "Confirm Merge" })).toBeInTheDocument();
  });

  it("says why an action is closed", async () => {
    render({ ...view, land: { ...view.land, base_protected: true, allowed: { pr: true, discard: true }, why: { merge: "main is a protected branch" } } });
    expect(await screen.findByRole("button", { name: "Merge" })).toBeDisabled();
    expect(screen.getByText(/Merge: main is a protected branch/)).toBeInTheDocument();
  });
});
