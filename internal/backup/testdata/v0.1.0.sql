-- A crowsnest v0.1.0 database (schema 1-9, the migration files read from the v0.1.0 tag) with a
-- day's rows, for TestUpgradeFromTheReleasedV010Database. Regenerate: apply the tag's migrations
-- and the rows with sqlite3, then .dump (see the test's comment).
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE auth_sessions (
    id_hash      TEXT    PRIMARY KEY,
    cred_digest  TEXT    NOT NULL,
    created_at   INTEGER NOT NULL, -- unix seconds
    last_seen_at INTEGER NOT NULL, -- unix seconds
    expires_at   INTEGER NOT NULL, -- unix seconds; sliding, pushed forward on use
    user_agent   TEXT    NOT NULL DEFAULT ''
, host TEXT NOT NULL DEFAULT '');
INSERT INTO auth_sessions VALUES('9c386948991b69afd3a326663f90d8a9a42214e8fefa6d8c38e2b737f0f78fd2','bfef02b56dcd9537e9837bb07ace4a5e7525dbcff17866ce3b76aca76a98645d',1795132800,1795139000,4102444800,'Mozilla/5.0 (iPhone)','127.0.0.1:2769');
CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO kv VALUES('auth.salt','00112233445566778899aabbccddeeff');
INSERT INTO kv VALUES('orchestrator.session','7d0c1f7e-1c2a-4b8a-9a52-2f9c3c0d7a11');
INSERT INTO kv VALUES('orchestrator.pane','w1:p1');
INSERT INTO kv VALUES('poke.last_at','1795139000');
CREATE TABLE questions (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT, -- shown as q_<id>
    title               TEXT    NOT NULL,
    body_md             TEXT    NOT NULL DEFAULT '',
    kind                TEXT    NOT NULL,                  -- confirm | choice | ack | manual | notice
    multi               INTEGER NOT NULL DEFAULT 0,
    options_json        TEXT    NOT NULL DEFAULT '[]',
    recommend           TEXT    NOT NULL DEFAULT '',
    context_json        TEXT    NOT NULL DEFAULT '{}',
    urgency             TEXT    NOT NULL DEFAULT 'normal', -- low | normal | blocking
    files_json          TEXT    NOT NULL DEFAULT '[]',
    status              TEXT    NOT NULL DEFAULT 'open',   -- open | answered | expired | cancelled
    answer_value_json   TEXT,                              -- a key, or an array of keys when multi
    answer_note         TEXT    NOT NULL DEFAULT '',
    source_pane         TEXT    NOT NULL DEFAULT '',       -- herdr pane of the asker, for delivery
    job_id              TEXT    NOT NULL DEFAULT '',
    wait                INTEGER NOT NULL DEFAULT 0,        -- the asker said it would block on the answer
    created_at          INTEGER NOT NULL,
    expires_at          INTEGER NOT NULL,
    settled_at          INTEGER,
    -- How the outcome reaches the asker: '' (not yet decided), 'type' (typed into its pane),
    -- 'poll' (it was waiting on the long-poll), 'none' (nothing to send: a notice, or the asker
    -- cancelled it itself, or there is no pane).
    delivery            TEXT    NOT NULL DEFAULT '',
    delivery_claimed_at INTEGER,
    delivered_at        INTEGER,
    delivery_error      TEXT    NOT NULL DEFAULT ''
);
INSERT INTO questions VALUES(1,'Which chart library?','Both are fine; one is smaller.','choice',0,'[{"key":"echarts","label":"ECharts"},{"key":"uplot","label":"uPlot","detail":"smaller"}]','uplot','{"repo":"site"}','blocking','[]','open',NULL,'','w3:p1','j_4',0,1795135500,1795394700,NULL,'',NULL,NULL,'');
INSERT INTO questions VALUES(2,'Keep the old flag name?','','confirm',0,'[{"key":"yes","label":"Yes"},{"key":"no","label":"No"}]','yes','{}','normal','[]','answered','"yes"','as an alias','w2:p3','j_3',0,1795134000,1795393200,1795134600,'type',NULL,1795134601,'');
INSERT INTO questions VALUES(3,'Daily digest at 18:00?','','confirm',0,'[{"key":"yes","label":"Yes"},{"key":"no","label":"No"}]','yes','{}','normal','[]','open',NULL,'','w1:p1','',0,1795138800,1795398000,NULL,'',NULL,NULL,'');
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
, last_stop_at INTEGER NOT NULL DEFAULT 0, lifecycle_op    TEXT    NOT NULL DEFAULT '', lifecycle_owner TEXT    NOT NULL DEFAULT '', lifecycle_at    INTEGER NOT NULL DEFAULT 0, lifecycle_gen   INTEGER NOT NULL DEFAULT 0, origin TEXT NOT NULL DEFAULT '', root_dir TEXT NOT NULL DEFAULT '');
INSERT INTO jobs VALUES(1,'Add the CSV export','Add a CSV export to the report page.','','builder','/Users/jeff/Work/app','job/1-add-the-csv-export','/Users/jeff/.herdr/worktrees/app/job-1',1,'claude','sonnet',0,'running',0,'','','',0,'writing the exporter','Edit: export.go','','[]','[]','','w2:p1','job-1','0b9d6a1e-0000-4000-8000-000000000001',1,1795133000,1795133010,0,1795139000,1795139000,0,0,0,0,0,'',0,0,0,'','',0,3,'','/Users/jeff/.herdr/worktrees/app/job-1');
INSERT INTO jobs VALUES(2,'Fix the flaky test','TestSync fails one run in ten.','','builder','/Users/jeff/Work/app','','',0,'claude','',0,'queued',0,'','','',0,'','','','[]','[]','','','','',0,1795134000,0,0,0,0,0,0,0,0,0,'',0,0,0,'','',0,0,'','');
INSERT INTO jobs VALUES(3,'Rename the config flag','Rename --colour to --color.','','builder','/Users/jeff/Work/app','job/3-rename-the-config-flag','/Users/jeff/.herdr/worktrees/app/job-3',1,'claude','',0,'done',0,'','','',0,'','','Renamed the flag; old name kept as an alias.','["go test ./... (ok)"]','[]','','w2:p3','job-3','0b9d6a1e-0000-4000-8000-000000000003',1,1795132900,1795132910,1795136000,1795136000,1795136000,1,0,0,0,0,'',0,0,1795136000,'','',0,5,'','/Users/jeff/.herdr/worktrees/app/job-3');
INSERT INTO jobs VALUES(4,'Choose the chart library','Pick a chart library for the dashboard.','','builder','/Users/jeff/Work/site','','/Users/jeff/Work/site',0,'claude','',0,'needs_you',0,'questions','waiting on q_1','',1,'asked which library','','','[]','[]','','w3:p1','job-4','0b9d6a1e-0000-4000-8000-000000000004',1,1795135000,1795135010,0,1795135500,1795135500,0,0,0,0,0,'',0,0,1795135500,'','',0,2,'','/Users/jeff/Work/site');
INSERT INTO jobs VALUES(5,'Upgrade the router','Move to the new router API.','','builder','/Users/jeff/Work/site','','/Users/jeff/Work/site',0,'codex','',0,'failed',0,'','','',0,'','','','[]','[]','The new API needs Node 24; the CI image has 22.','w3:p2','job-5','',1,1795133500,1795133510,1795134500,1795134500,1795134500,0,0,0,0,0,'',0,0,0,'','',0,4,'','/Users/jeff/Work/site');
INSERT INTO jobs VALUES(6,'Wait for the staging deploy','Check the deploy once staging is up.','','builder','/Users/jeff/Work/site','','/Users/jeff/Work/site',0,'claude','',0,'blocked',0,'','','staging is down',0,'waiting for staging','','','[]','[]','','w3:p3','job-6','0b9d6a1e-0000-4000-8000-000000000006',1,1795136500,1795136510,0,1795137000,1795137000,0,0,0,0,0,'',0,0,1795137000,'','',0,2,'','/Users/jeff/Work/site');
INSERT INTO jobs VALUES(7,'Summarise the inbox','Summarise what came in today.','','docs','','','',0,'claude','',0,'running',0,'','','',0,'reading the questions','','','[]','[]','','w1:p1','orchestrator','',1,1795138000,1795138010,0,1795139000,1795139000,0,0,0,0,0,'',0,0,0,'','',0,1,'','');
INSERT INTO jobs VALUES(8,'Write the export docs','Document the CSV export.','','builder','','','',0,'claude','',1,'queued',0,'','','',0,'','','','[]','[]','','','','',0,1795138500,0,0,0,0,0,0,0,0,0,'',0,0,0,'','',0,0,'agent','');
CREATE TABLE job_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    at          INTEGER NOT NULL,
    kind        TEXT    NOT NULL, -- state, progress, nudge, hook, restart, retry, question, message, launch, stuck, ...
    summary     TEXT    NOT NULL,
    detail_json TEXT    NOT NULL DEFAULT '{}'
);
INSERT INTO job_events VALUES(1,1,1795133010,'launch','Opened pane w2:p1 in /Users/jeff/.herdr/worktrees/app/job-1 (worktree, branch job/1-add-the-csv-export)','{"pane":"w2:p1"}');
INSERT INTO job_events VALUES(2,1,1795139000,'progress','writing the exporter','{}');
INSERT INTO job_events VALUES(3,3,1795136000,'state','done: Renamed the flag; old name kept as an alias.','{}');
INSERT INTO job_events VALUES(4,4,1795135500,'question','Asked q_1: Which chart library?','{"question":"q_1"}');
INSERT INTO job_events VALUES(5,5,1795134500,'state','failed: The new API needs Node 24; the CI image has 22.','{}');
INSERT INTO job_events VALUES(6,6,1795137000,'state','blocked: staging is down','{}');
CREATE TABLE journal (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          INTEGER NOT NULL,              -- unix seconds
    kind        TEXT    NOT NULL,              -- poke, skip, send, answer, restart, alert, backstop
    summary     TEXT    NOT NULL,
    detail_json TEXT    NOT NULL DEFAULT '{}'
);
INSERT INTO journal VALUES(1,1795133000,'send','You gave job j_1 from the web app','{}');
INSERT INTO journal VALUES(2,1795135000,'poke','Poked the orchestrator: 1 new question','{"reasons":["question"]}');
INSERT INTO journal VALUES(3,1795136000,'skip','Skipped a poke: the orchestrator is busy','{}');
INSERT INTO journal VALUES(4,1795137500,'alert','Job j_5 failed','{"job":"j_5"}');
CREATE TABLE push_subscriptions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint         TEXT    NOT NULL UNIQUE,
    p256dh           TEXT    NOT NULL,        -- the browser's public key, base64url
    auth             TEXT    NOT NULL,        -- the browser's auth secret, base64url
    user_agent       TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,        -- unix seconds
    last_success_at  INTEGER NOT NULL DEFAULT 0,
    last_failure_at  INTEGER NOT NULL DEFAULT 0,
    failures         INTEGER NOT NULL DEFAULT 0  -- consecutive failed sends (not 404/410, which prune)
);
INSERT INTO push_subscriptions VALUES(1,'https://push.example.invalid/sub/abc','BMx-fixture-key','fixture-auth','Mozilla/5.0 (iPhone)',1795133100,0,0,0);
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL DEFAULT (unixepoch()));
INSERT INTO schema_migrations VALUES(1,'0001_init.sql',1795132800);
INSERT INTO schema_migrations VALUES(2,'0002_questions.sql',1795132800);
INSERT INTO schema_migrations VALUES(3,'0003_session_host.sql',1795132800);
INSERT INTO schema_migrations VALUES(4,'0004_jobs.sql',1795132800);
INSERT INTO schema_migrations VALUES(5,'0005_journal.sql',1795132800);
INSERT INTO schema_migrations VALUES(6,'0006_job_lifecycle.sql',1795132800);
INSERT INTO schema_migrations VALUES(7,'0007_job_origin.sql',1795132800);
INSERT INTO schema_migrations VALUES(8,'0008_job_root.sql',1795132800);
INSERT INTO schema_migrations VALUES(9,'0009_push.sql',1795132800);
PRAGMA writable_schema=ON;
CREATE TABLE IF NOT EXISTS sqlite_sequence(name,seq);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('jobs',8);
INSERT INTO sqlite_sequence VALUES('job_events',6);
INSERT INTO sqlite_sequence VALUES('questions',3);
INSERT INTO sqlite_sequence VALUES('journal',4);
INSERT INTO sqlite_sequence VALUES('push_subscriptions',1);
CREATE INDEX auth_sessions_expires_at ON auth_sessions (expires_at);
CREATE INDEX questions_status ON questions (status, created_at);
CREATE INDEX questions_pending_delivery ON questions (delivery, delivered_at);
CREATE INDEX jobs_state ON jobs (state, created_at);
CREATE INDEX job_events_job ON job_events (job_id, id);
CREATE INDEX journal_kind ON journal (kind, id);
CREATE INDEX jobs_agent ON jobs (agent_name, state);
CREATE INDEX jobs_pane ON jobs (pane_id, state);
CREATE INDEX questions_job ON questions (job_id, status);
CREATE INDEX jobs_parent ON jobs (parent_id);
PRAGMA writable_schema=OFF;
COMMIT;
