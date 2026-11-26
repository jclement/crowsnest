// Jobs in exactly the server's shape (internal/jobs/types.go), for unit tests and ?fixtures=1:
// one job in each state that matters to the board, built by makeJob from a base record.
import type { Job, JobDetail } from "./jobs";

const NOW = Date.parse("2026-11-26T12:00:00Z");
const ago = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

export function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "j_1",
    title: "Add retry back-off to the webhook sender",
    brief_md: "Add retry back-off to the webhook sender.",
    done_when: "",
    role: "builder",
    dir: "/src/crowsnest",
    worktree_branch: "job/1-add-retry-back-off",
    work_dir: "/worktrees/job-1",
    engine: "claude",
    model: "",
    state: "running",
    stuck: false,
    cause: "",
    cause_text: "",
    cause_detail: "",
    blocked_reason: "",
    waiting: false,
    progress: "Writing table tests",
    doing_now: "Writing table tests",
    last_tool: "",
    result_md: "",
    verified: [],
    not_verified: [],
    fail_reason: "",
    pane_id: "w2:p1",
    agent_name: "job-1",
    claude_session_id: "s1",
    created_at: ago(60),
    started_at: ago(59),
    settled_at: null,
    last_activity_at: ago(2),
    nudges: 0,
    restarts: 0,
    retry_at: null,
    retry_count: 0,
    open_questions: 0,
    isolate: true,
    ...overrides,
  };
}

/** A board with every kind of card, relative to `now`. */
export function makeBoard(now: number = Date.now()): Job[] {
  const at = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
  return [
    makeJob({ id: "j_1", last_activity_at: at(2) }),
    makeJob({ id: "j_2", title: "Tidy the README", nudges: 2, last_activity_at: at(1), agent_name: "job-2" }),
    makeJob({
      id: "j_3", title: "Migrate the settings page", stuck: true, cause: "limit", cause_text: "Hit a usage or rate limit",
      cause_detail: "resets at 14:00", retry_at: new Date(now + 4 * 60_000).toISOString(), retry_count: 1, last_activity_at: at(16),
    }),
    makeJob({ id: "j_4", title: "Decide the release date", state: "needs_you", cause: "questions", cause_text: "Waiting on your answers", open_questions: 1, agent_name: "orchestrator", last_activity_at: at(30) }),
    makeJob({ id: "j_5", title: "Fix the flaky test", state: "done", result_md: "Fixed the race.", verified: ["npm test"], not_verified: ["Safari"], settled_at: at(5) }),
    makeJob({ id: "j_6", title: "Port the TUI", state: "failed", fail_reason: "Not released yet.", settled_at: new Date(now - 3 * 86_400_000).toISOString() }),
  ];
}

export function makeDetail(overrides: Partial<JobDetail> = {}): JobDetail {
  return {
    ...makeJob(),
    timeline: [
      { id: 1, at: ago(60), kind: "created", summary: "Job created" },
      { id: 2, at: ago(59), kind: "launch", summary: "Brief sent to job-1" },
      { id: 3, at: ago(10), kind: "nudge", summary: "Nudged (1 of 3): the turn ended with nothing declared" },
    ],
    questions: [],
    ...overrides,
  };
}
