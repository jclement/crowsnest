import { describe, expect, it } from "vitest";
import { acceptChip, budgetChip, budgetRows, durationText, receiptText, resetCountdown, type BudgetView } from "./accept";

function budget(overrides: Partial<BudgetView> = {}): BudgetView {
  return {
    limits: { usd: 2, tokens: 0, minutes: 60, turns: 0, source: { usd: "default" }, fleet: false },
    used: { usd: 1.7, tokens: 1000, minutes: 12, turns: 3, measured: true, children: 1 },
    share: 0.85,
    over: "usd",
    state: "warned",
    detail: "Used 85%",
    ...overrides,
  };
}

describe("acceptance", () => {
  it("labels each accept state distinctly", () => {
    expect(acceptChip({ state: "done", accept_state: "accepted" })?.label).toBe("Accepted");
    expect(acceptChip({ state: "done", accept_state: "unverified" })?.label).toBe("Unverified");
    expect(acceptChip({ state: "running", accept_state: "failed", check_rounds: 2 })?.label).toBe("Check failed (round 2)");
    expect(acceptChip({ state: "done", accept_state: "stale" })?.tone).toBe("warning");
    expect(acceptChip({ state: "done", accept_state: "needs_rebase" })?.label).toBe("Needs rebase");
    expect(acceptChip({ state: "done", accept_state: "" })).toBeNull();
  });
  it("words a duration", () => {
    expect(durationText(4200)).toBe("4 s");
    expect(durationText(64_000)).toBe("1 min 4 s");
    expect(durationText(120_000)).toBe("2 min");
  });
});

describe("budgets", () => {
  it("chips the warning and the limit", () => {
    expect(budgetChip(budget())?.label).toBe("Budget 85%");
    expect(budgetChip(budget({ state: "exceeded", share: 1.1 }))?.tone).toBe("danger");
    expect(budgetChip(budget({ state: "", share: 0.2 }))).toBeNull();
    expect(budgetChip(undefined)).toBeNull();
  });
  it("says not measured, never zero", () => {
    const rows = budgetRows(budget({ used: { usd: null, tokens: null, minutes: 30, turns: 2, measured: false, children: 0 } }));
    expect(rows[0]).toMatchObject({ label: "Dollars", used: "not measured", share: null });
    expect(rows[1]).toMatchObject({ label: "Minutes", used: "30", limit: "60", share: 0.5 });
  });
});

describe("the usage limit", () => {
  it("counts down to the reset", () => {
    const now = Date.parse("2026-11-27T10:00:00Z");
    expect(resetCountdown({ reset_at: "2026-11-27T10:23:00Z", resuming: false }, now)).toBe("resets in 23 min");
    expect(resetCountdown({ reset_at: "2026-11-27T12:30:00Z", resuming: false }, now)).toBe("resets in 2 h 30 min");
    expect(resetCountdown({ reset_at: "2026-11-27T09:00:00Z", resuming: true }, now)).toMatch(/resuming/);
  });
});

describe("receipt", () => {
  it("tells stored, held, uncertain and received apart", () => {
    expect(receiptText("stored")?.label).toBe("Stored");
    expect(receiptText("held", "a dialog is open")?.detail).toContain("a dialog is open");
    expect(receiptText("uncertain")?.label).toBe("Delivery uncertain");
    expect(receiptText("received")?.tone).toBe("success");
    expect(receiptText("")).toBeNull();
  });
});
