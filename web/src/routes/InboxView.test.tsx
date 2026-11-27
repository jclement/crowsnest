import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ToastProvider } from "../components/ui/Toast";
import { makeFleet, makeScreens } from "../lib/fixtures";
import { fixtureQuestions } from "../lib/questionFixtures";
import { fixtureSnapshot } from "../lib/attentionFixtures";
import type { AttentionItem } from "../lib/attention";
import { stubApi } from "../test/render";
import { InboxView } from "./InboxView";

function renderInbox(path = "/inbox") {
  const root = createRootRoute();
  const inbox = createRoute({
    getParentRoute: () => root,
    path: "/inbox",
    validateSearch: (search: Record<string, unknown>): { q?: string } => ({ q: typeof search.q === "string" ? search.q : undefined }),
    component: InboxView,
  });
  const fleet = createRoute({ getParentRoute: () => root, path: "/fleet", component: () => <p>fleet</p> });
  const decide = createRoute({ getParentRoute: () => root, path: "/decide", component: () => <p>decision packet</p> });
  const router = createRouter({ routeTree: root.addChildren([inbox, fleet, decide]), history: createMemoryHistory({ initialEntries: [path] }) });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return router;
}

function stubInbox() {
  const fleet = makeFleet();
  const screens = makeScreens();
  const snapshot = fixtureSnapshot([], fixtureQuestions);
  const dialog: AttentionItem = {
    id: "dialog:w3:p1", kind: "dialog", urgency: "blocking", rank: 40, since: new Date().toISOString(), title: "Dialog: Rebuild the docs site",
    project: { id: 2, slug: "website", name: "Website" }, job_id: "j_5", job_title: "Rebuild the docs site", agent: "job-5", pane_id: "w3:p1",
    action: { kind: "dialog", label: "Answer the dialog" }, dialog_agent: fleet.agents.find((agent) => agent.pane_id === "w3:p1"),
  };
  // The server orders the queue; the test puts the dialog second, as a blocking question outranks it.
  snapshot.items.splice(1, 0, dialog);
  snapshot.counts.total += 1;
  snapshot.counts.blocking += 1;
  return stubApi((call) => {
    if (call.path === "/attention") return { body: { data: snapshot } };
    if (call.path.startsWith("/questions?status=open")) return { body: { data: fixtureQuestions.filter((q) => q.status === "open"), meta: { total: 6 } } };
    if (call.path.startsWith("/questions?status=")) return { body: { data: fixtureQuestions.filter((q) => q.status !== "open") } };
    const one = /^\/questions\/(q_\d+)$/.exec(call.path);
    if (one) return { body: { data: fixtureQuestions.find((q) => q.id === one[1]) } };
    if (call.path === "/fleet") return { body: { data: fleet } };
    const screenMatch = /^\/agents\/([^/]+)\/screen$/.exec(call.path);
    if (screenMatch) return { body: { data: screens[decodeURIComponent(screenMatch[1]!)]?.screen } };
    return undefined;
  });
}

describe("InboxView", () => {
  it("lists the attention queue in the server's order, each with its project and job, and notices apart", async () => {
    stubInbox();
    renderInbox();
    const list = await screen.findByRole("list", { name: "Needs you" });
    await waitFor(() => expect(within(list).getAllByText(/Dialog: Rebuild the docs site/).length).toBeGreaterThan(0));
    const rows = within(list).getAllByRole("button").map((row) => row.textContent ?? "");
    expect(rows[0]).toContain("Apply the fleet cache refactor?");
    expect(rows[1]).toContain("Dialog: Rebuild the docs site");
    expect(rows[1]).toContain("Website · Rebuild the docs site");
    expect(rows.every((row) => !row.includes("Nightly backup finished"))).toBe(true);
    const notices = screen.getByRole("list", { name: "Notices" });
    expect(within(notices).getByText(/Nightly backup finished/)).toBeInTheDocument();
    // The count line is the snapshot's own total: the badge's number.
    expect(screen.getByText(/need you: \d+ blocking/)).toHaveTextContent(`${rows.length} need you`);
  });

  it("on a phone, an open question opens its decision packet", async () => {
    stubInbox();
    const router = renderInbox();
    await userEvent.setup().click(await screen.findByRole("button", { name: /Apply the fleet cache refactor/ }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/decide"));
    expect(router.state.location.search).toEqual({ item: "question:q_1" });
  });

  it("a link to a question opens it with its body, diff and options", async () => {
    stubInbox();
    renderInbox("/inbox?q=q_1");
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("region", { name: "Diff of internal/fleet/cache.go" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: /Apply it/ })).toHaveTextContent("Recommended");
    expect(within(dialog).getByText("crowsnest")).toBeInTheDocument();
  });

  it("opens a terminal dialog as a dialog card", async () => {
    stubInbox();
    renderInbox("/inbox?q=dialog:w3:p1");
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByLabelText("Screen text")).toBeInTheDocument();
  });

  it("shows settled questions in History with their answer", async () => {
    stubInbox();
    renderInbox();
    await userEvent.setup().click(await screen.findByRole("tab", { name: "History" }));
    const list = await screen.findByRole("list", { name: "History" });
    expect(within(list).getByRole("button")).toHaveTextContent("→ Yes");
  });
});
