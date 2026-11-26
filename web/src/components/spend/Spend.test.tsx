import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { makePeriod, makeReport, makeSpend, makeTurns } from "../../lib/spendFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { SpendChip } from "./SpendChip";
import { SpendSection } from "./SpendSection";
import { SpendButton, SpendPanel, SpendTable } from "./SpendSheet";

afterEach(() => vi.unstubAllGlobals());

describe("SpendChip", () => {
  it("shows the estimated cost, tokens for an unknown model, and nothing before any call", () => {
    const { rerender } = render(<SpendChip spend={makeSpend()} />);
    expect(screen.getByText("$1.20")).toBeInTheDocument();
    expect(screen.getByTitle(/est\..*not billed per token/)).toHaveTextContent("Spend: $1.20 (estimate)");
    rerender(<SpendChip spend={makeSpend({ cost_usd: null, total_tokens: 12_000 })} />);
    expect(screen.getByText("12k tok")).toBeInTheDocument();
    rerender(<SpendChip spend={makeSpend({ turns: 0, total_tokens: 0 })} />);
    expect(screen.queryByText(/\$/)).not.toBeInTheDocument();
    rerender(<SpendChip spend={makeSpend({ measured: false, reason: "codex" })} />);
    expect(screen.getByText("not measured")).toBeInTheDocument();
  });
});

describe("SpendSection", () => {
  it("breaks the spend down with the cache share, the per-call chart and the estimate note", async () => {
    render(<SpendSection spend={makeSpend()} turns={makeTurns(12)} />);
    expect(screen.getByText("$1.20 est.")).toBeInTheDocument();
    expect(screen.getByText("Cache read").nextSibling).toHaveTextContent("1.84M");
    expect(screen.getByText("Model calls").nextSibling).toHaveTextContent("38");
    expect(screen.getByText("Wall time").nextSibling).toHaveTextContent("39m");
    expect(screen.getByRole("meter", { name: /from the cache/ })).toHaveAttribute("aria-valuenow", "95");
    const bars = within(screen.getByRole("list", { name: /Tokens per model call/ })).getAllByRole("listitem");
    expect(bars).toHaveLength(12);
    await userEvent.hover(bars[11]!);
    expect(screen.getByText(/64k tokens · \$0\.04 est\. · claude-opus-5-5/)).toBeInTheDocument();
    expect(screen.getByText(/not billed per token/)).toBeInTheDocument();
  });

  it("says why a job is not measured instead of showing zeros", () => {
    render(<SpendSection spend={makeSpend({ measured: false, reason: "codex" })} />);
    expect(screen.getByText(/Not measured: crowsnest does not read Codex/)).toBeInTheDocument();
    expect(screen.queryByText("Input")).not.toBeInTheDocument();
  });

  it("shows tokens and 'cost unknown' for a model with no price", () => {
    render(<SpendSection spend={makeSpend({ cost_usd: null, unpriced_tokens: 1_954_812, models: ["gpt-9"] })} turns={[]} />);
    expect(screen.getByText("cost unknown")).toBeInTheDocument();
    expect(screen.getByText(/1\.95M tokens are from a model with no known price/)).toBeInTheDocument();
  });
});

describe("SpendTable", () => {
  it("sorts by a column header and marks it", async () => {
    render(<SpendTable report={makeReport()} />);
    const firstCells = () => screen.getAllByRole("row").slice(1, -1).map((row) => within(row).getAllByRole("cell")[0]!.textContent);
    expect(firstCells()).toEqual(["Add retry back-off", "Orchestrator", "Ask a mystery model"]);
    expect(screen.getByRole("columnheader", { name: /Est. cost/ })).toHaveAttribute("aria-sort", "descending");
    await userEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(firstCells()).toEqual(["Add retry back-off", "Ask a mystery model", "Orchestrator"]);
    expect(screen.getByRole("columnheader", { name: /Name/ })).toHaveAttribute("aria-sort", "ascending");
    expect(screen.getByText("unknown")).toBeInTheDocument();
    expect(within(screen.getAllByRole("row").at(-1)!).getByText("≥$3.69")).toBeInTheDocument();
  });
});

describe("the Spend sheet", () => {
  it("shows today and the week on the button, and asks for the range and grouping chosen", async () => {
    const calls = stubApi(({ path }) => {
      if (path === "/spend/summary") return { body: { data: makePeriod() } };
      if (path.startsWith("/spend?")) {
        const group = new URLSearchParams(path.split("?")[1]).get("group") ?? "job";
        return { body: { data: makeReport({ group: group as "job" }) } };
      }
      return undefined;
    });
    renderWithClient(
      <>
        <SpendButton onOpen={() => {}} />
        <SpendPanel />
      </>,
    );
    expect(await screen.findByRole("button", { name: /^Spend: ?\$1\.50 today · \$12\.44 wk ?\(estimates\)$/ })).toBeInTheDocument();
    expect(await screen.findByText("≥ $3.69 est.")).toBeInTheDocument();
    expect(calls.some((call) => call.path === "/spend?group=job&from=2026-11-23&to=2026-11-26")).toBe(true);
    await userEvent.click(screen.getByRole("tab", { name: "By model" }));
    await userEvent.click(screen.getByRole("tab", { name: "Today" }));
    await vi.waitFor(() => expect(calls.some((call) => call.path === "/spend?group=model&from=2026-11-26&to=2026-11-26")).toBe(true));
    expect(screen.getByText(/Prices checked 2026-11-26/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "CSV" })).toBeEnabled();
  });
});
