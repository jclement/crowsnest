import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { parsePoke, pokeToast } from "../../lib/journal";
import { MessageItem } from "./MessageItem";
import { PokeRow } from "./PokeRow";

const poke = "[crowsnest poke 2026-11-26T03:10:00Z talk=off]\n- builder (pane w2:p1) finished its turn (done), for 47s\n- job j_4 is done: Fix the parser";

describe("PokeRow", () => {
  it("is a compact system row with the reasons collapsed", async () => {
    render(<PokeRow text={poke} />);
    const row = screen.getByRole("note", { name: "Poke from crowsnest" });
    expect(row).toHaveTextContent("Poke·2 reasons");
    expect(screen.queryByText(/finished its turn/)).not.toBeInTheDocument();
    const toggle = screen.getByRole("button", { expanded: false });
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("builder (pane w2:p1) finished its turn (done), for 47s")).toBeInTheDocument();
    expect(screen.getByText("job j_4 is done: Fix the parser")).toBeInTheDocument();
  });

  it("replaces the user bubble for messages tagged poke", () => {
    render(<MessageItem message={{ id: "p", index: 0, seq: 1, at: "2026-11-26T03:10:00Z", kind: "user", tag: "poke", text: poke }} />);
    expect(screen.getByRole("note", { name: "Poke from crowsnest" })).toBeInTheDocument();
    expect(screen.queryByText(/\[crowsnest poke/)).not.toBeInTheDocument();
  });

  it("says when talk mode was on and copes with a reason-less header", () => {
    render(<PokeRow text="[crowsnest poke 2026-11-26T03:10:00Z talk=on]" />);
    expect(screen.getByRole("note")).toHaveTextContent("0 reasons");
    expect(screen.getByText("(talk on)")).toBeInTheDocument();
    expect(screen.getByRole("button")).toBeDisabled();
  });
});

describe("parsePoke and pokeToast", () => {
  it("parses the header and the reasons", () => {
    expect(parsePoke(poke)).toEqual({ at: "2026-11-26T03:10:00Z", talk: false, reasons: ["builder (pane w2:p1) finished its turn (done), for 47s", "job j_4 is done: Fix the parser"] });
    expect(parsePoke("hello")).toBeNull();
  });

  it("words the toast for each outcome", () => {
    expect(pokeToast({ outcome: "sent", reasons: ["a"], manual: true, at: "" }).title).toBe("Poke sent (1 reason)");
    expect(pokeToast({ outcome: "skipped", reason: "nothing is due", reasons: [], manual: true, at: "" })).toMatchObject({ tone: "info", title: "Poke skipped: nothing is due" });
    expect(pokeToast({ outcome: "skipped", reason: "the orchestrator is working", reasons: ["x"], manual: true, at: "" }).tone).toBe("warning");
  });
});
