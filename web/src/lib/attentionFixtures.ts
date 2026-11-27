// The attention queue for fixtures mode and the unit tests (DESIGN.md 22): a small stand-in for
// internal/attention.Build over the fixture approvals and questions, so `?fixtures=1` pages and
// Vitest see a snapshot in exactly the server's shape. The real rules (de-duplication with
// dialogs and jobs, failed jobs, standing agents) are tested in Go; this mirrors only what the
// fixtures hold.
import type { Approval } from "./approvals";
import type { AttentionItem, AttentionSnapshot, NowResponse, SinceResponse } from "./attention";
import type { Question } from "./questions";

const crowsnest = { id: 1, slug: "crowsnest", name: "Crowsnest" };
const website = { id: 2, slug: "website", name: "Website" };

const riskRank = { dangerous: 60, elevated: 50, low: 40 } as const;

/** A snapshot of the pending approvals and open questions given. */
export function fixtureSnapshot(approvals: Approval[], questions: Question[]): AttentionSnapshot {
  const items: AttentionItem[] = [];
  for (const approval of approvals) {
    if (approval.status !== "pending") continue;
    items.push({
      // The label as LabelOf gives it, simplified: a low-risk call is taken as one a rule could allow.
      id: `approval:${approval.id}`, kind: "approval", urgency: "blocking", label: approval.risk === "dangerous" ? "dangerous" : approval.risk === "low" ? "approval" : "blocking",
      rank: riskRank[approval.risk], since: approval.created_at,
      title: approval.summary, detail: `${approval.risk} risk`, project: crowsnest, job_id: approval.job_id, job_title: approval.job_id ? `Job ${approval.job_id}` : undefined,
      agent: approval.agent_name, pane_id: approval.pane_id, action: { kind: "decide", label: "Allow or deny" }, approval,
    });
  }
  for (const question of questions) {
    if (question.status !== "open" || question.kind === "notice") continue;
    const blocking = question.urgency === "blocking";
    items.push({
      id: `question:${question.id}`, kind: "question", urgency: blocking ? "blocking" : "can_wait", label: blocking ? "blocking" : "can_wait", rank: blocking ? 50 : question.urgency === "low" ? 20 : 30,
      since: question.created_at, title: question.title, project: question.context.repo === "website" ? website : crowsnest,
      job_id: question.job_id, pane_id: question.source_pane, action: { kind: "answer", label: "Answer" }, question,
    });
  }
  const band = (item: AttentionItem) => (item.label === "approval" ? 1 : item.label === "can_wait" ? 2 : 0);
  items.sort((a, b) => band(a) - band(b) || b.rank - a.rank || a.since.localeCompare(b.since));
  const blocking = items.filter((item) => band(item) === 0).length;
  const approvalCount = items.filter((item) => band(item) === 1).length;
  const byProject: Record<string, number> = {};
  for (const item of items) byProject[String(item.project?.id ?? 0)] = (byProject[String(item.project?.id ?? 0)] ?? 0) + 1;
  return { items, counts: { total: items.length, blocking, approval: approvalCount, can_wait: items.length - blocking - approvalCount, snoozed: 0, by_project: byProject }, revision: 1, at: new Date().toISOString() };
}

/** The Now view's rows for fixtures mode. */
export function fixtureNow(snapshot: AttentionSnapshot): NowResponse {
  const waiting = (id: number) => snapshot.counts.by_project[String(id)] ?? 0;
  return {
    counts: snapshot.counts,
    rows: [
      { kind: "project", project: crowsnest, lead: { name: "lead-crowsnest", state: "idle" }, owner: "Its lead owns every job here.", doing: "j_1 Add retry back-off: Writing table tests", counts: { running: 2, waiting: waiting(1), done_today: 1, failed_today: 0 }, spend_today_usd: 3.42, last_activity_at: new Date().toISOString() },
      { kind: "project", project: website, lead: { name: "lead-website", state: "working" }, owner: "Its lead owns every job here.", doing: "j_2 Tidy the README: Rewriting the macOS steps", counts: { running: 1, waiting: waiting(2), done_today: 0, failed_today: 0 }, spend_today_usd: 0.88 },
      { kind: "unfiled", owner: "Jobs in no project belong to the orchestrator.", doing: "Quiet", counts: { running: 0, waiting: waiting(0), done_today: 0, failed_today: 0 }, spend_today_usd: null },
      { kind: "orchestrator", lead: { name: "orchestrator", state: "idle" }, owner: "The front door.", doing: "Idle, ready for the next thing", counts: { running: 0, waiting: 0, done_today: 0, failed_today: 0 }, spend_today_usd: 1.2 },
    ],
  };
}

/** "Since you left" for fixtures mode. */
export function fixtureSince(): SinceResponse {
  const hourAgo = new Date(Date.now() - 3600_000).toISOString();
  return {
    since: new Date(Date.now() - 8 * 3600_000).toISOString(),
    first: false,
    counts: { done: 1, failed: 1 },
    events: [
      { kind: "done", at: hourAgo, title: "Fix the flaky SSE reconnect test", project: crowsnest, job_id: "j_8", detail: "Fixed the race in sse.test.tsx" },
      { kind: "failed", at: hourAgo, title: "Port the TUI to Bubble Tea v2", project: crowsnest, job_id: "j_9", detail: "Bubble Tea v2 is not released yet" },
    ],
  };
}
