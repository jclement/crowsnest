-- 0012: structured approvals (DESIGN.md 8.7). A Claude agent's PermissionRequest hook posts the
-- tool call it is about to be asked about; the human allows or denies it from the browser (or a
-- notification, for low-risk ones) and the hook prints the decision. Statuses: pending, allowed,
-- denied, expired (nobody answered in time), superseded (the agent stopped waiting: answered in
-- the terminal, turn interrupted, session ended, crowsnest restarted).
CREATE TABLE approvals (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id        TEXT    NOT NULL DEFAULT '',   -- 'j_12', or '' (the orchestrator holding no job)
    pane_id       TEXT    NOT NULL DEFAULT '',
    agent_name    TEXT    NOT NULL DEFAULT '',
    session_id    TEXT    NOT NULL DEFAULT '',   -- Claude's session id
    cwd           TEXT    NOT NULL DEFAULT '',
    tool_name     TEXT    NOT NULL,
    tool_input    TEXT    NOT NULL DEFAULT '{}', -- JSON, as the hook gave it
    suggestions   TEXT    NOT NULL DEFAULT '[]', -- the hook's permission_suggestions, shown, never applied
    risk          TEXT    NOT NULL,              -- low | elevated | dangerous
    risk_reasons  TEXT    NOT NULL DEFAULT '[]', -- JSON array of sentences
    status        TEXT    NOT NULL DEFAULT 'pending',
    hooked        INTEGER NOT NULL DEFAULT 1,    -- 1: a hook process waits on it; 0: demo
    decided_via   TEXT    NOT NULL DEFAULT '',   -- browser | notification | '' (not decided by a human here)
    note          TEXT    NOT NULL DEFAULT '',   -- the human's note; on deny it is the message the agent gets
    status_reason TEXT    NOT NULL DEFAULT '',   -- why it expired or was superseded
    created_at    INTEGER NOT NULL,
    settled_at    INTEGER NOT NULL DEFAULT 0,
    expires_at    INTEGER NOT NULL
);
CREATE INDEX approvals_by_status ON approvals (status, id);
CREATE INDEX approvals_by_pane ON approvals (pane_id, status);

-- Single-use action tokens carried in a Web Push payload, so a notification's buttons can answer
-- one item without the session cookie (DESIGN.md 8.7). Only the SHA-256 of a token is kept.
CREATE TABLE action_tokens (
    hash        TEXT    PRIMARY KEY,
    item        TEXT    NOT NULL,                -- 'approval:ap_3' or 'question:q_7'
    actions     TEXT    NOT NULL,                -- JSON array of the action names it may perform
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    used_at     INTEGER NOT NULL DEFAULT 0
);
