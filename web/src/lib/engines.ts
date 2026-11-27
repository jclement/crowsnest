// What a Codex agent does not get from crowsnest, said where an engine is chosen (R8: "Codex
// honestly in the form"): the new-job form, a project's defaults, a schedule's job, and the
// default engine in Settings.
//
// The list is DESIGN.md section 20's (Known limitations): crowsnest supervises Claude through its
// hooks, and Codex has none. The trust builder (wip/trust) is to own the exact list in one Go
// function served by GET /api/v1/engines; until that endpoint exists this sentence is the source,
// and the lead swaps it for the API's (REPORT.md says so).

/** The engines a job can run on. */
export type Engine = "claude" | "codex";

/** Each thing a Codex agent does not get, one short clause each, in DESIGN 20's order. */
export const CODEX_GAPS: readonly string[] = [
  "a stop crowsnest can refuse (it can only nudge about a minute after Codex stops)",
  "spend measurement",
  "spinning detection",
  "usage-limit reset times",
  "answer receipts past “delivered”",
];

/** The note shown beside a Codex choice: what it does not get, and what still bounds it. */
export function codexNote(): string {
  return `Codex runs without crowsnest's hooks, so it does not get ${listWords(CODEX_GAPS)}. Budgets bound it by minutes and turns.`;
}

/** "a, b and c". */
function listWords(items: readonly string[]): string {
  if (items.length <= 1) return items.join("");
  return `${items.slice(0, -1).join(", ")} or ${items.at(-1)}`;
}
