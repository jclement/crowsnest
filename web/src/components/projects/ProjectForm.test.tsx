// New project's folder: a typed path's facts prefill the name and check command and show the
// remote's note; a folder chosen in the picker does the same; the check command, when changed,
// rides along to be saved.
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import { ProjectForm } from "./ProjectForm";

const azureFacts = {
  path: "/h/billing-api", resolved: "/h/billing-api", label: "~/billing-api", name: "billing-api", git: true, worktree: false,
  branch: "develop", default_branch: "develop", host: "azure", check_command: "mise run check", agents_md: false, claude_md: true,
  is_home: false, is_filesystem_root: false, project: null, contains: [], agent_start: [],
};

function stub(facts: object = azureFacts) {
  return stubApi((call) => {
    if (call.path === "/dirs" || call.path === "/roles") return { body: { data: [] } };
    if (call.path.startsWith("/browse/facts")) return { body: { data: facts } };
    if (call.path === "/browse/places") return { body: { data: { home: "/h", favourites: [], roots: [] } } };
    if (call.path.startsWith("/browse?")) {
      return { body: { data: { path: "/h", resolved: "/h", label: "~", root: { path: "/h", label: "Home", kind: "home" }, parent: "", crumbs: [{ name: "~", path: "/h", browsable: true }],
        entries: [{ name: "billing-api", path: "/h/billing-api", dir: true, git: true, symlink: false, hidden: false, mtime: "", readable: true }], more: 0, hidden_count: 0, file_count: 0, partial: false, git: false } } };
    }
    return undefined;
  });
}

describe("ProjectForm: the folder", () => {
  it("prefills the name and check command from a typed path's facts, with the remote's note", async () => {
    stub();
    const onSubmit = vi.fn();
    renderWithClient(<ProjectForm submitLabel="Create project" pending={false} onSubmit={onSubmit} onCancel={() => {}} />);
    await userEvent.type(screen.getByLabelText(/Directory/), "/h/billing-api");
    expect(await screen.findByText("Azure DevOps: Land will not open pull requests here; it merges locally.")).toBeInTheDocument();
    expect(screen.getByText(/git repo on Azure DevOps, branch develop/)).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toHaveValue("Billing api");
    expect(screen.getByLabelText(/Check command/)).toHaveValue("mise run check");
    await userEvent.clear(screen.getByLabelText(/Check command/));
    await userEvent.type(screen.getByLabelText(/Check command/), "make test");
    await userEvent.click(screen.getByRole("button", { name: "Create project" }));
    // The .codex/ the facts showed goes back with the trust box ("" here: none), R10 S5a.
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ dir: "/h/billing-api", name: "Billing api" }), { checkCommand: "make test", detectedCheck: "mise run check", trustFolder: true, codexFingerprint: "" });
  });

  it("asks to trust a folder that carries nothing for agents by default, and not once unticked", async () => {
    stub();
    const onSubmit = vi.fn();
    renderWithClient(<ProjectForm submitLabel="Create project" pending={false} onSubmit={onSubmit} onCancel={() => {}} />);
    await userEvent.type(screen.getByLabelText(/Directory/), "/h/billing-api");
    expect(await screen.findByText(/Nothing in this folder runs when an agent starts/)).toBeInTheDocument();
    const box = screen.getByRole("checkbox", { name: /Agents may work in this folder without asking/ });
    expect(box).toBeChecked();
    await userEvent.click(box);
    await userEvent.click(screen.getByRole("button", { name: "Create project" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ trustFolder: false }));
  });

  // R9-S4: a folder that carries hooks, MCP servers, a Codex config or instructions is not trusted
  // by default; the box lists them, with "these run when an agent starts", and ticking it is the
  // human's choice.
  it("leaves the trust box unticked for a folder that runs something at agent start, and lists it", async () => {
    stub({
      ...azureFacts,
      agent_start: [
        { path: ".claude/settings.json", kind: "hooks", runs: true },
        { path: ".mcp.json", kind: "mcp", runs: true },
        { path: ".codex/hooks.json", kind: "codex", runs: true },
        { path: "CLAUDE.md", kind: "instructions", runs: false },
      ],
    });
    const onSubmit = vi.fn();
    renderWithClient(<ProjectForm submitLabel="Create project" pending={false} onSubmit={onSubmit} onCancel={() => {}} />);
    const box = screen.getByRole("checkbox", { name: /Agents may work in this folder without asking/ });
    expect(box).not.toBeChecked();
    await userEvent.type(screen.getByLabelText(/Directory/), "/h/billing-api");
    expect(await screen.findByText(/These run when an agent starts/)).toBeInTheDocument();
    for (const line of [".claude/settings.json (hooks)", ".mcp.json (MCP servers)", ".codex/hooks.json (Codex config)", "CLAUDE.md (instructions)"]) {
      expect(screen.getByText(line)).toBeInTheDocument();
    }
    expect(box).not.toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Create project" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ trustFolder: false }));
    await userEvent.click(box);
    await userEvent.click(screen.getByRole("button", { name: "Create project" }));
    expect(onSubmit).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ trustFolder: true }));
  });

  it("takes the folder chosen in the picker", async () => {
    stub();
    renderWithClient(<ProjectForm submitLabel="Create project" pending={false} onSubmit={() => {}} onCancel={() => {}} />);
    await userEvent.click(screen.getByRole("button", { name: "Browse…" }));
    await userEvent.click(await screen.findByRole("button", { name: /^billing-api/ }));
    await userEvent.click(screen.getByRole("button", { name: "Select this folder" }));
    await waitFor(() => expect(screen.getByLabelText(/Directory/)).toHaveValue("/h/billing-api"));
    expect(await screen.findByLabelText("Name")).toHaveValue("Billing api");
  });
});
