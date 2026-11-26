-- 0014: landing a finished job, the review gate, start holds and the daily digest (DESIGN.md
-- 6.7, 6.11 to 6.13, 7.12). Times are unix seconds; 0 means unset.

-- What a job in a git worktree started from, so its review can show exactly what it produced.
ALTER TABLE jobs ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';   -- the main checkout's branch at launch
ALTER TABLE jobs ADD COLUMN base_commit TEXT NOT NULL DEFAULT '';   -- the commit the worktree started at
ALTER TABLE jobs ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '';  -- herdr's workspace for the worktree (teardown)

-- The walkthrough the agent attached to done: the change explained chunk by chunk.
ALTER TABLE jobs ADD COLUMN walkthrough_md TEXT NOT NULL DEFAULT '';

-- The review gate. reviewer is what the job asked for: '' the server's default, 'off', 'claude'
-- or 'codex'. review_of marks a reviewer job (the job it reviews). review_round counts the
-- rounds a reviewed job has been through; review_verdict and review_md are the latest round's.
ALTER TABLE jobs ADD COLUMN reviewer TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN review_of INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN review_round INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN review_verdict TEXT NOT NULL DEFAULT '';  -- '' | approve | changes | error
ALTER TABLE jobs ADD COLUMN review_md TEXT NOT NULL DEFAULT '';
CREATE INDEX jobs_review_of ON jobs(review_of) WHERE review_of != 0;

-- Landing: what happened to the job's branch and worktree once it was finished.
-- land_state: '' (not landed) | merged | pr | discarded. land_detail is a sentence or the PR's
-- URL. torn_down_at is when its worktree, workspace and agent were removed.
ALTER TABLE jobs ADD COLUMN land_state TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN land_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN landed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN torn_down_at INTEGER NOT NULL DEFAULT 0;
-- A lead's request to land, waiting on the human: the action asked for and its question.
ALTER TABLE jobs ADD COLUMN land_request TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN land_question_id INTEGER NOT NULL DEFAULT 0;

-- The human's "start anyway": this job skips the slot limit, the load hold and the pause.
ALTER TABLE jobs ADD COLUMN start_anyway INTEGER NOT NULL DEFAULT 0;

-- Daily digests: one row per digest written, by the orchestrator or by crowsnest itself.
CREATE TABLE digests (
    id          INTEGER PRIMARY KEY,
    day         TEXT    NOT NULL,              -- the local calendar day it covers, YYYY-MM-DD
    at          INTEGER NOT NULL,              -- when it was delivered
    source      TEXT    NOT NULL,              -- orchestrator | crowsnest
    body_md     TEXT    NOT NULL,
    question_id INTEGER NOT NULL DEFAULT 0     -- the Inbox notice that carried it
);
CREATE INDEX digests_at ON digests(at);
