// Acceptance, budgets, wake-ups, the fleet's usage limit and answer receipt (DESIGN.md 6.15 to
// 6.19): the TypeScript mirror of the Go shapes, and the pure helpers the cards, the job view,
// the banner and the Inbox use.
import type { ChipTone } from "../components/ui/Chip";

export type AcceptState = "" | "checking" | "accepted" | "unverified" | "failed" | "stale" | "needs_rebase";

/** crowsnest's latest check of a job (Detail.evidence). */
export interface Evidence {
  command: string;
  status: "running" | "passed" | "failed" | "error";
  exit_code: number;
  duration_ms: number;
  commit: string;
  output_tail: string;
  started_at: string;
  finished_at: string | null;
  head?: string;
  stale: boolean;
}

export interface BudgetLimits {
  usd: number;
  tokens: number;
  minutes: number;
  turns: number;
  source: Record<string, string>;
  fleet: boolean;
}

export interface BudgetUse {
  /** null: not measured (never zero). */
  usd: number | null;
  tokens: number | null;
  minutes: number;
  turns: number;
  measured: boolean;
  children: number;
}

export interface BudgetView {
  limits: BudgetLimits;
  used: BudgetUse;
  share: number;
  over: string;
  state: "" | "warned" | "exceeded" | "grace";
  detail: string;
  fleet_usd?: number;
  fleet_ceiling?: number;
}

export interface BudgetInput {
  usd?: number;
  tokens?: number;
  minutes?: number;
  turns?: number;
}

export interface FleetLimit {
  engine: string;
  since: string;
  reset_at: string;
  guessed: boolean;
  job: string;
  detail: string;
  held: number;
  resuming: boolean;
}

export interface Violation {
  job_id: string;
  title: string;
  state: string;
  cause: string;
  rule: string;
  since: string;
}

export interface WaitsView {
  violations: Violation[];
  checked_at?: string;
}

export interface ProjectAccept {
  project_id: number;
  check_command: string;
  budget_usd: number;
  budget_minutes: number;
  effective: string;
  effective_source: string;
}

export type Receipt = "" | "stored" | "held" | "uncertain" | "failed" | "delivered" | "received";

// ---- Acceptance ------------------------------------------------------------------------------

/** The acceptance chip a card shows beside its state, or null for none. */
export function acceptChip(job: { state: string; accept_state?: AcceptState; check_rounds?: number }): { label: string; tone: ChipTone; title: string } | null {
  switch (job.accept_state) {
    case "checking":
      return { label: "Checking", tone: "accent", title: "crowsnest is running the project's check" };
    case "accepted":
      return { label: "Accepted", tone: "success", title: "Clean tree, commits, and crowsnest's check passed at this commit" };
    case "unverified":
      return { label: "Unverified", tone: "warning", title: "Finished with a clean tree and commits, but the project has no check command" };
    case "failed":
      return {
        label: job.state === "running" ? `Check failed (round ${job.check_rounds ?? 1})` : "Check failed",
        tone: "danger",
        title: "crowsnest's check of the agent's done failed",
      };
    case "stale":
      return { label: "Evidence stale", tone: "warning", title: "A commit came after the check: run it again before landing" };
    case "needs_rebase":
      return { label: "Needs rebase", tone: "warning", title: "The base branch moved on since the branch started" };
    default:
      return null;
  }
}

/** "1 min 4 s" for a check's duration. */
export function durationText(ms: number): string {
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds} s`;
  const minutes = Math.floor(seconds / 60);
  return seconds % 60 ? `${minutes} min ${seconds % 60} s` : `${minutes} min`;
}

// ---- Budgets ---------------------------------------------------------------------------------

/** The budget chip a card shows, or null when there is nothing to say. */
export function budgetChip(budget: BudgetView | undefined): { label: string; tone: ChipTone; title: string } | null {
  if (!budget) return null;
  const percent = Math.round(budget.share * 100);
  switch (budget.state) {
    case "exceeded":
      return { label: "Over budget", tone: "danger", title: budget.detail || "Reached its budget" };
    case "grace":
      return { label: "Finishing its step", tone: "warning", title: "At its budget: it may finish the step it is in" };
    case "warned":
      return { label: `Budget ${percent}%`, tone: "warning", title: budget.detail || `Used ${percent}% of its budget` };
  }
  if (budget.share >= 0.5) return { label: `Budget ${percent}%`, tone: "neutral", title: `Used ${percent}% of its budget` };
  return null;
}

/** The measures of a budget, each "used of limit", with "not measured" where spend is unknown. */
export function budgetRows(budget: BudgetView): Array<{ label: string; used: string; limit: string; share: number | null }> {
  const rows: Array<{ label: string; used: string; limit: string; share: number | null }> = [];
  const { limits, used } = budget;
  if (limits.usd > 0) {
    rows.push({ label: "Dollars", used: used.usd === null ? "not measured" : `$${used.usd.toFixed(2)}`, limit: `$${limits.usd.toFixed(2)}`, share: used.usd === null ? null : used.usd / limits.usd });
  }
  if (limits.fleet && budget.fleet_ceiling) {
    rows.push({ label: "Fleet today", used: `$${(budget.fleet_usd ?? 0).toFixed(2)}`, limit: `$${budget.fleet_ceiling.toFixed(2)}`, share: (budget.fleet_usd ?? 0) / budget.fleet_ceiling });
  }
  if (limits.tokens > 0) {
    rows.push({ label: "Tokens", used: used.tokens === null ? "not measured" : compact(used.tokens), limit: compact(limits.tokens), share: used.tokens === null ? null : used.tokens / limits.tokens });
  }
  if (limits.minutes > 0) rows.push({ label: "Minutes", used: `${Math.round(used.minutes)}`, limit: `${limits.minutes}`, share: used.minutes / limits.minutes });
  if (limits.turns > 0) rows.push({ label: "Turns", used: `${used.turns}`, limit: `${limits.turns}`, share: used.turns / limits.turns });
  return rows;
}

function compact(value: number): string {
  if (value >= 1e6) return `${(value / 1e6).toFixed(1)}M`;
  if (value >= 1e3) return `${Math.round(value / 1e3)}k`;
  return String(value);
}

// ---- The usage limit -------------------------------------------------------------------------

/** "resets in 23 min" / "resetting now" for the banner's countdown. */
export function resetCountdown(limit: Pick<FleetLimit, "reset_at" | "resuming">, now: number = Date.now()): string {
  if (limit.resuming) return "resuming the waiting jobs one by one";
  const seconds = Math.round((Date.parse(limit.reset_at) - now) / 1000);
  if (seconds <= 0) return "resetting now";
  if (seconds < 60) return `resets in ${seconds} s`;
  if (seconds < 3600) return `resets in ${Math.round(seconds / 60)} min`;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.round((seconds % 3600) / 60);
  return minutes ? `resets in ${hours} h ${minutes} min` : `resets in ${hours} h`;
}

// ---- Receipt ---------------------------------------------------------------------------------

/** An answer's receipt in words, with a tone. */
export function receiptText(receipt: Receipt | undefined, heldReason?: string): { label: string; tone: ChipTone; detail: string } | null {
  switch (receipt) {
    case "stored":
      return { label: "Stored", tone: "neutral", detail: "Your answer is saved; it has not reached the agent yet." };
    case "held":
      return { label: "Held", tone: "warning", detail: `The agent could not take it yet${heldReason ? ` (${heldReason})` : ""}; crowsnest keeps trying.` };
    case "uncertain":
      return { label: "Delivery uncertain", tone: "warning", detail: "It was handed to the terminal but crowsnest lost the answer. Check whether it arrived before typing it again." };
    case "failed":
      return { label: "Not delivered", tone: "danger", detail: "Typing it into the agent failed." };
    case "delivered":
      return { label: "Delivered", tone: "accent", detail: "Typed into the agent; not yet seen in its conversation." };
    case "received":
      return { label: "Received", tone: "success", detail: "The agent took it in: it is in its conversation." };
    default:
      return null;
  }
}
