import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { journalPage } from "../../lib/journalFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { JournalPanel } from "./JournalPanel";

afterEach(() => vi.unstubAllGlobals());

function serveJournal(limit = 50) {
  return stubApi((call) => {
    if (!call.path.startsWith("/journal")) return undefined;
    const query = new URLSearchParams(call.path.split("?")[1] ?? "");
    return { body: { data: journalPage(query.get("kind")?.split(",") ?? [], Number(query.get("before") ?? 0), limit) } };
  });
}

describe("JournalPanel", () => {
  it("lists entries newest first in a table", async () => {
    serveJournal();
    renderWithClient(<JournalPanel />);
    const table = await screen.findByRole("table", { name: /Journal, newest first \(8 entries\)/ });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(8);
    expect(rows[0]).toHaveTextContent("Poke");
    expect(rows[0]).toHaveTextContent("Poked the orchestrator");
    expect(rows[7]).toHaveTextContent("nothing is due");
  });

  it("filters by kind", async () => {
    const calls = serveJournal();
    renderWithClient(<JournalPanel />);
    await screen.findByRole("table");
    await userEvent.click(screen.getByRole("button", { name: "Skipped" }));
    await waitFor(() => expect(calls.at(-1)?.path).toBe("/journal?kind=skip"));
    await waitFor(() => expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(3));
    expect(screen.getByRole("button", { name: "Skipped" })).toHaveAttribute("aria-pressed", "true");
  });

  it("loads older pages", async () => {
    serveJournal(3);
    renderWithClient(<JournalPanel />);
    await screen.findByRole("table");
    expect(screen.getByText("3 of 8")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    await screen.findByText("6 of 8");
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    await screen.findByText("8 of 8");
    expect(screen.queryByRole("button", { name: "Load older" })).not.toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    stubApi(() => ({ body: { data: { entries: [], total: 0, next_before: 0 } } }));
    renderWithClient(<JournalPanel />);
    expect(await screen.findByText("Nothing journalled yet")).toBeInTheDocument();
  });
});
