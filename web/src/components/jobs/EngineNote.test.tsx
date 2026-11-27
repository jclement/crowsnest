// EngineNote where an engine is chosen (R8 cut 8): the sentence comes from GET /engines, nothing
// for an engine that runs without nothing.
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import { EngineNote } from "./EngineNote";

const table = [
  { id: "claude", name: "Claude", installed: true, summary: "", capabilities: [], rescue: [], models: [], without: [] },
  { id: "codex", name: "Codex", installed: true, summary: "", capabilities: [], rescue: [], models: [], without: ["Spend in dollars", "The spinning detector"] },
];

describe("EngineNote", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("says what Codex runs without, from the server's table", async () => {
    stubApi(({ path }) => (path === "/engines" ? { body: { data: table } } : undefined));
    renderWithClient(<EngineNote engine="codex" />);
    expect(await screen.findByTestId("engine-note")).toHaveTextContent("Codex runs without spend in dollars and the spinning detector.");
  });

  it("says nothing for Claude", async () => {
    stubApi(({ path }) => (path === "/engines" ? { body: { data: table } } : undefined));
    const { container } = renderWithClient(<EngineNote engine="claude" />);
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(container.querySelector("[data-testid=engine-note]")).toBeNull();
  });
});
