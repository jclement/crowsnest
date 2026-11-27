-- 0019: holds, kept apart from the agent-declared state (R6-C01, DESIGN.md 6.20).
--
-- holds_json is the set of things that keep a job where it is until someone other than its agent
-- lets it go: a JSON array of {kind, lift, why, since, from}. kind is budget, check, review,
-- approval, questions, approval_tool, usage_limit, checking or reviewing; lift is "human" or
-- "crowsnest". internal/jobs applies them after every change (a held job stays in the state its
-- holds imply) and refuses every agent call that would change or clear one.
ALTER TABLE jobs ADD COLUMN holds_json TEXT NOT NULL DEFAULT '[]';

-- The holds the existing rows already imply, so an upgrade does not open a gate.
UPDATE jobs SET holds_json = json_array(json_object('kind', 'budget', 'lift', 'human', 'why', cause_detail,
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'needs_you' AND cause = 'budget';
UPDATE jobs SET holds_json = json_array(json_object('kind', 'check', 'lift', 'human', 'why', cause_detail,
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'needs_you' AND cause = 'check';
UPDATE jobs SET holds_json = json_array(json_object('kind', 'review', 'lift', 'human', 'why', cause_detail,
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'needs_you' AND cause = 'review';
UPDATE jobs SET holds_json = json_array(json_object('kind', 'approval', 'lift', 'human', 'why', 'waiting for your approval',
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'queued' AND cause = 'approval';
UPDATE jobs SET holds_json = json_array(json_object('kind', 'approval_tool', 'lift', 'crowsnest', 'why', cause_detail,
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'needs_you' AND cause = 'approval-tool';
UPDATE jobs SET holds_json = json_array(json_object('kind', 'usage_limit', 'lift', 'crowsnest', 'why', cause_detail,
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE stuck = 1 AND cause = 'limit' AND state IN ('running', 'blocked', 'needs_you');
UPDATE jobs SET holds_json = json_array(json_object('kind', 'checking', 'lift', 'crowsnest', 'why', accept_detail,
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'in_review' AND accept_state = 'checking';
UPDATE jobs SET holds_json = json_array(json_object('kind', 'reviewing', 'lift', 'crowsnest', 'why', 'a reviewer looks at the change',
    'since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))
    WHERE state = 'in_review' AND accept_state != 'checking';

-- pending_feedback is a check failure's message to the agent that has not reached it yet (R6-C07):
-- written with the job's move back to running, typed only while the automation is not paused and
-- nothing holds the job, and cleared only once it was typed (or the agent called done again).
ALTER TABLE jobs ADD COLUMN pending_feedback TEXT NOT NULL DEFAULT '';

-- definition_changed lists the files the job's own diff changed that define its check (mise
-- configuration and tasks, a Makefile, package.json, CI files, a script the command names), so the
-- evidence says when the branch changed what "passing" means (R6-S4). Comma-separated; '' none.
ALTER TABLE job_checks ADD COLUMN definition_changed TEXT NOT NULL DEFAULT '';
