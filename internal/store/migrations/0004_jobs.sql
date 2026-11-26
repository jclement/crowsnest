-- 0004: jobs and their timelines (DESIGN.md sections 6.1 and 11).
--
-- A job is a piece of work with one assignee, a declared state and a result. The supervisor's
-- bookkeeping (stuck/cause, nudges, restarts, the pending retry) lives on the row so a restarted
-- crowsnest carries on where it was. job_events is the job's timeline: every state change,
-- progress line, nudge, hook, restart, question and message, in order. Times are unix seconds
-- (0 = unset).
CREATE TABLE jobs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT, -- shown as j_<id>
    title             TEXT    NOT NULL,
    brief_md          TEXT    NOT NULL,
    done_when         TEXT    NOT NULL DEFAULT '',
    role              TEXT    NOT NULL DEFAULT 'builder',
    dir               TEXT    NOT NULL DEFAULT '',  -- '' = assigned to the orchestrator
    worktree_branch   TEXT    NOT NULL DEFAULT '',
    work_dir          TEXT    NOT NULL DEFAULT '',  -- where the agent actually runs (the worktree)
    isolate           INTEGER NOT NULL DEFAULT 0,
    engine            TEXT    NOT NULL DEFAULT 'claude', -- claude | codex
    model             TEXT    NOT NULL DEFAULT '',
    parent_id         INTEGER NOT NULL DEFAULT 0,
    state             TEXT    NOT NULL DEFAULT 'queued',
    stuck             INTEGER NOT NULL DEFAULT 0,
    cause             TEXT    NOT NULL DEFAULT '',  -- why stuck, or why needs_you
    cause_detail      TEXT    NOT NULL DEFAULT '',
    blocked_reason    TEXT    NOT NULL DEFAULT '',
    waiting           INTEGER NOT NULL DEFAULT 0,   -- agent declared: nothing left its questions do not block
    progress          TEXT    NOT NULL DEFAULT '',
    last_tool         TEXT    NOT NULL DEFAULT '',  -- summary of the last tool call, the "doing now" fallback
    result_md         TEXT    NOT NULL DEFAULT '',
    verified_json     TEXT    NOT NULL DEFAULT '[]',
    not_verified_json TEXT    NOT NULL DEFAULT '[]',
    fail_reason       TEXT    NOT NULL DEFAULT '',
    pane_id           TEXT    NOT NULL DEFAULT '',
    agent_name        TEXT    NOT NULL DEFAULT '',
    claude_session_id TEXT    NOT NULL DEFAULT '',
    brief_sent        INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    started_at        INTEGER NOT NULL DEFAULT 0,
    settled_at        INTEGER NOT NULL DEFAULT 0,
    last_activity_at  INTEGER NOT NULL DEFAULT 0,
    last_progress_at  INTEGER NOT NULL DEFAULT 0,  -- last tool call or job_update: what resets the nudge budget
    nudges            INTEGER NOT NULL DEFAULT 0,  -- all nudges, for the card
    nudge_streak      INTEGER NOT NULL DEFAULT 0,  -- consecutive nudges with no progress between them
    last_nudge_at     INTEGER NOT NULL DEFAULT 0,
    denials           INTEGER NOT NULL DEFAULT 0,  -- consecutive auto-mode denials
    restarts          INTEGER NOT NULL DEFAULT 0,
    restart_times     TEXT    NOT NULL DEFAULT '', -- comma-separated unix seconds of recent restarts
    retry_at          INTEGER NOT NULL DEFAULT 0,  -- next automatic recovery attempt
    retry_count       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX jobs_state ON jobs (state, created_at);

CREATE TABLE job_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    at          INTEGER NOT NULL,
    kind        TEXT    NOT NULL, -- state, progress, nudge, hook, restart, retry, question, message, launch, stuck, ...
    summary     TEXT    NOT NULL,
    detail_json TEXT    NOT NULL DEFAULT '{}'
);
CREATE INDEX job_events_job ON job_events (job_id, id);
