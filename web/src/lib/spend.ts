// Spend (DESIGN.md 7.11): the TypeScript mirror of internal/spend's shapes, and the pure helpers
// the chip, the job's Spend section and the breakdown sheet share: money and token formatting,
// the one way a cost is worded ("$1.20 est.", "≥ $1.20 est.", "cost unknown", "not measured"),
// sorting a report's rows, and its CSV export.

/** A job's or the orchestrator's spend, as GET /jobs, /jobs/{id} and /spend return it. */
export interface SpendSummary {
  /** False when crowsnest cannot measure it (reason says why): the zeros mean unknown. */
  measured: boolean;
  reason?: "codex" | "orchestrator" | string;
  input_tokens: number;
  output_tokens: number;
  cache_write_tokens: number;
  cache_read_tokens: number;
  total_tokens: number;
  turns: number;
  tool_calls: number;
  wall_seconds: number;
  /** null when no token could be priced (an unknown model): never a misleading zero. */
  cost_usd: number | null;
  /** Some tokens are from a model with no known price; cost_usd covers the rest. */
  cost_partial: boolean;
  unpriced_tokens: number;
  cache_hit_share: number;
  models: string[];
  first_at: string | null;
  last_at: string | null;
}

/** One model call for the job's chart. */
export interface SpendTurn {
  at: string;
  model: string;
  tokens: number;
  output: number;
  cost_usd: number | null;
}

export type SpendGroup = "job" | "model" | "day";

export interface SpendRow extends SpendSummary {
  key: string;
  label: string;
}

/** GET /spend */
export interface SpendReport {
  from: string;
  to: string;
  group: SpendGroup;
  rows: SpendRow[];
  total: SpendRow;
  prices_checked: string;
  prices_source: string;
  prices_file?: string;
  note: string;
}

/** GET /spend/summary */
export interface SpendPeriod {
  today: SpendRow;
  week: SpendRow;
  orchestrator_today: SpendRow;
  orchestrator_total: SpendSummary;
  today_date: string;
  week_start: string;
  prices_checked: string;
  note: string;
}

/** The sentence every spend view carries (the server sends the same one as `note`). */
export const SPEND_NOTE = "An API-equivalent estimate from the agents' transcripts. Subscription plans are not billed per token, so this is not a bill.";

/** $0.004, $0.27, $12.40, $1,204. */
export function formatUSD(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(3)}`;
  if (usd < 1000) return `$${usd.toFixed(2)}`;
  return `$${Math.round(usd).toLocaleString("en-US")}`;
}

/** 950, 1.2k, 12k, 4.56M. */
export function formatTokens(n: number): string {
  const trim = (s: string) => (s.includes(".") ? s.replace(/\.?0+$/, "") : s);
  if (n >= 1_000_000) return `${trim((n / 1e6).toFixed(2))}M`;
  if (n >= 10_000) return `${Math.round(n / 1e3)}k`;
  if (n >= 1_000) return `${trim((n / 1e3).toFixed(1))}k`;
  return String(n);
}

/** The cost as words: "$1.20 est.", "≥ $1.20 est.", "cost unknown". */
export function costText(spend: Pick<SpendSummary, "cost_usd" | "cost_partial">): string {
  if (spend.cost_usd === null) return "cost unknown";
  return `${spend.cost_partial ? "≥ " : ""}${formatUSD(spend.cost_usd)} est.`;
}

/**
 * What the card's chip says, or null when there is nothing worth a chip (no calls yet). An
 * unmeasured job says so; an unknown model shows tokens instead of a cost.
 */
export function chipText(spend: SpendSummary | undefined): { text: string; title: string } | null {
  if (!spend) return null;
  if (!spend.measured) {
    if (spend.reason === "orchestrator") return null; // its use is on the orchestrator's line
    return { text: "not measured", title: notMeasuredText(spend) };
  }
  if (spend.turns === 0 && spend.total_tokens === 0) return null;
  const title = `${formatTokens(spend.total_tokens)} tokens in ${spend.turns} model calls. ${SPEND_NOTE}`;
  if (spend.cost_usd === null) return { text: `${formatTokens(spend.total_tokens)} tok`, title: `Cost unknown: no price for ${spend.models.join(", ")}. ${title}` };
  return { text: `${spend.cost_partial ? "≥" : ""}${formatUSD(spend.cost_usd)}`, title: `${costText(spend)}. ${title}` };
}

/** Why a job is not measured, in words. */
export function notMeasuredText(spend: SpendSummary): string {
  if (spend.reason === "codex") return "Not measured: crowsnest does not read Codex's usage records.";
  if (spend.reason === "orchestrator") return "Counted on the orchestrator's line: the orchestrator does this job in its own conversation.";
  return "Not measured.";
}

/** 45s, 12m, 3h 05m. */
export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.max(0, Math.round(seconds))}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`;
}

export function formatShare(share: number): string {
  return `${Math.round(share * 100)}%`;
}

// ---- The breakdown table -------------------------------------------------------------------

export type SpendSortKey = "label" | "cost" | "total" | "input" | "output" | "cache_write" | "cache_read" | "turns" | "tool_calls";

export interface SpendSort {
  key: SpendSortKey;
  descending: boolean;
}

function sortValue(row: SpendRow, key: SpendSortKey): number | string {
  switch (key) {
    case "label":
      return row.label.toLowerCase();
    case "cost":
      return row.cost_usd ?? -1; // unknown sorts below every known cost
    case "total":
      return row.total_tokens;
    case "input":
      return row.input_tokens;
    case "output":
      return row.output_tokens;
    case "cache_write":
      return row.cache_write_tokens;
    case "cache_read":
      return row.cache_read_tokens;
    case "turns":
      return row.turns;
    case "tool_calls":
      return row.tool_calls;
  }
}

/** A sorted copy of rows; ties keep the key order so the table never shuffles on refresh. */
export function sortRows(rows: SpendRow[], sort: SpendSort): SpendRow[] {
  return [...rows].sort((a, b) => {
    const x = sortValue(a, sort.key);
    const y = sortValue(b, sort.key);
    const order = x < y ? -1 : x > y ? 1 : a.key.localeCompare(b.key);
    return sort.descending && x !== y ? -order : order;
  });
}

const CSV_COLUMNS: { header: string; value: (row: SpendRow) => string | number }[] = [
  { header: "key", value: (row) => row.key },
  { header: "label", value: (row) => row.label },
  { header: "cost_usd_estimate", value: (row) => (row.cost_usd === null ? "" : row.cost_usd.toFixed(6)) },
  { header: "cost_partial", value: (row) => (row.cost_partial ? "yes" : "no") },
  { header: "input_tokens", value: (row) => row.input_tokens },
  { header: "output_tokens", value: (row) => row.output_tokens },
  { header: "cache_write_tokens", value: (row) => row.cache_write_tokens },
  { header: "cache_read_tokens", value: (row) => row.cache_read_tokens },
  { header: "total_tokens", value: (row) => row.total_tokens },
  { header: "model_calls", value: (row) => row.turns },
  { header: "tool_calls", value: (row) => row.tool_calls },
  { header: "unpriced_tokens", value: (row) => row.unpriced_tokens },
];

function csvField(value: string | number): string {
  const text = String(value);
  // A leading =, +, - or @ would run as a formula in a spreadsheet: job titles are written by
  // people and agents.
  const safe = /^[=+\-@]/.test(text) ? `'${text}` : text;
  return /[",\n\r]/.test(safe) ? `"${safe.replace(/"/g, '""')}"` : safe;
}

/** The report as CSV, UTF-8 with a BOM (so Excel reads it as UTF-8), the total last. */
export function reportCSV(report: SpendReport, rows: SpendRow[] = report.rows): string {
  const lines = [CSV_COLUMNS.map((column) => column.header).join(",")];
  for (const row of [...rows, report.total]) lines.push(CSV_COLUMNS.map((column) => csvField(column.value(row))).join(","));
  return `\uFEFF${lines.join("\r\n")}\r\n`;
}

/** YYYY-MM-DD in the browser's local time. */
export function localDate(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

export type SpendRange = "today" | "week" | "30d";

/**
 * The from/to dates of a range, as the server's period reports them: today and the week come from
 * GET /spend/summary (the server's local calendar); 30 days counts back from the server's today.
 */
export function rangeDates(range: SpendRange, period: Pick<SpendPeriod, "today_date" | "week_start"> | undefined): { from?: string; to?: string } {
  if (!period) return {};
  if (range === "today") return { from: period.today_date, to: period.today_date };
  if (range === "week") return { from: period.week_start, to: period.today_date };
  const [year, month, day] = period.today_date.split("-").map(Number);
  const start = new Date(year ?? 1970, (month ?? 1) - 1, (day ?? 1) - 29);
  return { from: localDate(start), to: period.today_date };
}

// ---- The per-call chart ------------------------------------------------------------------

/** At most this many bars; older calls are summed into the leftmost bars by bucketing. */
const MAX_BARS = 80;

export interface TurnBar {
  tokens: number;
  cost: number | null;
  calls: number;
  from: string;
  to: string;
  model: string;
}

/** Groups turns into at most MAX_BARS bars of consecutive calls. */
export function bucketTurns(turns: SpendTurn[], maxBars = MAX_BARS): TurnBar[] {
  const size = Math.max(1, Math.ceil(turns.length / maxBars));
  const bars: TurnBar[] = [];
  for (let i = 0; i < turns.length; i += size) {
    const group = turns.slice(i, i + size);
    const first = group[0];
    const last = group[group.length - 1];
    if (!first || !last) continue;
    const costs = group.map((turn) => turn.cost_usd);
    bars.push({
      tokens: group.reduce((sum, turn) => sum + turn.tokens, 0),
      cost: costs.some((cost) => cost === null) ? null : costs.reduce<number>((sum, cost) => sum + (cost ?? 0), 0),
      calls: group.length,
      from: first.at,
      to: last.at,
      model: [...new Set(group.map((turn) => turn.model))].join(", "),
    });
  }
  return bars;
}
