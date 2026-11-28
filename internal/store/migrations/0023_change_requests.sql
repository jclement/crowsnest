-- 0023: requested changes hold a finished job, one change at a time, feedback in immutable
-- batches, versioned comment sends, and where an isolated check ran (R9, DESIGN.md 6.15, 6.20, 6.21).
--
-- jobs.changes_state is the human's "request changes" on a finished job (R9-C1): '' none,
-- 'requested' (a fix job is making them), 'fix_failed' (the fix job failed or was cancelled; the
-- human decides) or 'made' (the fix job is done; the human re-accepts). Land refuses every state
-- but ''. Only the human's resolution (Accept the changes / Set aside) clears it.
ALTER TABLE jobs ADD COLUMN changes_state TEXT NOT NULL DEFAULT '';

-- jobs.branch_op is the one operation that may change a finished job's branch at a time (R9-C2):
-- '' none, 'changes:<fix id>' or 'rebase:<fix id>' (a fix job works in the worktree), or 'land'
-- (crowsnest is merging, pushing or discarding it). branch_op_owner names the crowsnest process
-- that took it and branch_op_at when, so start-up recovery can settle one a dead process left.
ALTER TABLE jobs ADD COLUMN branch_op TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN branch_op_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN branch_op_at INTEGER NOT NULL DEFAULT 0;

-- A fix job still running owns its parent's branch now: the reservation the old rows imply.
UPDATE jobs SET branch_op = 'rebase:' || (SELECT MAX(f.id) FROM jobs f WHERE f.fix_of = jobs.id
    AND f.state NOT IN ('done', 'failed', 'cancelled'))
    WHERE EXISTS (SELECT 1 FROM jobs f WHERE f.fix_of = jobs.id AND f.state NOT IN ('done', 'failed', 'cancelled'));

-- jobs.feedback_json replaces pending_feedback (R9-C3, C4, C14): the messages waiting to be typed
-- to the job's agent, as immutable batches {"seq": n, "batches": [{id, kind, text, count, ref,
-- force, state, since}]}. kind is check, review or comments; state is pending or sending (the one
-- batch a delivery claimed). A delivery settles only the batch it claimed; an append is a new
-- batch. The old column's text becomes one pending batch.
ALTER TABLE jobs ADD COLUMN feedback_json TEXT NOT NULL DEFAULT '';
UPDATE jobs SET feedback_json = json_object('seq', 1, 'batches', json_array(json_object('id', 1, 'kind', 'check',
    'text', pending_feedback, 'state', 'pending', 'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))),
    pending_feedback = ''
    WHERE pending_feedback != '';

-- diff_comments.version counts edits of a note's text, sent_version the version that was last
-- delivered (R9-C5): a note is due when sent_version < version, so an edit made while a send was
-- in flight stays due. claim is the send that holds the note right now ('' none) and claim_version
-- the version it took; claimed_at when. uncertain_at marks a note whose send may or may not have
-- arrived (crowsnest stopped mid-send): it is due again, and the human decides.
ALTER TABLE diff_comments ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE diff_comments ADD COLUMN sent_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE diff_comments ADD COLUMN claim TEXT NOT NULL DEFAULT '';
ALTER TABLE diff_comments ADD COLUMN claim_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE diff_comments ADD COLUMN claimed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE diff_comments ADD COLUMN uncertain_at INTEGER NOT NULL DEFAULT 0;
UPDATE diff_comments SET sent_version = 1 WHERE sent_at > 0;

-- job_checks.workspace is the temporary directory an isolated check made its copy in (R9-C7),
-- recorded before the copy is made so start-up can remove one a crash left behind; '' for a
-- worktree check or once removed. tree_before / tree_after hold, for an isolated check, the
-- tracked tree verified in the copy before and after the run (R9-C6).
ALTER TABLE job_checks ADD COLUMN workspace TEXT NOT NULL DEFAULT '';
