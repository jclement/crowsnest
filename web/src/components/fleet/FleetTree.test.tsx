import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { makeFleet } from "../../lib/fixtures";
import { FleetTree } from "./FleetTree";

function workspaceHeaders() {
  const list = screen.getByRole("list", { name: "Workspaces" });
  return within(list)
    .getAllByRole("button", { expanded: true })
    .concat(within(list).queryAllByRole("button", { expanded: false }))
    .map((button) => button.textContent ?? "");
}

function agentButtons() {
  return screen.getAllByRole("button").filter((button) => button.hasAttribute("data-needs-you") || button.querySelector("[data-status]"));
}

describe("FleetTree", () => {
  it("groups agents under their workspace and tab", () => {
    render(<FleetTree fleet={makeFleet()} onSelect={() => {}} />);
    const panes = screen.getByRole("list", { name: "Panes in review" });
    expect(within(panes).getByText("m3-review")).toBeInTheDocument();
    expect(within(panes).getByText("codex")).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Tabs in crowsnest · wip/m3-web" })).toBeInTheDocument();
  });

  it("lists the workspace with needs-you agents first, flagged", () => {
    render(<FleetTree fleet={makeFleet()} onSelect={() => {}} />);
    const headers = workspaceHeaders();
    expect(headers[0]).toContain("pumpcurves · feature/head-loss");
    expect(headers[0]).toContain("2 need you");
    const first = agentButtons()[0]!;
    expect(first).toHaveTextContent("head-loss");
    expect(first).toHaveAttribute("data-needs-you", "true");
    expect(screen.getByText("2 need you", { selector: "p span" })).toBeInTheDocument();
  });

  it("shows each agent's name, kind, terminal title and cwd tail", () => {
    render(<FleetTree fleet={makeFleet()} onSelect={() => {}} />);
    const row = screen.getByRole("button", { name: /m3-builder/ });
    expect(row).toHaveTextContent("claude");
    expect(row).toHaveTextContent("Fleet view: screenshots at 375 and 1440 px");
    expect(row).toHaveTextContent("…/crowsnest/wip-m3-web");
    expect(within(row).getByRole("img", { name: "Done" })).toBeInTheDocument();
  });

  it("filters by text and says when nothing matches", async () => {
    const user = userEvent.setup();
    render(<FleetTree fleet={makeFleet()} onSelect={() => {}} />);
    const filter = screen.getByRole("searchbox", { name: "Filter the fleet" });
    await user.type(filter, "codex");
    expect(screen.getByRole("button", { name: /m3-review/ })).toBeInTheDocument();
    expect(screen.queryByText("Reviewing M2 fleet cache")).not.toBeInTheDocument();
    await user.clear(filter);
    await user.type(filter, "zzz");
    expect(screen.getByText("Nothing matches")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear filter" }));
    expect(screen.getByText("Reviewing M2 fleet cache")).toBeInTheDocument();
  });

  it("collapses and expands a workspace", async () => {
    const user = userEvent.setup();
    render(<FleetTree fleet={makeFleet()} onSelect={() => {}} />);
    const header = screen.getByRole("button", { name: /^1\s*orchestrator/ });
    await user.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Reviewing M2 fleet cache")).not.toBeInTheDocument();
    await user.click(header);
    expect(screen.getByText("Reviewing M2 fleet cache")).toBeInTheDocument();
  });

  it("calls onSelect with the pane id and marks the selected row", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const { rerender } = render(<FleetTree fleet={makeFleet()} onSelect={onSelect} />);
    await user.click(screen.getByRole("button", { name: /head-loss-tests/ }));
    expect(onSelect).toHaveBeenCalledWith("w3:p2");
    rerender(<FleetTree fleet={makeFleet()} selected="w3:p2" onSelect={onSelect} />);
    expect(screen.getByRole("button", { name: /head-loss-tests/ })).toHaveAttribute("aria-current", "true");
  });
});
