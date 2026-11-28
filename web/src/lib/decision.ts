import type { TrustPrompt } from "./types";
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
import { shortPath } from "./format";
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
  /**
   * True when the consequence says something beyond the choice's own label ("The agent is told you
   * chose this" does not): the packet shows "If you choose" only when some choice has (R10).
   */
  said: boolean;
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
// The same words, inflections, phrase and normalisation as questions.GrantingText (R7-S6, R9-S10).
const GRANTING_WORDS = new Set(
  `allow allows allowed allowing approve approves approved approving approval merge merges merged merging land lands landed
  landing push pushes pushed pushing deploy deploys deployed deploying deployment delete deletes deleted deleting deletion
  grant grants granted granting permit permits permitted permitting proceed proceeds proceeded proceeding authorize authorizes
  authorized authorizing authorise authorises authorised authorising authorization authorisation accept accepts accepted
  accepting confirm confirms confirmed confirming publish publishes published publishing apply applies applied applying`.split(/\s+/),
);
const GRANTING_PHRASES = ["go ahead"];

/** Cyrillic and Greek letters that look like Latin ones, folded onto those (part of UTS #39's skeleton). */
const CONFUSABLES = new Map<number, string>([
  [0x0430, "a"], [0x0435, "e"], [0x043e, "o"], [0x0440, "p"], [0x0441, "c"], [0x0443, "y"], [0x0445, "x"], [0x0456, "i"],
  [0x0458, "j"], [0x0455, "s"], [0x0501, "d"], [0x04cf, "l"], [0x0261, "g"], [0x0251, "a"], [0x0410, "a"], [0x0415, "e"],
  [0x041e, "o"], [0x0420, "p"], [0x0421, "c"], [0x0425, "x"], [0x0406, "i"], [0x0408, "j"], [0x0405, "s"], [0x041c, "m"],
  [0x041d, "h"], [0x0422, "t"], [0x0412, "b"], [0x041a, "k"], [0x03bf, "o"], [0x03b1, "a"], [0x03b5, "e"], [0x03b9, "i"],
  [0x03ba, "k"], [0x03bd, "v"], [0x03c1, "p"], [0x03c4, "t"], [0x03c5, "u"], [0x039f, "o"], [0x0391, "a"], [0x0392, "b"],
  [0x0395, "e"], [0x0397, "h"], [0x0399, "i"], [0x039a, "k"], [0x039c, "m"], [0x039d, "n"], [0x03a1, "p"], [0x03a4, "t"],
  [0x03a7, "x"], [0x0131, "i"],
  // R9-S10: Latin small capitals, Cherokee look-alikes, and the letters that read as l and w.
  [0x1d00, "a"], [0x0299, "b"], [0x1d04, "c"], [0x1d05, "d"], [0x1d07, "e"], [0xa730, "f"], [0x0262, "g"], [0x029c, "h"],
  [0x026a, "i"], [0x1d0a, "j"], [0x1d0b, "k"], [0x029f, "l"], [0x1d0d, "m"], [0x0274, "n"], [0x1d0f, "o"], [0x1d18, "p"],
  [0x0280, "r"], [0xa731, "s"], [0x1d1b, "t"], [0x1d1c, "u"], [0x1d20, "v"], [0x1d21, "w"], [0x028f, "y"], [0x1d22, "z"],
  [0x13aa, "a"], [0x13f4, "b"], [0x13df, "c"], [0x13a0, "d"], [0x13ac, "e"], [0x13c0, "g"], [0x13bb, "h"], [0x13a5, "i"],
  [0x13e7, "j"], [0x13e6, "k"], [0x13de, "l"], [0x13b7, "m"], [0x13be, "o"], [0x13e2, "p"], [0x13a1, "r"], [0x13da, "s"],
  [0x13a2, "t"], [0x13d9, "v"], [0x13b3, "w"], [0x13bd, "y"], [0x13c3, "z"],
  [0x0142, "l"], [0x0141, "l"], [0x03c9, "w"], [0x051d, "w"], [0x051c, "w"], [0x0461, "w"],
]);

/**
 * Text as grantingText reads it: NFKD, format characters and combining marks removed, look-alikes
 * folded, a capital I after a letter read as l (AIIow), lowercased, split into words.
 */
function normalisedWords(text: string): string[] {
  const folded: string[] = [];
  for (const char of text.normalize("NFKD")) {
    if (/\p{Cf}|\p{Mn}/u.test(char)) continue;
    folded.push(CONFUSABLES.get(char.codePointAt(0) ?? 0) ?? char);
  }
  return folded
    .map((char, i) => (char === "I" && i > 0 && /\p{L}/u.test(folded[i - 1]!) ? "l" : char))
    .join("")
    .toLowerCase()
    .split(/[^\p{L}]+/u)
    .filter(Boolean);
}

/** True when text uses a granting word or phrase (whole words, any case, normalised): the same rule as questions.GrantingText. */
export function grantingText(text: string): boolean {
  const words = normalisedWords(text);
  if (words.some((word) => GRANTING_WORDS.has(word))) return true;
  const joined = ` ${words.join(" ")} `;
  return GRANTING_PHRASES.some((phrase) => joined.includes(` ${phrase} `));
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

/**
 * Words that hand an agent a permission or reach outside its worktree. GRANTING_WORDS also holds
 * everyday ones (apply, accept, confirm, proceed, "go ahead"); those still make a question
 * gate-shaped for notifications and rows (isGate, the server's GateShaped), but on their own they
 * no longer cost a second tap in the packet, where the evidence is on screen (R10 Opus F4: "Apply
 * the fleet cache refactor?" asked to confirm that it "widens what agents may do", and a confirm
 * that is always there teaches the second tap to be automatic).
 */
const PERMISSION_WORDS = new Set(
  `allow allows allowed allowing approve approves approved approving approval merge merges merged merging land lands landed
  landing push pushes pushed pushing deploy deploys deployed deploying deployment delete deletes deleted deleting deletion
  grant grants granted granting permit permits permitted permitting authorize authorizes authorized authorizing authorise
  authorises authorised authorising authorization authorisation publish publishes published publishing`.split(/\s+/),
);

/**
 * True when pressing this option in the packet takes the second, deliberate tap: never for a
 * refusing key; always on crowsnest's own gates (land, delegation, task review); otherwise only
 * when the question's title or the option itself uses a permission word (allow, approve, merge,
 * push, deploy, delete, grant, publish, …).
 */
export function needsSecondTap(question: Pick<Question, "context" | "options"> & { title?: string }, key: string): boolean {
  if (!grants(key)) return false;
  if (GATE_CONTEXT_KEYS.some((contextKey) => contextKey in (question.context ?? {}))) return true;
  const option = question.options.find((candidate) => candidate.key === key);
  const texts = [question.title ?? "", key, option?.label ?? ""];
  return texts.some((text) => normalisedWords(text).some((word) => PERMISSION_WORDS.has(word)));
}

/** "Apply it" as the end of a sentence: "apply it" (an all-capital word such as "PR" is kept). */
export function inSentence(label: string): string {
  const first = label.split(/\s/)[0] ?? "";
  if (first.length > 1 && first === first.toUpperCase()) return label;
  return label.charAt(0).toLowerCase() + label.slice(1);
}

// ---- The auto-advance guard -------------------------------------------------------------------

/**
 * After an answer the packet moves to the next decision at once (a phone run through the queue).
 * The next item's buttons then sit where the last tap was, so a reflex double tap or a tap one beat
 * late would answer something nobody read (R10 Opus B1). For this long after an advance the new
 * item's choices are inert, and the packet says what was answered and what is next.
 */
export const ADVANCE_GUARD_MS = 700;

let lastAnswer: { at: number; title: string } | null = null;

/** Records an answer just sent from the packet (what it was, for the next item's notice). */
export function noteAnswered(title: string, now: number = Date.now()): void {
  lastAnswer = { at: now, title };
}

/**
 * The answer that brought the packet here, when it was sent within the guard's reach (a couple of
 * seconds: the next item loads first), else null. Read by the next packet as it mounts.
 */
export function recentAnswer(now: number = Date.now(), within = 3_000): { at: number; title: string } | null {
  return lastAnswer && now - lastAnswer.at <= within ? lastAnswer : null;
}

/** Forgets the last answer (tests). */
export function forgetAnswer(): void {
  lastAnswer = null;
}

// ---- Briefs ----------------------------------------------------------------------------------

/** A question's brief: its options with their details as consequences, the agent's pick. */
export function questionBrief(question: Question, blocking: boolean): DecisionBrief {
  const choices: Choice[] = question.options.map((option) => ({
    key: option.key,
    label: option.label,
    consequence: option.detail?.trim() || defaultConsequence(question, option.key),
    recommended: option.key === question.recommend,
    said: !!option.detail?.trim(),
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
  // The folder's last two parts: the whole absolute path filled three lines on a phone (R10 Fable).
  const where = approval.cwd ? ` in ${shortPath(approval.cwd)}` : "";
  const allowWords =
    approval.risk === "dangerous"
      ? `It runs now${where}. This is dangerous: ${approval.risk_reasons.join("; ") || "see the reasons"}.`
      : `The agent runs it once, now${where}.`;
  return {
    kind: "approval",
    ref: approval.id,
    ask: `Allow ${approval.summary}?`,
    choices: [
      { key: "allow", label: approval.risk === "dangerous" ? "Allow (confirm)" : "Allow once", consequence: allowWords, recommended: false, said: approval.risk !== "low" },
      { key: "deny", label: "Deny", consequence: "It does not run; the agent is told, with your note, and carries on.", recommended: false, said: approval.risk !== "low" },
    ],
    recommendation: approval.risk === "low" ? `${riskLabels.low}: a routine call (${approval.risk_reasons.join("; ") || "nothing risky found"}).` : null,
    blocking: "The agent is waiting for this answer.",
  };
}

/** A dialog's brief: its numbered options, which go to the terminal as digits. */
export function dialogBrief(paneID: string, title: string, options: { key: string; label: string; warning?: string }[], trust?: TrustPrompt): DecisionBrief {
  if (trust) return trustBrief(paneID, title, trust);
  return {
    kind: "dialog",
    ref: paneID,
    ask: title,
    choices: options.map((option) => ({
      key: option.key,
      label: option.label,
      consequence: option.warning ? `Careful: ${option.warning}` : option.key ? `Presses ${option.key} in the agent's terminal.` : "Picks it in the agent's terminal.",
      recommended: false,
      said: !!option.warning,
    })),
    recommendation: null,
    blocking: "The agent is stopped at this dialog.",
  };
}

/**
 * The folder-trust question (DESIGN.md 8.2.1) as a decision: trust the folder, or not. Neither is
 * recommended: it is the person's call, about what is in the folder.
 */
export function trustBrief(paneID: string, title: string, trust: TrustPrompt): DecisionBrief {
  const engine = trust.engine === "codex" ? "Codex" : "Claude Code";
  const folder = trust.folder || "its working folder";
  return {
    kind: "dialog",
    ref: paneID,
    ask: title,
    choices: [
      {
        key: "trust",
        label: "Trust this folder",
        consequence: `${engine} may read, edit and run files in ${folder}, and the folder's own agent settings and hooks run. It remembers the answer for this folder. To spare every new job this question, trust the project's own folder (the project page offers it).`,
        recommended: false,
        said: true,
      },
      {
        key: "deny",
        label: "Don't trust",
        consequence: trust.engine === "codex" ? "Codex goes back to its own launcher; the job waits." : "Claude exits; the job waits.",
        recommended: false,
        said: true,
      },
    ],
    recommendation: null,
    blocking: `The agent cannot start until you answer. Trust it if you know what is in ${folder}.`,
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
