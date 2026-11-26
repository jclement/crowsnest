// What the global needs-you tray shows (DESIGN.md 8.7), as a pure function of the data the app
// already has: pending approvals, open questions, agents at a terminal dialog and stuck jobs. Most
// urgent first, newest first within the same urgency, snoozed items left out.
//
// Two pairs describe one thing and are shown once: an approval and its agent's terminal dialog
// (the same request, drawn in the terminal at the same moment), and a job waiting on approvals,
// questions or a dialog and those items themselves.
import { compareApprovals, type Approval } from "./approvals";
import { isFinished, type Job } from "./jobs";
import type { Question } from "./questions";
import type { Agent } from "./types";

/** The event the `g n` shortcut dispatches; the tray listens and takes focus. */
export const FOCUS_TRAY_EVENT = "crowsnest:focus-tray";

export type TrayItem =
  | { kind: "approval"; id: string; rank: number; at: string; approval: Approval }
  | { kind: "question"; id: string; rank: number; at: string; question: Question }
  | { kind: "dialog"; id: string; rank: number; at: string; agent: Agent }
  | { kind: "job"; id: string; rank: number; at: string; job: Job };

export interface TraySources {
  approvals?: Approval[];
  questions?: Question[];
  agents?: Agent[];
  jobs?: Job[];
}

/** How urgent each kind is; higher first. */
export const ranks = {
  dangerous: 6,
  elevated: 5,
  blocking: 5,
  low: 4,
  dialog: 4,
  question: 3,
  lowQuestion: 2,
  job: 1,
} as const;

/** Job causes that only the human can fix and no other tray item stands for. */
const jobCausesForTray = new Set(["auth", "config", "launch"]);

export function trayItems(sources: TraySources, snoozed: Record<string, number> = {}, now = Date.now()): TrayItem[] {
  const items: TrayItem[] = [];
  const approvals = (sources.approvals ?? []).filter((approval) => approval.status === "pending").sort(compareApprovals);
  const approvalPanes = new Set(approvals.map((approval) => approval.pane_id).filter(Boolean));
  for (const approval of approvals) {
    items.push({ kind: "approval", id: `approval:${approval.id}`, rank: ranks[approval.risk], at: approval.created_at, approval });
  }
  for (const question of sources.questions ?? []) {
    if (question.status !== "open" || question.kind === "notice") continue;
    const rank = question.urgency === "blocking" ? ranks.blocking : question.urgency === "low" ? ranks.lowQuestion : ranks.question;
    items.push({ kind: "question", id: question.id, rank, at: question.created_at, question });
  }
  for (const agent of sources.agents ?? []) {
    if (!agent.needs_you || approvalPanes.has(agent.pane_id)) continue;
    items.push({ kind: "dialog", id: `dialog:${agent.pane_id}`, rank: ranks.dialog, at: "", agent });
  }
  for (const job of sources.jobs ?? []) {
    if (isFinished(job.state)) continue;
    const needsFix = job.state === "needs_you" && jobCausesForTray.has(job.cause);
    if (!job.stuck && !needsFix) continue;
    items.push({ kind: "job", id: `job:${job.id}`, rank: ranks.job, at: job.last_activity_at ?? job.created_at, job });
  }
  return items
    .filter((item) => (snoozed[item.id] ?? 0) <= now)
    .sort((a, b) => b.rank - a.rank || b.at.localeCompare(a.at));
}

/** A one-line title for an item. */
export function trayTitle(item: TrayItem): string {
  switch (item.kind) {
    case "approval":
      return item.approval.summary;
    case "question":
      return item.question.title;
    case "dialog":
      return `${item.agent.name || item.agent.pane_id} is waiting at a dialog`;
    case "job":
      return `${item.job.id}: ${item.job.title}`;
  }
}

// ---- Snooze --------------------------------------------------------------------------------

export const SNOOZE_MS = 10 * 60 * 1000;
const SNOOZE_KEY = "crowsnest-tray-snoozed";

/** The snoozed items still snoozed at now (expired entries dropped). */
export function readSnoozed(now = Date.now()): Record<string, number> {
  try {
    const raw = JSON.parse(localStorage.getItem(SNOOZE_KEY) ?? "{}") as Record<string, number>;
    return Object.fromEntries(Object.entries(raw).filter(([, until]) => typeof until === "number" && until > now));
  } catch {
    return {};
  }
}

export function writeSnoozed(snoozed: Record<string, number>): void {
  try {
    localStorage.setItem(SNOOZE_KEY, JSON.stringify(snoozed));
  } catch {
    // private mode: the snooze lasts for this page only
  }
}
