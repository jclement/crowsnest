-- 0016: approval policy (DESIGN.md 8.7, "Policy"). Rules the human keeps so a low-risk call is
-- allowed by the hook at once instead of waiting for a tap: a project's rules (added on the
-- project page) and a job's remembered rules ("Allow and remember for this job", gone when the
-- job ends). A rule is never deleted, only revoked, so the approvals it allowed can still name it.
CREATE TABLE approval_rules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    scope       TEXT    NOT NULL,              -- project | job
    project_id  INTEGER NOT NULL DEFAULT 0,    -- projects.id for a project rule; the job's project for a job rule
    job_id      TEXT    NOT NULL DEFAULT '',   -- 'j_12' for a job rule
    kind        TEXT    NOT NULL,              -- bash | edit | tool
    pattern     TEXT    NOT NULL,              -- a command glob, a directory under the root, a tool name or prefix
    max_risk    TEXT    NOT NULL DEFAULT 'low',-- low | elevated: the riskiest call it may allow (never dangerous)
    note        TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    created_via TEXT    NOT NULL DEFAULT 'browser', -- browser | demo
    revoked_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX approval_rules_by_project ON approval_rules (project_id, revoked_at);
CREATE INDEX approval_rules_by_job ON approval_rules (job_id, revoked_at);

-- The rules that allowed an approval settled by policy (decided_via = 'policy'), comma-separated
-- ids ('3' or '3,5'); '' for every other approval.
ALTER TABLE approvals ADD COLUMN rule_ids TEXT NOT NULL DEFAULT '';
