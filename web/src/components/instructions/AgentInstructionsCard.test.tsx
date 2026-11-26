import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentInstructions } from "../../lib/instructions";
import { renderWithClient, stubApi } from "../../test/render";
import { AgentInstructionsCard } from "./AgentInstructionsCard";

afterEach(() => vi.unstubAllGlobals());

const manifest = (changed: boolean): AgentInstructions => ({
  subject: "j_12",
  started_at: new Date(Date.now() - 600_000).toISOString(),
  hash: "abcdef0123456789",
  layers: [
    { layer: "global", scope: "global", key: "", revision: 3, source: "db", size: 120, hash: "a" },
    { layer: "role builder", scope: "role", key: "builder", revision: 0, source: "builtin", size: 900, hash: "b" },
    { layer: "job j_12", scope: "job", key: "j_12", revision: 0, source: "brief", size: 300, hash: "c" },
  ],
  changed,
  changes: changed ? [{ layer: "global", scope: "global", key: "", was_source: "db", now_source: "db", was_revision: 3, now_revision: 4 }] : [],
  loaded: [{ path: "/src/app/CLAUDE.md", memory_type: "Project", load_reason: "session_start", size: 2048, at: new Date().toISOString() }],
  skills_note: "Skills and slash commands are not listed.",
});

function serve(data: AgentInstructions) {
  return stubApi((call) => {
    if (call.path === "/agents-instructions/j_12") return { body: { data } };
    if (call.path === "/instructions/global/-") return { body: { data: { layer: { scope: "global", key: "", name: "Global", source: "db", effective_md: "Keep replies short.", saved_md: "", builtin_md: "", file_path: "", editable: true, revision: 4, updated_at: null, size: 19 }, revisions: [] } } };
    if (call.path === "/jobs/j_12/restart-agent" && call.method === "POST") return { body: { data: { id: "j_12" } } };
    return undefined;
  });
}

describe("AgentInstructionsCard", () => {
  it("lists the layers at start, the files Claude loaded by path, and the skills note", async () => {
    serve(manifest(false));
    renderWithClient(<AgentInstructionsCard subject="j_12" restart={{ kind: "job", id: "j_12" }} who="j_12's agent" />);
    const layers = await screen.findByRole("list", { name: "Layers at start" });
    expect(layers).toHaveTextContent("global");
    expect(layers).toHaveTextContent("built-in");
    expect(screen.getByText("/src/app/CLAUDE.md")).toBeInTheDocument();
    expect(screen.getByText("session start")).toBeInTheDocument();
    expect(screen.getByText(/Skills and slash commands/)).toBeInTheDocument();
    expect(screen.queryByText(/changed since start/)).not.toBeInTheDocument();
    // A layer opens with its current text.
    await userEvent.click(screen.getByRole("button", { name: "global" }));
    expect(await screen.findByText("Keep replies short.")).toBeInTheDocument();
  });

  it("says when the instructions changed and restarts the agent with the new ones", async () => {
    const calls = serve(manifest(true));
    renderWithClient(<AgentInstructionsCard subject="j_12" restart={{ kind: "job", id: "j_12" }} who="j_12's agent" />);
    expect(await screen.findByText(/Instructions changed since start/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Restart with new instructions" }));
    await userEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(await screen.findByText("Restarted with the new instructions")).toBeInTheDocument();
    expect(calls.some((call) => call.method === "POST" && call.path === "/jobs/j_12/restart-agent")).toBe(true);
  });
});
