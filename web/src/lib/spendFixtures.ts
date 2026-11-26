// Spend fixtures (7.11): one summary, report and period in exactly the server's shapes, for the
// unit tests and ?fixtures=1 (development builds only, like the other fixtures).
import type { SpendPeriod, SpendReport, SpendRow, SpendSummary, SpendTurn } from "./spend";

export function makeSpend(overrides: Partial<SpendSummary> = {}): SpendSummary {
  return {
    measured: true,
    input_tokens: 412,
    output_tokens: 18_400,
    cache_write_tokens: 96_000,
    cache_read_tokens: 1_840_000,
    total_tokens: 1_954_812,
    turns: 38,
    tool_calls: 27,
    wall_seconds: 2_340,
    cost_usd: 1.2,
    cost_partial: false,
    unpriced_tokens: 0,
    cache_hit_share: 0.95,
    models: ["claude-opus-5-5"],
    first_at: "2026-11-26T09:00:00Z",
    last_at: "2026-11-26T09:39:00Z",
    ...overrides,
  };
}

export function makeRow(key: string, label: string, overrides: Partial<SpendSummary> = {}): SpendRow {
  return { key, label, ...makeSpend({ wall_seconds: 0, first_at: null, last_at: null, ...overrides }) };
}

export function makeTurns(count = 12): SpendTurn[] {
  return Array.from({ length: count }, (_, i) => ({
    at: new Date(Date.UTC(2026, 10, 26, 9, i * 3)).toISOString(),
    model: "claude-opus-5-5",
    tokens: 20_000 + i * 4_000,
    output: 300 + i * 10,
    cost_usd: 0.02 + i * 0.002,
  }));
}

export function makeReport(overrides: Partial<SpendReport> = {}): SpendReport {
  const rows = [
    makeRow("j_1", "Add retry back-off", { cost_usd: 3.28 }),
    makeRow("orchestrator", "Orchestrator", { cost_usd: 0.41, models: ["claude-sonnet-5-5"], total_tokens: 900_000 }),
    makeRow("j_2", "Ask a mystery model", { cost_usd: null, unpriced_tokens: 5_000, total_tokens: 5_000, models: ["gpt-9"] }),
  ];
  return {
    from: "2026-11-23",
    to: "2026-11-26",
    group: "job",
    rows,
    total: makeRow("total", "Total", { cost_usd: 3.69, cost_partial: true, unpriced_tokens: 5_000 }),
    prices_checked: "2026-11-26",
    prices_source: "https://platform.claude.com/docs/en/about-claude/pricing",
    note: "Estimated API-equivalent cost.",
    ...overrides,
  };
}

export function makePeriod(overrides: Partial<SpendPeriod> = {}): SpendPeriod {
  return {
    today: makeRow("total", "Today", { cost_usd: 1.5 }),
    week: makeRow("total", "This week", { cost_usd: 12.44 }),
    orchestrator_today: makeRow("orchestrator", "Orchestrator", { cost_usd: 0.12 }),
    orchestrator_total: makeSpend({ cost_usd: 2.31 }),
    today_date: "2026-11-26",
    week_start: "2026-11-23",
    prices_checked: "2026-11-26",
    note: "Estimated API-equivalent cost.",
    ...overrides,
  };
}

/** Answers the spend routes in fixtures mode; undefined for any other path. */
export function spendFixtureApi(path: string): unknown {
  if (path === "/spend/summary") return makePeriod();
  const match = /^\/spend\?(.*)$/.exec(path);
  if (match) {
    const group = (new URLSearchParams(match[1]).get("group") ?? "job") as SpendReport["group"];
    return makeReport({ group });
  }
  return undefined;
}
