// The needs-you tray's rows (DESIGN.md 8.7, 22): the server's attention queue (lib/attention.ts),
// narrowed to the shapes the tray's cards take. The server decides what counts, the order and the
// de-duplication (an approval and its agent's dialog are one row; a job waiting on a question is
// listed through the question); this file only picks the payload each row's card needs. Snoozed
// items are left out here and listed in the Inbox.
import type { Approval } from "./approvals";
import { activeItems, type AttentionItem, type AttentionSnapshot, type StandingAgent } from "./attention";
import type { Job } from "./jobs";
import type { Question } from "./questions";
import type { Agent } from "./types";

/** The event the `g n` shortcut dispatches; the tray listens and takes focus. */
export const FOCUS_TRAY_EVENT = "crowsnest:focus-tray";

interface Base {
  id: string;
  at: string;
  item: AttentionItem;
}

export type TrayItem =
  | (Base & { kind: "approval"; approval: Approval })
  | (Base & { kind: "question"; question: Question })
  | (Base & { kind: "dialog"; agent: Agent })
  | (Base & { kind: "job"; job: Job })
  | (Base & { kind: "agent"; standing: StandingAgent });

/** One tray row for an attention item, or null when the item lacks its payload. */
export function trayItemOf(item: AttentionItem): TrayItem | null {
  const base = { id: item.id, at: item.since, item };
  switch (item.kind) {
    case "approval":
      return item.approval ? { ...base, kind: "approval", approval: item.approval } : null;
    case "question":
      return item.question ? { ...base, kind: "question", question: item.question } : null;
    case "dialog":
      return item.dialog_agent ? { ...base, kind: "dialog", agent: item.dialog_agent } : null;
    case "job":
      return item.job ? { ...base, kind: "job", job: item.job } : null;
    case "agent":
      return item.standing ? { ...base, kind: "agent", standing: item.standing } : null;
  }
}

/** The tray's rows: the queue's unsnoozed items, in the server's order. */
export function trayItems(snapshot: AttentionSnapshot | undefined): TrayItem[] {
  return activeItems(snapshot)
    .map(trayItemOf)
    .filter((row): row is TrayItem => row !== null);
}

/** A one-line title for a row. */
export function trayTitle(row: TrayItem): string {
  return row.item.title;
}
