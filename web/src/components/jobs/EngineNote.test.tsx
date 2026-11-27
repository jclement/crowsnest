// The Codex note where an engine is chosen (R8): only for Codex, saying what it does not get.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { CODEX_GAPS, codexNote } from "../../lib/engines";
import { EngineNote } from "./EngineNote";

describe("EngineNote", () => {
  it("says nothing for Claude or the default", () => {
    const { container, rerender } = render(<EngineNote engine="claude" />);
    expect(container).toBeEmptyDOMElement();
    rerender(<EngineNote engine="" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("lists every gap for Codex, and what still bounds it", () => {
    render(<EngineNote engine="codex" />);
    const note = screen.getByRole("note", { name: "What Codex does not get" });
    for (const gap of CODEX_GAPS) expect(note).toHaveTextContent(gap);
    expect(note).toHaveTextContent("Budgets bound it by minutes and turns.");
    expect(codexNote()).toMatch(/^Codex runs without crowsnest's hooks, so it does not get .+ or answer receipts past “delivered”\. /);
  });
});
