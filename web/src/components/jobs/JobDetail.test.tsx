import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { makeFleet } from "../../lib/fixtures";
import { makeDetail } from "../../lib/jobFixtures";
import type { JobDetail as Detail } from "../../lib/jobs";
import { fixtureQuestions } from "../../lib/questionFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { JobDetail } from "./JobDetail";

function renderDetail(detail: Detail) {
  const root = createRootRoute();
  const home = createRoute({ getParentRoute: () => root, path: "/", component: () => <JobDetail id={detail.id} /> });
  const inbox = createRoute({ getParentRoute: () => root, path: "/inbox", component: () => <p>inbox</p> });
  const router = createRouter({ routeTree: root.addChildren([home, inbox]), history: createMemoryHistory({ initialEntries: ["/"] }) });
  const calls = stubApi((call) => {
    if (call.path === `/jobs/${detail.id}`) return { body: { data: detail } };
    if (/^\/jobs\/j_\d+\/(nudge|resume|cancel|message)$/.test(call.path)) return { body: { data: { ...detail, queued: false, started: true } } };
    if (call.path === "/fleet") return { body: { data: makeFleet() } };
    const question = /^\/questions\/(q_\d+)$/.exec(call.path);
    if (question) return { body: { data: fixtureQuestions.find((q) => q.id === question[1]) } };
    return undefined;
  });
  renderWithClient(<RouterProvider router={router} />);
  return calls;
}

describe("JobDetail", () => {
  it("shows the timeline, the brief and a message box for a running job", async () => {
    const calls = renderDetail(makeDetail());
    expect(await screen.findByRole("heading", { name: "Add retry back-off to the webhook sender" })).toBeInTheDocument();
    const timeline = screen.getByRole("list", { name: "Timeline" });
    expect(within(timeline).getByText(/Nudged \(1 of 3\)/)).toBeInTheDocument();
    expect(within(timeline).getAllByRole("listitem")[0]).toHaveTextContent("Nudged"); // newest first
    await userEvent.type(screen.getByLabelText(/Message to job-1/), "use the v2 API");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(calls.find((call) => call.path === "/jobs/j_1/message")?.body).toEqual({ text: "use the v2 API", force_over_draft: false }));
  });

  it("says why a stuck job is stuck and offers the one action that fits", async () => {
    const calls = renderDetail(makeDetail({ stuck: true, cause: "unresponsive", cause_text: "Stopped three times without saying why", cause_detail: "Ended its turn 4 times" }));
    const banner = await screen.findByRole("alert");
    expect(banner).toHaveTextContent("Stopped three times without saying why");
    await userEvent.click(within(banner).getByRole("button", { name: "Nudge" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_1/nudge" && call.method === "POST")).toBe(true));
  });

  it("offers Retry now with the countdown for a limit", async () => {
    const calls = renderDetail(makeDetail({ stuck: true, cause: "limit", cause_text: "Hit a usage or rate limit", retry_at: new Date(Date.now() + 10 * 60_000).toISOString(), retry_count: 1 }));
    const banner = await screen.findByRole("alert");
    expect(banner).toHaveTextContent("Retry in 10 min (attempt 2)");
    await userEvent.click(within(banner).getByRole("button", { name: "Retry now" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_1/resume")).toBe(true));
  });

  it("shows the result with what was verified and what was not", async () => {
    renderDetail(makeDetail({ state: "done", result_md: "Fixed the **race**.", verified: ["npm test: 50 runs"], not_verified: ["Safari"] }));
    // The agent's own account is labelled as such: crowsnest's evidence is a separate card (6.15).
    const verified = await screen.findByRole("region", { name: "Verified (reported by the agent)" });
    expect(within(verified).getByText("npm test: 50 runs")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Not verified (reported by the agent)" })).getByText("Safari")).toBeInTheDocument();
    expect(screen.queryByLabelText(/Message to/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel job" })).not.toBeInTheDocument();
  });

  it("puts an open question right in the job, answerable there", async () => {
    const open = fixtureQuestions.find((q) => q.status === "open" && q.kind === "confirm")!;
    renderDetail(makeDetail({ state: "needs_you", cause: "questions", cause_text: "Waiting on your answers", questions: [{ id: open.id, title: open.title, kind: open.kind, status: "open", urgency: open.urgency, wait: false, answer_value: null, answer_note: "", created_at: open.created_at, options: open.options }] }));
    expect(await screen.findByRole("article", { name: open.title })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Answer in Inbox" })).toBeInTheDocument();
  });

  it("a finished job sent back with comments says Changes requested in its header, not Done · Accepted (R10 Opus B3)", async () => {
    renderDetail(
      makeDetail({ id: "j_18", state: "done", worktree_branch: "job/91-retry", accept_state: "accepted", changes_state: "requested", fix_job: "j_30", pane_id: "" }),
    );
    const header = await screen.findByRole("heading", { name: "Add retry back-off to the webhook sender" });
    const chips = header.parentElement!;
    expect(within(chips).getByText("Changes requested")).toBeInTheDocument();
    expect(within(chips).queryByText("Accepted")).toBeNull();
    expect(within(chips).queryByText("Done")).toBeNull();
  });

  it("asks before cancelling", async () => {
    const calls = renderDetail(makeDetail());
    await userEvent.click(await screen.findByRole("button", { name: "Cancel job" }));
    expect(calls.some((call) => call.path.endsWith("/cancel"))).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Cancel the job" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_1/cancel")).toBe(true));
  });
});
