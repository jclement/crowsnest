// Questions from agents (DESIGN.md section 8.1): the TypeScript mirror of internal/questions
// (types.go) in its JSON spelling, plus the small pure helpers the Inbox needs (ordering, labels,
// what an answer looks like). Keep the types a plain mirror of the wire format.
import type { Attachment } from "./attachments";

export type QuestionKind = "confirm" | "choice" | "ack" | "manual" | "notice";
export type QuestionStatus = "open" | "answered" | "expired" | "cancelled";
export type Urgency = "low" | "normal" | "blocking";
export type Delivery = "" | "type" | "poll" | "none";

export interface QuestionOption {
  key: string;
  label: string;
  detail?: string;
}

export interface QuestionFile {
  filename: string;
  original: string;
  final: string;
}

export interface Question {
  id: string;
  title: string;
  body_md: string;
  kind: QuestionKind;
  multi: boolean;
  options: QuestionOption[];
  recommend?: string;
  context: Record<string, string>;
  urgency: Urgency;
  /** Only in GET /questions/{id}; lists leave file contents out. */
  files?: QuestionFile[];
  file_count: number;
  status: QuestionStatus;
  /** A key, an array of keys (multi), "ack", "dismissed", or null. */
  answer_value: string | string[] | null;
  answer_note: string;
  source_pane?: string;
  job_id?: string;
  wait: boolean;
  created_at: string;
  expires_at: string;
  settled_at: string | null;
  delivery: Delivery;
  delivered_at: string | null;
  delivery_error?: string;
  /** The answer's journey to the agent (DESIGN.md 6.19): stored, held, uncertain, failed, delivered or received. */
  receipt?: import("./accept").Receipt;
  received_at?: string | null;
  held_reason?: string;
  /** The asker's files (GET /questions/{id} only). */
  attachments?: Attachment[];
  /** The human's files sent with the answer, with the asker's copies. */
  answer_attachments?: Attachment[];
}

/** The body of POST /questions/{id}/answer. */
export interface AnswerRequest {
  answer_value?: string | string[];
  answer_note?: string;
  /** Uploads (a_…) sent with the answer: copied into the asker's inbox, named in the line typed back. */
  attachments?: string[];
}

/** GET /inbox/summary. */
export interface InboxSummary {
  open: number;
  blocking: number;
}

/** The data of a `question` SSE event. */
export interface QuestionEvent {
  id: string;
  status: QuestionStatus;
  title: string;
  kind: QuestionKind;
  urgency: Urgency;
  change: "asked" | "answered" | "expired" | "cancelled" | "delivered";
}

export const kindLabels: Record<QuestionKind, string> = {
  confirm: "Confirm",
  choice: "Choice",
  ack: "Acknowledge",
  manual: "Written answer",
  notice: "Notice",
};

export const statusLabels: Record<QuestionStatus, string> = {
  open: "Open",
  answered: "Answered",
  expired: "Expired",
  cancelled: "Cancelled",
};

const urgencyRank: Record<Urgency, number> = { blocking: 0, normal: 1, low: 2 };

/** Open first; among open, blocking then normal then low (notices last); newest first within each. */
export function compareQuestions(a: Question, b: Question): number {
  const openA = a.status === "open" ? 0 : 1;
  const openB = b.status === "open" ? 0 : 1;
  if (openA !== openB) return openA - openB;
  if (openA === 0) {
    const rank = rankOf(a) - rankOf(b);
    if (rank !== 0) return rank;
    return b.created_at.localeCompare(a.created_at);
  }
  return (b.settled_at ?? b.created_at).localeCompare(a.settled_at ?? a.created_at);
}

function rankOf(question: Question): number {
  return question.kind === "notice" ? 3 : urgencyRank[question.urgency];
}

/** The labels of the chosen options, for showing a settled answer. */
export function answerSummary(question: Question): string {
  switch (question.status) {
    case "expired":
      return "Expired with no answer";
    case "cancelled":
      return "Cancelled";
    case "open":
      return "";
  }
  switch (question.kind) {
    case "manual":
      return question.answer_note;
    case "ack":
      return "Acknowledged";
    case "notice":
      return "Dismissed";
  }
  const keys = Array.isArray(question.answer_value) ? question.answer_value : question.answer_value ? [question.answer_value] : [];
  return keys.map((key) => question.options.find((option) => option.key === key)?.label ?? key).join(", ");
}

/** The answer request for a kind, from what the form collected. */
export function buildAnswer(question: Question, picked: string[], note: string): AnswerRequest {
  const answer: AnswerRequest = {};
  if (note.trim()) answer.answer_note = note.trim();
  if (question.kind === "confirm" || question.kind === "choice") {
    answer.answer_value = question.multi ? picked : picked[0];
  }
  return answer;
}

/** Whether the form has enough to submit. */
export function canSubmit(question: Question, picked: string[], note: string): boolean {
  switch (question.kind) {
    case "confirm":
    case "choice":
      return picked.length > 0;
    case "manual":
      return note.trim().length > 0;
    default:
      return true;
  }
}
