import { isRoutine } from "../../lib/decision";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { fixtureApprovals } from "../../lib/approvalFixtures";
import { fixtureSnapshot } from "../../lib/attentionFixtures";
import { FOCUS_TRAY_EVENT } from "../../lib/needsYou";
import { fixtureQuestions } from "../../lib/questionFixtures";
import type { Approval } from "../../lib/approvals";
import { stubApi } from "../../test/render";
import { ToastProvider } from "../ui/Toast";
import { TrayList, TrayPanel } from "./NeedsYouTray";
import { useTrayItems } from "./useTrayItems";

// Rendered on /chat: on Now ("/") and the Inbox the tray hides, since those views show the queue.
function renderTray(ui: React.ReactElement, path = "/chat") {
  const root = createRootRoute({ component: () => ui });
  const inbox = createRoute({ getParentRoute: () => root, path: "/inbox", component: () => null });
  const chat = createRoute({ getParentRoute: () => root, path: "/chat", component: () => null });
  const router = createRouter({ routeTree: root.addChildren([inbox, chat]), history: createMemoryHistory({ initialEntries: [path] }) });
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

function stubTray(approvals: Approval[] = fixtureApprovals) {
  const snoozed = new Set<string>();
  const snapshot = () => {
    const data = fixtureSnapshot(approvals, fixtureQuestions);
    data.items = data.items.map((item) => (snoozed.has(item.id) ? { ...item, snoozed_until: "2099-01-01T00:00:00Z" } : item));
    data.counts.total -= snoozed.size;
    return data;
  };
  return stubApi((call) => {
    if (call.path === "/attention") return { body: { data: snapshot() } };
    if (call.path === "/attention/snooze") {
      snoozed.add((call.body as { id: string }).id);
      return { body: { data: snapshot() } };
    }
    if (call.path.startsWith("/approvals?status=pending")) return { body: { data: approvals.filter((a) => a.status === "pending") } };
    if (call.path.startsWith("/questions?status=open")) return { body: { data: fixtureQuestions.filter((q) => q.status === "open") } };
    if (call.path === "/fleet") return { body: { data: { workspaces: [], agents: [], revision: 1 } } };
    if (call.path === "/jobs") return { body: { data: [] } };
    const decide = /^\/approvals\/(ap_\d+)\/decide$/.exec(call.path);
    if (decide) return { body: { data: { ...approvals.find((a) => a.id === decide[1])!, status: "allowed" } } };
    const answer = /^\/questions\/(q_\d+)\/answer$/.exec(call.path);
    if (answer) return { body: { data: { ...fixtureQuestions.find((q) => q.id === answer[1])!, status: "answered" } } };
    return undefined;
  });
}

function List() {
  const items = useTrayItems();
  return <TrayList items={items} fresh={new Set()} />;
}

describe("NeedsYouTray", () => {
  beforeEach(() => localStorage.clear());

  it("lists the most urgent first and answers an approval in place", async () => {
    const user = userEvent.setup();
    const calls = stubTray();
    renderTray(<List />);
    const list = await screen.findByRole("list", { name: "Waiting on you" });
    await waitFor(() => expect(within(list).getAllByRole("button", { expanded: false }).length).toBeGreaterThan(3));
    const rows = within(list).getAllByRole("button", { expanded: false });
    expect(rows[0]).toHaveTextContent("git push --force origin main");
    await user.click(within(list).getByRole("button", { name: /go test/, expanded: false }));
    await user.click(screen.getByRole("button", { name: "Allow once" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/approvals/ap_1/decide" && (call.body as { decision: string }).decision === "allow")).toBe(true));
  });

  it("answers a confirm or choice question with one of its options", async () => {
    const user = userEvent.setup();
    const calls = stubTray([]);
    renderTray(<List />);
    // A routine one: a question with a diff or a long body opens its decision packet instead.
    const choice = fixtureQuestions.find((q) => q.status === "open" && isRoutine(q))!;
    await user.click(await screen.findByRole("button", { name: new RegExp(choice.title.replace(/[?.*+()]/g, "\\$&")), expanded: false }));
    const option = choice.options[0]!;
    await user.click(screen.getByRole("button", { name: new RegExp(option.label.replace(/[?.*+()]/g, "\\$&")) }));
    await waitFor(() => expect(calls.some((call) => call.path === `/questions/${choice.id}/answer` && (call.body as { answer_value: string }).answer_value === option.key)).toBe(true));
  });

  it("snoozes an item for ten minutes", async () => {
    const user = userEvent.setup();
    const calls = stubTray();
    renderTray(<List />);
    const snoozeButton = await screen.findByRole("button", { name: /Snooze for 10 minutes: Bash: go test/ });
    await user.click(snoozeButton);
    await waitFor(() => expect(screen.queryByRole("button", { name: /Snooze for 10 minutes: Bash: go test/ })).toBeNull());
    // The snooze is the server's (every device sees it), not this browser's.
    expect(calls.some((call) => call.path === "/attention/snooze" && (call.body as { id: string }).id === "approval:ap_1")).toBe(true);
    expect(localStorage.getItem("crowsnest-tray-snoozed")).toBeNull();
  });

  it("shows a count, folds, and takes focus on the tray shortcut", async () => {
    const user = userEvent.setup();
    stubTray();
    renderTray(<TrayPanel />);
    const panel = await screen.findByRole("complementary", { name: "Needs you" });
    // Docked, it lists every item (no "Show all" stack to unfold).
    await waitFor(() => expect(within(panel).getAllByRole("listitem").length).toBeGreaterThan(0));
    expect(within(panel).queryByText("Show all", { exact: false })).toBeNull();
    await user.click(within(panel).getByRole("button", { name: "Fold the tray" }));
    const pill = await screen.findByRole("button", { name: /Needs you: show the tray/ });
    window.dispatchEvent(new Event(FOCUS_TRAY_EVENT));
    await waitFor(() => expect(document.activeElement?.hasAttribute("data-tray-row")).toBe(true));
    expect(pill).not.toBeInTheDocument();
  });

  it("hides on Now and the Inbox, which show the queue themselves", async () => {
    stubTray();
    renderTray(<TrayPanel />, "/inbox");
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(screen.queryByRole("complementary", { name: "Needs you" })).toBeNull();
  });

  it("renders nothing when nothing needs you", async () => {
    stubApi((call) => {
      if (call.path === "/attention") return { body: { data: fixtureSnapshot([], []) } };
      if (call.path.startsWith("/approvals") || call.path.startsWith("/questions") || call.path === "/jobs") return { body: { data: [] } };
      if (call.path === "/fleet") return { body: { data: { workspaces: [], agents: [], revision: 1 } } };
      return undefined;
    });
    renderTray(<TrayPanel />);
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(screen.queryByRole("complementary", { name: "Needs you" })).toBeNull();
  });
});
