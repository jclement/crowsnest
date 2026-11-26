import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ToastProvider } from "../components/ui/Toast";
import { makeFleet, makeScreens } from "../lib/fixtures";
import { fixtureQuestions } from "../lib/questionFixtures";
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
  const router = createRouter({ routeTree: root.addChildren([inbox, fleet]), history: createMemoryHistory({ initialEntries: [path] }) });
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
  return stubApi((call) => {
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
  it("lists blocking questions first, then dialogs, then the rest", async () => {
    stubInbox();
    renderInbox();
    const list = await screen.findByRole("list", { name: "Needs you" });
    await waitFor(() => expect(within(list).getAllByText(/is waiting at a dialog/).length).toBeGreaterThan(0));
    const rows = within(list).getAllByRole("button").map((row) => row.textContent ?? "");
    expect(rows[0]).toContain("Apply the fleet cache refactor?");
    expect(rows[1]).toContain("is waiting at a dialog");
    expect(rows.at(-1)).toContain("Nightly backup finished");
    expect(rows.some((row) => row.includes("1 file"))).toBe(true);
  });

  it("opens a question with its body, diff and options", async () => {
    stubInbox();
    const router = renderInbox();
    await userEvent.setup().click(await screen.findByRole("button", { name: /Apply the fleet cache refactor/ }));
    await waitFor(() => expect(router.state.location.search).toEqual({ q: "q_1" }));
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
