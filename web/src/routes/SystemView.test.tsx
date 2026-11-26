import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { herdrMissing, report } from "../components/system/fixtures.test-data";
import { renderWithClient, stubApi } from "../test/render";
import { SystemView } from "./SystemView";

const sample = { at: "2026-11-26T12:00:00Z", cpu: 42, cores: 8, mem_used: 4 << 30, mem_total: 16 << 30, load1: 3.2, load5: 2, load15: 1, disk_free: 100 << 30, disk_total: 500 << 30, scope: "host", working: 2 };

function routes(body: unknown) {
  return stubApi((call) => {
    if (call.path.startsWith("/system?") || call.path === "/system") return { body: { data: body } };
    if (call.path === "/system/stats") return { body: { data: { latest: sample, history: [sample, { ...sample, cpu: 60 }] } } };
    if (call.path === "/system/agents") return { body: { data: [{ pane_id: "p1", name: "orchestrator", kind: "claude", status: "idle", workspace: "orchestrator", cpu: 12.5, rss: 300 << 20, processes: 3 }] } };
    return undefined;
  });
}

describe("SystemView", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the verdict, versions, agents, the running instance, errors and the machine", async () => {
    routes(report());
    renderWithClient(<SystemView />);
    expect(await screen.findByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("2.1.332")).toBeInTheDocument();
    expect(screen.getByText("3m 2s")).toBeInTheDocument();
    expect(screen.getByText("herdr is not answering")).toBeInTheDocument();
    expect(await screen.findByText("42%")).toBeInTheDocument();
    expect(await screen.findByText("300M")).toBeInTheDocument();
  });

  it("a failing check shows its fix and command; Re-check asks the server to run them again", async () => {
    const calls = routes(herdrMissing());
    const user = userEvent.setup();
    renderWithClient(<SystemView />);
    expect(await screen.findByText("Something needs fixing")).toBeInTheDocument();
    expect(screen.getByText("brew install herdr")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Re-check" }));
    await vi.waitFor(() => expect(calls.some((call) => call.path === "/system?recheck=1")).toBe(true));
  });
});
