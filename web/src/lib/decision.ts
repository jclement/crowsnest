// The decision packet (R4 Codex P1/P2, Fable 3.2/3.3): one screen per decision that reads top to
// bottom (where it belongs, what is asked, what each choice does, the evidence, the
// recommendation) with the answer within thumb reach. This file is the pure part: it turns an
// attention item (or the record it stands for, once it has left the queue) into the packet's
// words, says when a decision is stale, and keeps the human's note in localStorage so a rotation,
// a trip to another app or iOS discarding the page in the background never loses it.
//
// The item id is the attention queue's (`question:q_7`, `approval:ap_3`, `dialog:w1:p2`), so a
// push, the tray, Now and the Inbox all link to the same packet: `/decide?item=<id>`.
import type { Approval } from "./approvals";
import { riskLabels } from "./approvals";
import type { AttentionItem } from "./attention";
import type { Question } from "./questions";

/** The kinds of decision the packet answers. Job and standing-agent items open elsewhere. */
export type DecisionKind = "question" | "approval" | "dialog";

/** One choice and what it does, in a line. */
export interface Choice {
  key: string;
  label: string;
  /** What happens when this is chosen, in a line. */
  consequence: string;
  recommended: boolean;
}

/** The packet's words. */
export interface DecisionBrief {
  kind: DecisionKind;
  /** The record's id: q_7, ap_3, or the dialog's pane id. */
  ref: string;
  /** What is being asked, in a line. */
  ask: string;
  choices: Choice[];
  /** The recommendation in a sentence, or null when nobody made one. */
  recommendation: string | null;
  /** What stays blocked until this is answered, or null. */
  blocking: string | null;
}

/** Splits an attention item id into its kind and record id; null for anything not a decision. */
export function parseItemId(item: string | undefined): { kind: DecisionKind; ref: string } | null {
  if (!item) return null;
  const colon = item.indexOf(":");
  if (colon <= 0) return null;
  const kind = item.slice(0, colon);
  const ref = item.slice(colon + 1);
  if (!ref) return null;
  if (kind === "question" || kind === "approval" || kind === "dialog") return { kind, ref };
  return null;
}

/** The packet's link for an attention item, or null when the item is not a decision. */
export function decisionLink(item: Pick<AttentionItem, "id" | "kind">): { to: "/decide"; search: { item: string } } | null {
  return item.kind === "question" || item.kind === "approval" || item.kind === "dialog" ? { to: "/decide", search: { item: item.id } } : null;
}

/** Longer bodies than this are evidence to read, not a preview: the question opens its packet. */
const ROUTINE_BODY_CHARS = 400;

/**
 * A question answerable from a row or a notification: one choice, no files, a short body. Any
 * other question opens the packet, so a consequential choice never gets a shorter path than its
 * evidence (R4 Codex P2).
 */
export function isRoutine(question: Pick<Question, "kind" | "multi" | "files" | "body_md" | "context" | "options">): boolean {
  return (
    (question.kind === "confirm" || question.kind === "choice") &&
    !question.multi &&
    !question.files?.length &&
    question.body_md.length <= ROUTINE_BODY_CHARS &&
    !isGate(question)
  );
}

/**
 * Context keys crowsnest reserves for its own gate questions (land requests, delegation approvals,
 * task file reviews): the same list as internal/questions/gate.go, whose GateShaped is the
 * authority (the push action endpoint refuses what it calls a gate).
 */
const GATE_CONTEXT_KEYS = ["land_for", "land_action", "land_token", "approval_for", "requested_by", "task_review", "task_hash", "crowsnest_gate"];
/** Words that grant something: a question whose option keys, labels or title use one is a gate (R6-S9). */
const GRANTING_WORDS = new Set(["allow", "allowed", "approve", "approved", "approval", "merge", "land", "push", "deploy", "delete", "grant", "permit"]);

/** True when text uses a granting word (whole words, any case): the same rule as questions.GrantingText. */
export function grantingText(text: string): boolean {
  return text
    .toLowerCase()
    .split(/[^\p{L}]+/u)
    .some((word) => GRANTING_WORDS.has(word));
}
/** Option keys that refuse: choosing one never needs the gate's confirmation. */
const REFUSING_KEYS = new Set(["deny", "no", "reject", "cancel", "later", "not_yet"]);

/**
 * A question that widens what agents may do (crowsnest's land and delegation gates, or anything
 * whose options or title allow, approve, merge, push, deploy or delete). Mirrors questions.GateShaped: such a question
 * is never answerable from a notification or a row, and in the packet a granting choice takes a
 * second, deliberate tap (R5-S2).
 */
export function isGate(question: Pick<Question, "context" | "options"> & { title?: string }): boolean {
  if (GATE_CONTEXT_KEYS.some((key) => key in (question.context ?? {}))) return true;
  if (question.title && grantingText(question.title)) return true;
  return question.options.some((option) => grantingText(option.key) || grantingText(option.label));
}

/** True when choosing this key on a gate question needs the second tap. */
export function grants(key: string): boolean {
  return !REFUSING_KEYS.has(key.toLowerCase());
}

// ---- Briefs ----------------------------------------------------------------------------------

/** A question's brief: its options with their details as consequences, the agent's pick. */
export function questionBrief(question: Question, blocking: boolean): DecisionBrief {
  const choices: Choice[] = question.options.map((option) => ({
    key: option.key,
    label: option.label,
    consequence: option.detail?.trim() || defaultConsequence(question, option.key),
    recommended: option.key === question.recommend,
  }));
  const recommended = question.options.find((option) => option.key === question.recommend);
  return {
    kind: "question",
    ref: question.id,
    ask: question.title,
    choices,
    recommendation: recommended ? `The agent recommends “${recommended.label}”.${recommended.detail ? ` ${recommended.detail}` : ""}` : null,
    blocking: blocking || question.urgency === "blocking" ? "The agent is waiting on this answer." : null,
  };
}

function defaultConsequence(question: Question, key: string): string {
  if (question.kind === "confirm") return key === "yes" ? "The agent goes ahead." : key === "no" ? "The agent does not do it." : "The agent is told.";
  return "The agent is told you chose this.";
}

/** An approval's brief: allow or deny and what each does, with the risk in words. */
export function approvalBrief(approval: Approval): DecisionBrief {
  const where = approval.cwd ? ` in ${approval.cwd}` : "";
  const allowWords =
    approval.risk === "dangerous"
      ? `It runs now${where}. This is dangerous: ${approval.risk_reasons.join("; ") || "see the reasons"}.`
      : `The agent runs it once, now${where}.`;
  return {
    kind: "approval",
    ref: approval.id,
    ask: `Allow ${approval.summary}?`,
    choices: [
      { key: "allow", label: approval.risk === "dangerous" ? "Allow (confirm)" : "Allow once", consequence: allowWords, recommended: false },
      { key: "deny", label: "Deny", consequence: "It does not run; the agent is told, with your note, and carries on.", recommended: false },
    ],
    recommendation: approval.risk === "low" ? `${riskLabels.low}: a routine call (${approval.risk_reasons.join("; ") || "nothing risky found"}).` : null,
    blocking: "The agent is waiting for this answer.",
  };
}

/** A dialog's brief: its numbered options, which go to the terminal as digits. */
export function dialogBrief(paneID: string, title: string, options: { key: string; label: string; warning?: string }[]): DecisionBrief {
  return {
    kind: "dialog",
    ref: paneID,
    ask: title,
    choices: options.map((option) => ({
      key: option.key,
      label: option.label,
      consequence: option.warning ? `Careful: ${option.warning}` : option.key ? `Presses ${option.key} in the agent's terminal.` : "Picks it in the agent's terminal.",
      recommended: false,
    })),
    recommendation: null,
    blocking: "The agent is stopped at this dialog.",
  };
}

// ---- Staleness -------------------------------------------------------------------------------

/**
 * Why a decision can no longer be taken, in a sentence, or null while it is live. A question is
 * live while open, an approval while pending; a dialog is live while its item is in the queue
 * (the caller passes `present`).
 */
export function questionStale(question: Question): string | null {
  switch (question.status) {
    case "open":
      return null;
    case "answered":
      return "Already answered.";
    case "expired":
      return "This question expired with no answer (expiry is not consent).";
    case "cancelled":
      return "The question was cancelled.";
  }
}

export function approvalStale(approval: Approval): string | null {
  switch (approval.status) {
    case "pending":
      return null;
    case "allowed":
      return approval.decided_via === "policy"
        ? `Already allowed by a rule. ${approval.status_reason ?? ""}`.trim()
        : `Already allowed${approval.decided_via === "notification" ? " from a notification" : ""}.`;
    case "denied":
      return "Already denied.";
    case "expired":
      return "Expired with no answer; the terminal dialog may still be there.";
    case "superseded":
      return `No longer waiting: ${approval.status_reason || "the agent moved on"}`;
  }
}

// ---- The note: kept across rotation, backgrounding and reloads ------------------------------

const NOTE_PREFIX = "crowsnest-note:";
/** Notes older than this are dropped when any note is read, so the store does not grow forever. */
const NOTE_MAX_AGE_MS = 3 * 24 * 60 * 60 * 1000;

interface StoredNote {
  text: string;
  at: number;
}

/** The note typed for an item, or "" (and old notes swept). */
export function loadNote(item: string, storage: Storage | undefined = safeStorage(), now = Date.now()): string {
  if (!storage) return "";
  sweepNotes(storage, now);
  const raw = storage.getItem(NOTE_PREFIX + item);
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw) as StoredNote;
    return typeof parsed.text === "string" ? parsed.text : "";
  } catch {
    return "";
  }
}

/** Saves the note for an item; an empty note removes it. */
export function saveNote(item: string, text: string, storage: Storage | undefined = safeStorage(), now = Date.now()): void {
  if (!storage) return;
  try {
    if (!text) storage.removeItem(NOTE_PREFIX + item);
    else storage.setItem(NOTE_PREFIX + item, JSON.stringify({ text, at: now } satisfies StoredNote));
  } catch {
    // A full or refused store: the note still lives in the page; nothing else to do.
  }
}

function sweepNotes(storage: Storage, now: number): void {
  const stale: string[] = [];
  for (let index = 0; index < storage.length; index++) {
    const key = storage.key(index);
    if (!key?.startsWith(NOTE_PREFIX)) continue;
    try {
      const parsed = JSON.parse(storage.getItem(key) ?? "") as StoredNote;
      if (now - parsed.at > NOTE_MAX_AGE_MS) stale.push(key);
    } catch {
      stale.push(key);
    }
  }
  for (const key of stale) storage.removeItem(key);
}

function safeStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

// ---- The agent's last words ------------------------------------------------------------------

/** The last assistant text of a conversation, trimmed to about `max` characters. */
export function lastAssistantText(messages: { kind: string; text?: string }[] | undefined, max = 600): string | null {
  if (!messages) return null;
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (!message) continue;
    if (message.kind === "assistant" && message.text?.trim()) {
      const text = message.text.trim();
      return text.length > max ? `${text.slice(0, max - 1)}…` : text;
    }
  }
  return null;
}
