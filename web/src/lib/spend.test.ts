import { describe, expect, it } from "vitest";
import { bucketTurns, chipText, costText, formatTokens, formatUSD, rangeDates, reportCSV, sortRows } from "./spend";
import { makeReport, makeSpend, makeTurns } from "./spendFixtures";

describe("spend formatting", () => {
  it("formats money with sensible precision", () => {
    expect(formatUSD(0)).toBe("$0.00");
    expect(formatUSD(0.0042)).toBe("$0.004");
    expect(formatUSD(0.27)).toBe("$0.27");
    expect(formatUSD(12.4)).toBe("$12.40");
    expect(formatUSD(1204.4)).toBe("$1,204");
  });

  it("formats tokens short", () => {
    expect(formatTokens(950)).toBe("950");
    expect(formatTokens(1_200)).toBe("1.2k");
    expect(formatTokens(12_345)).toBe("12k");
    expect(formatTokens(4_560_000)).toBe("4.56M");
    expect(formatTokens(2_000_000)).toBe("2M");
  });

  it("words a cost as an estimate, a lower bound, or unknown; never a zero", () => {
    expect(costText(makeSpend({ cost_usd: 1.2 }))).toBe("$1.20 est.");
    expect(costText(makeSpend({ cost_usd: 1.2, cost_partial: true }))).toBe("≥ $1.20 est.");
    expect(costText(makeSpend({ cost_usd: null }))).toBe("cost unknown");
  });

  it("picks what a card chip says", () => {
    expect(chipText(undefined)).toBeNull();
    expect(chipText(makeSpend({ turns: 0, total_tokens: 0 }))).toBeNull();
    expect(chipText(makeSpend())?.text).toBe("$1.20");
    expect(chipText(makeSpend({ cost_usd: null, total_tokens: 5_000 }))?.text).toBe("5k tok");
    expect(chipText(makeSpend({ measured: false, reason: "codex" }))?.text).toBe("not measured");
    expect(chipText(makeSpend({ measured: false, reason: "orchestrator" }))).toBeNull();
    expect(chipText(makeSpend())?.title).toMatch(/not billed per token/);
  });
});

describe("the breakdown table", () => {
  it("sorts by cost with unknown costs last, and by name", () => {
    const rows = makeReport().rows;
    expect(sortRows(rows, { key: "cost", descending: true }).map((row) => row.key)).toEqual(["j_1", "orchestrator", "j_2"]);
    expect(sortRows(rows, { key: "cost", descending: false }).map((row) => row.key)).toEqual(["j_2", "orchestrator", "j_1"]);
    expect(sortRows(rows, { key: "label", descending: false }).map((row) => row.label)).toEqual(["Add retry back-off", "Ask a mystery model", "Orchestrator"]);
  });

  it("exports CSV with a BOM, the total last, unknown costs blank, and formulas defused", () => {
    const report = makeReport();
    report.rows[0]!.label = '=HYPERLINK("x"), "quoted"';
    const csv = reportCSV(report);
    expect(csv.startsWith("﻿key,label,cost_usd_estimate")).toBe(true);
    const lines = csv.trim().split("\r\n");
    expect(lines).toHaveLength(5);
    expect(lines[1]).toContain(`"'=HYPERLINK(""x""), ""quoted"""`);
    expect(lines[3]).toMatch(/^j_2,Ask a mystery model,,no,/);
    expect(lines[4]).toMatch(/^total,Total,3\.690000,yes,/);
  });

  it("turns ranges into the server's days", () => {
    const period = { today_date: "2026-11-26", week_start: "2026-11-23" };
    expect(rangeDates("today", period)).toEqual({ from: "2026-11-26", to: "2026-11-26" });
    expect(rangeDates("week", period)).toEqual({ from: "2026-11-23", to: "2026-11-26" });
    expect(rangeDates("30d", period)).toEqual({ from: "2026-10-28", to: "2026-11-26" });
    expect(rangeDates("week", undefined)).toEqual({});
  });
});

describe("bucketTurns", () => {
  it("keeps one bar per call when they fit and groups consecutive calls when not", () => {
    expect(bucketTurns(makeTurns(12))).toHaveLength(12);
    const grouped = bucketTurns(makeTurns(200), 80);
    expect(grouped.length).toBeLessThanOrEqual(80);
    expect(grouped.reduce((sum, bar) => sum + bar.calls, 0)).toBe(200);
  });

  it("marks a group's cost unknown when any call in it is unknown", () => {
    const turns = makeTurns(4);
    turns[1]!.cost_usd = null;
    expect(bucketTurns(turns, 2).map((bar) => bar.cost === null)).toEqual([true, false]);
  });
});

describe("filterReport (the Spend sheet's project filter)", () => {
  it("keeps the chosen rows and sums the total again", async () => {
    const { filterReport } = await import("./spend");
    const row = (key: string, cost: number | null, tokens: number) =>
      ({
        key, label: key, measured: true, input_tokens: tokens, output_tokens: 1, cache_write_tokens: 0, cache_read_tokens: tokens,
        total_tokens: tokens * 2 + 1, turns: 1, tool_calls: 2, wall_seconds: 3, cost_usd: cost, cost_partial: false, unpriced_tokens: 0,
        cache_hit_share: 0.5, models: ["sonnet"], first_at: null, last_at: null,
      }) as import("./spend").SpendRow;
    const report = { from: "2026-11-20", to: "2026-11-26", group: "job", rows: [row("j_1", 1, 10), row("j_2", 2, 20), row("j_3", null, 5)], total: row("total", 3, 35), prices_checked: "x", prices_source: "x", note: "" } as import("./spend").SpendReport;
    const narrowed = filterReport(report, (r) => r.key !== "j_2");
    expect(narrowed.rows.map((r) => r.key)).toEqual(["j_1", "j_3"]);
    expect(narrowed.total.cost_usd).toBe(1);
    expect(narrowed.total.cost_partial).toBe(true);
    expect(narrowed.total.input_tokens).toBe(15);
    expect(narrowed.total.turns).toBe(2);
    expect(filterReport(report, () => false).total.cost_usd).toBe(0);
  });
});
