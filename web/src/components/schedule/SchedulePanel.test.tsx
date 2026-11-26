import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fixtureSchedule } from "../../lib/scheduleFixtures";
import type { ScheduleView } from "../../lib/schedule";
import { renderWithClient, stubApi } from "../../test/render";
import { SchedulePanel } from "./SchedulePanel";

afterEach(() => vi.unstubAllGlobals());

function serveSchedule(view: ScheduleView = structuredClone(fixtureSchedule)) {
  return stubApi((call) => {
    if (call.path === "/schedule" && call.method === "GET") return { body: { data: view } };
    const run = /^\/schedule\/([^/]+)\/run$/.exec(call.path);
    if (run && call.method === "POST") {
      const task = view.tasks.find((t) => t.name === run[1]);
      if (!task) return { status: 404, body: { error: { code: "NOT_FOUND", message: "No task file has that name" } } };
      task.run_now = true;
      return { body: { data: view } };
    }
    const set = /^\/schedule\/([^/]+)$/.exec(call.path);
    if (set && call.method === "PUT") {
      const task = view.tasks.find((t) => t.name === set[1]);
      if (task) {
        task.enabled = (call.body as { enabled: boolean }).enabled;
        task.next_due_at = task.enabled ? new Date(Date.now() + 3_600_000).toISOString() : null;
      }
      return { body: { data: view } };
    }
    if (call.path.startsWith("/journal")) return { body: { data: { entries: [], total: 0, next_before: 0 } } };
    return undefined;
  });
}

describe("SchedulePanel", () => {
  it("lists every task with its cadence, last run, next time and outcome, and the skipped files", async () => {
    serveSchedule();
    renderWithClient(<SchedulePanel />);
    const table = await screen.findByRole("table", { name: "Scheduled tasks" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("check");
    expect(rows[0]).toHaveTextContent("every 15m");
    expect(rows[0]).toHaveTextContent("9 min ago");
    expect(rows[0]).toHaveTextContent("in 6 min");
    expect(rows[0]).toHaveTextContent("carried by a poke");
    expect(rows[1]).toHaveTextContent("never");
    expect(rows[1]).toHaveTextContent("off");
    expect(rows[2]).toHaveTextContent("job");
    expect(rows[2]).toHaveTextContent("every 1d after 17:00");
    expect(rows[2]).toHaveTextContent("j_31 is done");
    expect(screen.getByText(/weekly\.md/)).toBeInTheDocument();
    expect(screen.getByText(/is not understood/)).toBeInTheDocument();
  });

  it("switches a task off and on", async () => {
    const calls = serveSchedule();
    renderWithClient(<SchedulePanel />);
    const table = await screen.findByRole("table", { name: "Scheduled tasks" });
    const toggle = within(table).getByRole("switch", { name: "check enabled" });
    expect(toggle).toHaveAttribute("aria-checked", "true");
    await userEvent.click(toggle);
    await waitFor(() => expect(within(table).getByRole("switch", { name: "check enabled" })).toHaveAttribute("aria-checked", "false"));
    expect(calls.find((call) => call.method === "PUT")).toEqual({ method: "PUT", path: "/schedule/check", body: { enabled: false } });
  });

  it("runs a task now and says where it went", async () => {
    const calls = serveSchedule();
    renderWithClient(<SchedulePanel />);
    const table = await screen.findByRole("table", { name: "Scheduled tasks" });
    await userEvent.click(within(table).getByRole("button", { name: "Run check now" }));
    expect(await screen.findByText("check goes with the next poke")).toBeInTheDocument();
    expect(calls.some((call) => call.method === "POST" && call.path === "/schedule/check/run")).toBe(true);
    await waitFor(() => expect(within(table).getByRole("button", { name: "Run check now" })).toBeDisabled());
    expect(within(table).getAllByRole("row")[1]).toHaveTextContent("now (Run now)");
  });

  it("explains where task files go when there are none", async () => {
    serveSchedule({ tasks: [], problems: [], dirs: ["/o/tasks", "/u/tasks"] });
    renderWithClient(<SchedulePanel />);
    expect(await screen.findByText("No scheduled tasks")).toBeInTheDocument();
    expect(screen.getByText("/o/tasks")).toBeInTheDocument();
    expect(screen.getByText("/u/tasks")).toBeInTheDocument();
  });
});
