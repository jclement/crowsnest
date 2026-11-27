import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { makeFleet } from "../../lib/fixtures";
import type { AgentStatus } from "../../lib/types";
import { renderWithClient, stubApi } from "../../test/render";
import { RescuePanel } from "./RescuePanel";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

afterEach(() => vi.unstubAllGlobals());

const job = { id: "j_5", pane_id: "w3:p1", agent_name: "job-5", stuck: true };

function stub(status: AgentStatus) {
  const fleet = makeFleet();
  fleet.agents = [{ ...(fleet.agents[0] as (typeof fleet.agents)[number]), pane_id: "w3:p1", name: "job-5", status }];
  return stubApi((call) => {
    if (call.path === "/fleet") return { body: { data: fleet } };
    if (call.path === "/agents/w3%3Ap1/screen")
      return {
        body: {
          data: { target: "w3:p1", pane_id: "w3:p1", name: "job-5", kind: "claude", status, text: "✻ Thinking…", ansi: "", screen_hash: "h1", revision: 1, dialog: { present: false, options: [], form: false, warnings: [] }, draft: { input_found: true, present: false, text: "" } },
        },
      };
    if (call.method === "POST") return { body: { data: { pane_id: "w3:p1", status: "ok", queued: false, started: true, keys: ["esc"], screen_hash: "" } } };
    return undefined;
  });
}

describe("RescuePanel and a pending approval", () => {
  it("offers the structured approval, not the terminal's dialog for the same call", async () => {
    const fleet = makeFleet();
    fleet.agents = [{ ...(fleet.agents[0] as (typeof fleet.agents)[number]), pane_id: "w3:p1", name: "job-5", status: "blocked" }];
    stubApi((call) => {
      if (call.path === "/fleet") return { body: { data: fleet } };
      if (call.path === "/approvals?status=pending") return { body: { data: [{ id: "ap_1", job_id: "j_5", pane_id: "w3:p1", tool_name: "Bash", status: "pending", risk: "elevated", summary: "Bash: git push" }] } };
      if (call.path === "/agents/w3%3Ap1/screen")
        return {
          body: {
            data: { target: "w3:p1", pane_id: "w3:p1", name: "job-5", kind: "claude", status: "blocked", text: "Allow this command?\n❯ 1. Yes\n  2. No", ansi: "", screen_hash: "h1", revision: 1,
              dialog: { present: true, options: [{ number: 1, label: "Yes" }, { number: 2, label: "No" }], form: false, warnings: [] }, draft: { input_found: false, present: false, text: "" } },
          },
        };
      return undefined;
    });
    renderWithClient(<RescuePanel job={job} defaultOpen />);
    expect(await screen.findByTestId("rescue-approval-note")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^1\. Yes|Yes$/ })).toBeNull();
  });
});

describe("RescuePanel", () => {
  it("interrupts with Esc, continues, and asks before a restart", async () => {
    const calls = stub("working");
    const user = userEvent.setup();
    renderWithClient(<RescuePanel job={job} defaultOpen />);
    await user.click(await screen.findByRole("button", { name: /Interrupt \(Esc\)/ }));
    await waitFor(() => expect(calls.find((call) => call.path === "/agents/w3%3Ap1/keys")?.body).toEqual({ keys: ["esc"], screen_hash: "" }));
    await user.click(screen.getByRole("button", { name: /^Continue/ }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_5/message")).toBe(true));
    await user.click(screen.getByRole("button", { name: /Restart \(same conversation\)/ }));
    expect(calls.some((call) => call.path === "/jobs/j_5/restart-agent")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Yes, restart" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_5/restart-agent")).toBe(true));
  });

  it("compact and switch model only while idle, typed as slash commands", async () => {
    const working = stub("working");
    const user = userEvent.setup();
    const { unmount } = renderWithClient(<RescuePanel job={job} defaultOpen />);
    await waitFor(() => expect(working.some((call) => call.path === "/fleet")).toBe(true));
    expect(await screen.findByRole("button", { name: /Compact now/ })).toBeDisabled();
    unmount();
    vi.unstubAllGlobals();

    const idle = stub("idle");
    renderWithClient(<RescuePanel job={job} defaultOpen />);
    await waitFor(() => expect(screen.getByRole("button", { name: /Compact now/ })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: /Compact now/ }));
    await waitFor(() => expect(idle.find((call) => call.path === "/agents/w3%3Ap1/prompt")?.body).toEqual({ text: "/compact" }));
    await user.selectOptions(screen.getByLabelText("Model"), "haiku");
    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(idle.filter((call) => call.path === "/agents/w3%3Ap1/prompt").map((call) => call.body)).toContainEqual({ text: "/model haiku" }));
  });

  it("over its budget, pushing the agent is not offered; the advice says what will help", async () => {
    stub("idle");
    renderWithClient(<RescuePanel job={{ ...job, cause: "budget" }} defaultOpen />);
    expect(await screen.findByTestId("rescue-advice")).toHaveTextContent(/budget/);
    expect(screen.getByRole("button", { name: /^Nudge/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^Continue/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /Interrupt/ })).toBeEnabled();
  });

  it("starts folded unless asked", () => {
    stub("idle");
    renderWithClient(<RescuePanel job={job} />);
    expect(screen.getByRole("button", { name: /Rescue/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("list", { name: "Rescue actions" })).toBeNull();
  });
});
