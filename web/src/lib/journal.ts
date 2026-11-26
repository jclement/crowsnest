// The journal and pokes (DESIGN.md 7.4 and 10): the TypeScript mirror of internal/journal's
// Entry and Page and internal/orch's PokeResult, the kinds with their labels, and the parser
// that splits a poke message into its header and reasons for the compact Chat row.

/** What a journal entry records (internal/journal Kind). */
export const journalKinds = ["poke", "skip", "send", "answer", "restart", "alert", "backstop", "task", "upkeep"] as const;
export type JournalKind = (typeof journalKinds)[number];

export const journalKindLabels: Record<JournalKind, string> = {
  poke: "Poke",
  skip: "Skipped",
  send: "Sent",
  answer: "Answer",
  restart: "Restart",
  alert: "Alert",
  backstop: "Backstop",
  task: "Task",
  upkeep: "Upkeep",
};

/** One entry of GET /journal. */
export interface JournalEntry {
  id: number;
  at: string;
  kind: JournalKind;
  summary: string;
  detail?: Record<string, unknown>;
}

/** One page of GET /journal: newest first; next_before is 0 on the last page. */
export interface JournalPage {
  entries: JournalEntry[];
  total: number;
  next_before: number;
}

/** POST /orchestrator/poke's answer (internal/orch PokeResult). */
export interface PokeResult {
  outcome: "sent" | "skipped" | "nothing";
  reason?: string;
  reasons: string[];
  manual: boolean;
  started?: boolean;
  note?: string;
  at: string;
}

/** The toast for a manual poke's result. */
export function pokeToast(result: PokeResult): { tone: "success" | "info" | "warning"; title: string; description?: string } {
  if (result.outcome === "sent") {
    const count = result.reasons.length;
    return { tone: "success", title: `Poke sent (${count} reason${count === 1 ? "" : "s"})`, description: result.note };
  }
  return { tone: result.reason === "nothing is due" ? "info" : "warning", title: `Poke skipped: ${result.reason ?? "nothing to send"}` };
}

/** A poke message split into its parts. */
export interface ParsedPoke {
  at?: string;
  talk?: boolean;
  reasons: string[];
}

// The talk flag was dropped when the orchestrator began writing a SAY line on every reply; older
// transcripts still carry it, so it stays optional here.
const pokeHeader = /^\[crowsnest poke (\S+?)(?: talk=(on|off))?\]/;

/** Splits `[crowsnest poke <time>]` and its `- reason` lines. Null when it is not a poke. */
export function parsePoke(text: string): ParsedPoke | null {
  const [first, ...rest] = text.split("\n");
  const header = pokeHeader.exec(first ?? "");
  if (!header) return text.startsWith("[crowsnest poke") ? { reasons: rest.map(stripBullet).filter(Boolean) } : null;
  return { at: header[1], talk: header[2] === undefined ? undefined : header[2] === "on", reasons: rest.map(stripBullet).filter(Boolean) };
}

function stripBullet(line: string): string {
  return line.replace(/^\s*-\s?/, "").trim();
}
