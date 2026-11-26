-- 0006: the job lifecycle claim (R2 fixes; DESIGN.md 6.8 "Lifecycle steps").
--
-- A step that acts outside crowsnest (starting an agent, typing a brief, a retry or a nudge) is
-- claimed on the row first, under the jobs lock: which step, which crowsnest process holds it,
-- when, and a generation that every claim and every terminal transition bumps. The external call
-- is made only while the claim still holds, and its outcome is committed only if it still does,
-- so a job cancelled meanwhile is never restarted or prompted. A claim left by a process that
-- died is found by the supervisor on its next pass and resolved by what the step was.
ALTER TABLE jobs ADD COLUMN lifecycle_op    TEXT    NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN lifecycle_owner TEXT    NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN lifecycle_at    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN lifecycle_gen   INTEGER NOT NULL DEFAULT 0;

-- The supervisor's own queries: the orchestrator's assignment and a job by its pane.
CREATE INDEX jobs_agent ON jobs (agent_name, state);
CREATE INDEX jobs_pane ON jobs (pane_id, state);

-- A job's questions are read by job id on every hook (the open count, the answer typed back).
CREATE INDEX questions_job ON questions (job_id, status);
