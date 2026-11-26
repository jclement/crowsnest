// Journal fixtures (M8): entries of every kind for ?fixtures=1, the unit tests and screenshots,
// in exactly the shape of GET /journal, plus a "Poke now" answer.
import type { JournalEntry, JournalPage, PokeResult } from "./journal";

const minute = 60_000;

/** Entries newest first, timed relative to now so "3 min ago" reads right. */
export function makeJournal(now = Date.now()): JournalEntry[] {
  const at = (minutesAgo: number) => new Date(now - minutesAgo * minute).toISOString();
  return [
    { id: 12, at: at(1), kind: "poke", summary: "Poked the orchestrator: builder (pane w2:p1) finished its turn (done), for 47s; job j_4 is done: Fix the flaky parser test", detail: { outcome: "sent", started: true } },
    { id: 11, at: at(3), kind: "skip", summary: "Poke held: the orchestrator is working; the poke waits for its turn to end" },
    { id: 10, at: at(6), kind: "answer", summary: "You answered q_7: Ship the migration tonight?" },
    { id: 9, at: at(9), kind: "send", summary: "You sent orchestrator: what is the reviewer doing?" },
    { id: 8, at: at(14), kind: "backstop", summary: "Nudged j_6 (job-6): it sat idle with nothing declared and no Stop hook ran" },
    { id: 7, at: at(22), kind: "alert", summary: "crowsnest: job j_5 is stuck (stopped three times without saying why): Update the docs" },
    { id: 6, at: at(31), kind: "restart", summary: "Restarted the orchestrator automatically (Claude had exited)" },
    { id: 5, at: at(45), kind: "skip", summary: "Poke now: nothing is due" },
  ];
}

/** A page of the fixture journal, filtered like the server. */
export function journalPage(kinds: string[], before: number, limit = 50): JournalPage {
  const all = makeJournal().filter((entry) => kinds.length === 0 || kinds.includes(entry.kind));
  const older = all.filter((entry) => !before || entry.id < before);
  const entries = older.slice(0, limit);
  return { entries, total: all.length, next_before: older.length > limit ? (entries.at(-1)?.id ?? 0) : 0 };
}

/** What "Poke now" answers in fixtures mode. */
export function fixturePokeResult(): PokeResult {
  return { outcome: "skipped", reason: "nothing is due", reasons: [], manual: true, at: new Date().toISOString() };
}
