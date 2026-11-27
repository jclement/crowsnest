-- 0016: acceptance (crowsnest's own evidence for a finished coding job), budgets, the spinning
-- detector, wake-ups for every wait, the agent's last words and answer receipt (DESIGN.md 6.15
-- to 6.19). Times are unix seconds; 0 means unset.

-- Acceptance. accept_state: '' (no acceptance: no directory, or not a git checkout) | checking
-- (crowsnest is running the check) | accepted (clean tree, commits, check passed at HEAD) |
-- unverified (clean tree and commits, but the project has no check command) | failed (the last
-- check failed) | stale (a commit after the evidence) | needs_rebase (the base branch moved on).
-- accept_detail is one sentence for the card. check_rounds counts failed checks sent back to
-- the agent. no_commits_reason is the agent's explanation for a finished job with no commits.
-- fix_of marks a job crowsnest started to rebase another job's branch (its worktree).
-- review_commit is HEAD when the latest reviewer started, so an approval is pinned to it.
ALTER TABLE jobs ADD COLUMN accept_state TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN accept_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN check_rounds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN no_commits_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN fix_of INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN review_commit TEXT NOT NULL DEFAULT '';

-- Budgets set on the job itself (0: unset, the project's or the server's default applies).
-- budget_state: '' | warned (80%) | exceeded (100%: needs you) | grace (the human let it finish
-- the step it was in). turns counts the agent's ended turns (Stop hooks; the backstop for
-- agents without hooks), which bounds an agent whose spend is not measured.
ALTER TABLE jobs ADD COLUMN budget_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN budget_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN budget_minutes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN budget_turns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN budget_state TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN budget_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN turns INTEGER NOT NULL DEFAULT 0;

-- Every wait has a wake-up. wake_kind: '' | time (wake_at) | job (wake_job settles) | human.
-- rechecks counts the wake-ups of one blocked spell.
ALTER TABLE jobs ADD COLUMN wake_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN wake_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN wake_job INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN rechecks INTEGER NOT NULL DEFAULT 0;
CREATE INDEX jobs_wake_job ON jobs(wake_job) WHERE wake_job != 0;

-- What the agent last said when it ended a turn (the Stop hook's last_assistant_message,
-- capped), shown on stuck and waiting cards.
ALTER TABLE jobs ADD COLUMN last_message TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN last_message_at INTEGER NOT NULL DEFAULT 0;

-- The spinning detector: the working tree's fingerprint when last sampled, and when it last
-- changed.
ALTER TABLE jobs ADD COLUMN tree_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN tree_changed_at INTEGER NOT NULL DEFAULT 0;

-- Projects: the command crowsnest runs to accept a finished job ('' automatic: `mise run check`
-- when the checkout's mise.toml has that task; 'none' for no check), and budget defaults.
ALTER TABLE projects ADD COLUMN check_command TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN budget_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN budget_minutes INTEGER NOT NULL DEFAULT 0;

-- One run of a job's check: what ran, against which commit, and what came of it.
-- status: running | passed | failed | error (it could not run, or timed out).
CREATE TABLE job_checks (
    id           INTEGER PRIMARY KEY,
    job_id       INTEGER NOT NULL,
    commit_sha   TEXT    NOT NULL,
    base_sha     TEXT    NOT NULL DEFAULT '',
    command      TEXT    NOT NULL,
    status       TEXT    NOT NULL,
    exit_code    INTEGER NOT NULL DEFAULT -1,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    output_tail  TEXT    NOT NULL DEFAULT '',
    owner        TEXT    NOT NULL DEFAULT '',
    started_at   INTEGER NOT NULL,
    finished_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX job_checks_job ON job_checks(job_id, id);

-- Answer receipt: when crowsnest saw the asker take the answer in (its next turn started with
-- the answer line in its transcript, or it read it on the long-poll), and how the latest
-- delivery attempt ended when it typed nothing (held: a dialog, a draft, the agent busy).
ALTER TABLE questions ADD COLUMN received_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE questions ADD COLUMN held_reason TEXT NOT NULL DEFAULT '';
