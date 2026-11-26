// Question fixtures in exactly the server's shape (internal/questions): one of every kind, a
// multi-select, a mermaid body and a diff. The Vitest suites and fixtures mode (?fixtures=1) both
// use them, so they cannot drift apart. Development and tests only.
import type { Question } from "./questions";

const base: Question = {
  id: "q_1",
  title: "",
  body_md: "",
  kind: "confirm",
  multi: false,
  options: [],
  context: {},
  urgency: "normal",
  file_count: 0,
  status: "open",
  answer_value: null,
  answer_note: "",
  source_pane: "w2:p1",
  wait: false,
  created_at: "2026-11-26T03:00:00Z",
  expires_at: "2026-11-27T03:00:00Z",
  settled_at: null,
  delivery: "",
  delivered_at: null,
};

/** A question with defaults filled in. */
export function makeQuestion(overrides: Partial<Question>): Question {
  return { ...base, ...overrides };
}

export const fixtureQuestions: Question[] = [
  makeQuestion({
    id: "q_1",
    title: "Apply the fleet cache refactor?",
    kind: "confirm",
    urgency: "blocking",
    recommend: "yes",
    options: [
      { key: "yes", label: "Apply it", detail: "Tests pass locally (42/42)" },
      { key: "no", label: "Not yet" },
    ],
    context: { repo: "crowsnest", branch: "wip/fleet-cache" },
    body_md: "Coalesce events.\n\n```mermaid\ngraph LR\n  A[event] --> B[150 ms window] --> C[snapshot]\n```",
    files: [{ filename: "internal/fleet/cache.go", original: "a\nb\nc\n", final: "a\nB\nc\nd\n" }],
    file_count: 1,
  }),
  makeQuestion({
    id: "q_2",
    title: "Which findings should the builder fix?",
    kind: "choice",
    multi: true,
    recommend: "race",
    options: [
      { key: "race", label: "Data race in the hub" },
      { key: "leak", label: "Goroutine leak" },
      { key: "names", label: "Rename helpers", detail: "Cosmetic" },
    ],
  }),
  makeQuestion({
    id: "q_3",
    title: "Where should the release notes live?",
    kind: "choice",
    recommend: "changelog",
    options: [
      { key: "changelog", label: "CHANGELOG.md" },
      { key: "releases", label: "GitHub releases" },
    ],
  }),
  makeQuestion({ id: "q_4", title: "What should the README's first sentence say?", kind: "manual" }),
  makeQuestion({ id: "q_5", title: "Heads up: herdr 0.9.5 needed", kind: "ack" }),
  makeQuestion({ id: "q_6", title: "Nightly backup finished", kind: "notice", urgency: "low" }),
  makeQuestion({
    id: "q_7",
    title: "Use SQLite for sessions?",
    kind: "confirm",
    recommend: "yes",
    options: [
      { key: "yes", label: "Yes" },
      { key: "no", label: "No" },
    ],
    status: "answered",
    answer_value: "yes",
    answer_note: "modernc, so CGO stays off",
    settled_at: "2026-11-26T02:00:00Z",
    delivery: "type",
    delivered_at: "2026-11-26T02:00:03Z",
  }),
];
