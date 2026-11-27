// The one attention queue (DESIGN.md 22): the server builds it (internal/attention) and every
// surface renders from it: the nav badge and the tab title (counts.total), the needs-you tray,
// the command palette's "Needs you" group, the Inbox's Open list and the Now view. Nothing in the
// web app counts "what needs you" by itself any more. The `attention` event carries the whole
// snapshot, so the cache is written, not refetched (lib/live.ts).
//
// Also here: the Now view's rows and "since you left", with this browser's id (kept in
// localStorage, so the phone and the desktop each have their own "since").
import type { Approval } from "./approvals";
import type { Job } from "./jobs";
import type { Question } from "./questions";
import type { Agent } from "./types";

export type AttentionKind = "approval" | "question" | "dialog" | "job" | "agent" | "limit" | "wait";
export type Urgency = "blocking" | "can_wait";

export interface ProjectRef {
  id: number;
  slug: string;
  name: string;
}

export interface AttentionAction {
  kind: "decide" | "answer" | "dialog" | "terminal" | "nudge" | "retry" | "restart" | "open" | "land";
  label: string;
}

export interface StandingAgent {
  name: string;
  label: string;
  project_id: number;
  pane_id: string;
  state: string;
  detail?: string;
}

/** One thing waiting on the human (internal/attention.Item). */
export interface AttentionItem {
  id: string;
  kind: AttentionKind;
  urgency: Urgency;
  rank: number;
  since: string;
  title: string;
  detail?: string;
  project: ProjectRef | null;
  job_id?: string;
  job_title?: string;
  agent?: string;
  pane_id?: string;
  action: AttentionAction;
  snoozed_until?: string;
  approval?: Approval;
  question?: Question;
  dialog_agent?: Agent;
  job?: Job;
  standing?: StandingAgent;
  /** A fleet usage limit (DESIGN.md 6.17). */
  limit?: import("./accept").FleetLimit;
}

export interface AttentionCounts {
  total: number;
  blocking: number;
  can_wait: number;
  snoozed: number;
  by_project: Record<string, number>;
}

export interface AttentionSnapshot {
  items: AttentionItem[];
  counts: AttentionCounts;
  revision: number;
  at: string;
  partial?: string[];
}

export const emptyCounts: AttentionCounts = { total: 0, blocking: 0, can_wait: 0, snoozed: 0, by_project: {} };

/** True when data has the shape of a snapshot (the `attention` event's payload). */
export function isSnapshot(data: unknown): data is AttentionSnapshot {
  const candidate = data as Partial<AttentionSnapshot> | null;
  return !!candidate && Array.isArray(candidate.items) && !!candidate.counts && typeof candidate.revision === "number";
}

/** The items not snoozed, in the server's order. */
export function activeItems(snapshot: AttentionSnapshot | undefined): AttentionItem[] {
  return (snapshot?.items ?? []).filter((item) => !item.snoozed_until);
}

/** The items snoozed for now. */
export function snoozedItems(snapshot: AttentionSnapshot | undefined): AttentionItem[] {
  return (snapshot?.items ?? []).filter((item) => !!item.snoozed_until);
}

/** "Crowsnest · Upgrade the Postgres driver": where an item belongs, for a second line. */
export function whereLine(item: AttentionItem): string {
  const parts = [item.project?.name ?? "Unfiled"];
  if (item.job_title && item.job_title !== item.title) parts.push(item.job_title);
  return parts.join(" · ");
}

/** The words for an item's kind on a row. */
export function kindLabel(item: AttentionItem): string {
  switch (item.kind) {
    case "approval": {
      const level = item.approval?.risk;
      return level === "dangerous" ? "Dangerous approval" : level === "elevated" ? "Approval (elevated)" : "Approval";
    }
    case "question":
      return item.urgency === "blocking" ? "Blocking question" : "Question";
    case "dialog":
      return "Terminal dialog";
    case "agent":
      return item.standing?.name === "orchestrator" ? "Orchestrator" : "Lead";
    case "job":
      if (item.action.kind === "land") return item.job?.state === "done" ? "Ready to land" : "Reviewer objects";
      if (item.job?.state === "failed") return "Failed job";
      if (item.job?.state === "queued") return "Waiting to start";
      if (item.job?.cause === "check") return "Check failed";
      if (item.job?.cause === "budget") return "Budget reached";
      if (item.job?.cause === "spinning") return "Spinning";
      if (item.job?.accept_state === "needs_rebase") return "Needs rebase";
      if (item.job?.accept_state === "stale" || item.job?.accept_state === "failed") return "Evidence stale";
      return item.job?.stuck ? "Stuck job" : "Job needs you";
    case "limit":
      return "Usage limit";
    case "wait":
      return "No owner";
  }
}

/**
 * Where an item opens in full: its decision packet (`/decide`, routes/DecideView.tsx) for
 * approvals, questions and dialogs; the job for a job; the agent's terminal for a standing agent.
 */
export function itemLink(item: AttentionItem): { to: string; search: Record<string, string> } {
  switch (item.kind) {
    case "approval":
    case "question":
    case "dialog":
      return { to: "/decide", search: { item: item.id } };
    case "job":
    case "wait":
      return { to: "/jobs", search: { job: item.job_id ?? "" } };
    case "limit":
      return { to: "/jobs", search: {} };
    case "agent":
      return { to: "/agents", search: item.pane_id ? { agent: item.pane_id, view: "terminal" } : {} };
  }
}

/** Markdown reduced to plain text for a one- or three-line preview (R4 Fable 3.3). */
export function plainPreview(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/(\*\*|__|\*|_|~~)(\S[^*_~]*?)\1/g, "$2")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^\s*[-*+>]\s+/gm, "")
    .replace(/\s+/g, " ")
    .trim();
}

// ---- The Now view ----------------------------------------------------------------------------

export interface RowCounts {
  running: number;
  waiting: number;
  done_today: number;
  failed_today: number;
}

export interface NowRow {
  kind: "project" | "unfiled" | "orchestrator";
  project?: ProjectRef;
  lead?: { name: string; state: string; detail?: string; pane_id?: string };
  owner: string;
  doing: string;
  counts: RowCounts;
  spend_today_usd: number | null;
  last_activity_at?: string;
}

export interface NowResponse {
  rows: NowRow[];
  counts: AttentionCounts;
}

export interface SinceEvent {
  kind: "done" | "failed" | "cancelled" | "asked" | "answered";
  at: string;
  title: string;
  detail?: string;
  project: ProjectRef | null;
  job_id?: string;
  ref?: string;
}

export interface SinceResponse {
  since: string;
  first: boolean;
  events: SinceEvent[];
  counts: Record<string, number>;
}

const BROWSER_KEY = "crowsnest-browser";

/** This browser's id: random, kept in localStorage, sent with "since you left" calls. */
export function browserId(): string {
  try {
    let id = localStorage.getItem(BROWSER_KEY);
    if (!id || !/^[A-Za-z0-9_-]{8,64}$/.test(id)) {
      const bytes = new Uint8Array(12);
      crypto.getRandomValues(bytes);
      id = "b" + Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
      localStorage.setItem(BROWSER_KEY, id);
    }
    return id;
  } catch {
    return "b-private-mode";
  }
}
