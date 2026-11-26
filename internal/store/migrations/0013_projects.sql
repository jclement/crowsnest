-- 0013: projects, instruction layers, schedules made in the app, and what each agent loaded
-- (DESIGN.md section 21). Times are unix seconds; 0 means unset.

-- A project is a repository's main checkout with its own defaults and, optionally, a standing
-- lead agent. Projects are archived, never deleted, so a job's project_id always names a row.
CREATE TABLE projects (
    id               INTEGER PRIMARY KEY,
    slug             TEXT    NOT NULL UNIQUE,        -- [a-z0-9-], names the lead agent (lead-<slug>)
    name             TEXT    NOT NULL,
    dir              TEXT    NOT NULL,               -- absolute and cleaned, as the human gave it
    root_dir         TEXT    NOT NULL,               -- dir with its links resolved: what jobs are matched against
    description      TEXT    NOT NULL DEFAULT '',
    default_role     TEXT    NOT NULL DEFAULT '',
    default_engine   TEXT    NOT NULL DEFAULT '',
    default_model    TEXT    NOT NULL DEFAULT '',
    default_isolate  INTEGER NOT NULL DEFAULT -1,    -- -1 the role's default, 0 no, 1 yes
    permission_mode  TEXT    NOT NULL DEFAULT '',    -- '' the server's --permission-mode
    lead_start       TEXT    NOT NULL DEFAULT 'manual', -- manual | open | job: when the lead starts by itself
    archived_at      INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);
-- Two live projects never share a directory (a job's directory must name one project).
CREATE UNIQUE INDEX projects_live_root ON projects(root_dir) WHERE archived_at = 0;

ALTER TABLE jobs ADD COLUMN project_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX jobs_project_state ON jobs(project_id, state);

-- Instruction layers crowsnest injects through the prompt files it writes: scope 'global'
-- (key ''), 'role' (key the role name) or 'project' (key the project id). A row exists only
-- while the human's text is in effect; resetting a role to its built-in deletes the row and
-- records a revision with deleted = 1.
CREATE TABLE instruction_layers (
    scope       TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    body_md     TEXT    NOT NULL,
    revision    INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (scope, key)
);
CREATE TABLE instruction_revisions (
    id          INTEGER PRIMARY KEY,
    scope       TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    revision    INTEGER NOT NULL,
    body_md     TEXT    NOT NULL,
    deleted     INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    UNIQUE (scope, key, revision)
);

-- What an agent was started with (crowsnest's own layers, with their revisions and a hash of
-- the composed text), one row per subject ('j_12', 'orchestrator', 'lead:<slug>'), replaced at
-- every start: the "instructions changed since start" check compares against it.
CREATE TABLE agent_layers (
    subject      TEXT    PRIMARY KEY,
    started_at   INTEGER NOT NULL,
    hash         TEXT    NOT NULL,
    layers_json  TEXT    NOT NULL
);

-- The instruction files Claude reported loading (the InstructionsLoaded hook), per subject and
-- session. Paths only: crowsnest never serves these files' contents (DESIGN.md 21.3).
CREATE TABLE agent_instructions (
    id           INTEGER PRIMARY KEY,
    subject      TEXT    NOT NULL,
    session_id   TEXT    NOT NULL DEFAULT '',
    at           INTEGER NOT NULL,
    path         TEXT    NOT NULL,
    memory_type  TEXT    NOT NULL DEFAULT '',
    load_reason  TEXT    NOT NULL DEFAULT '',
    size         INTEGER NOT NULL DEFAULT 0,
    UNIQUE (subject, session_id, path)
);
CREATE INDEX agent_instructions_subject ON agent_instructions(subject, id);

-- Schedules made in the app, beside task files. The name space is shared with task files and
-- a file of the same name wins (the row is flagged, never run). The on/off switch and the
-- stamps stay in kv schedule.state for both kinds, so the due rules have one input.
CREATE TABLE schedules (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    every       TEXT    NOT NULL,                 -- the task-file grammar: 15m, 4h, 1d, poke, change
    after       TEXT    NOT NULL DEFAULT '',      -- HH:MM local, or ''
    run_as      TEXT    NOT NULL DEFAULT 'poke', -- poke (to a standing agent) | job
    project_id  INTEGER NOT NULL DEFAULT 0,       -- poke: 0 the orchestrator, else that project's lead; job: the job's project
    body_md     TEXT    NOT NULL,                 -- the instruction (poke) or the brief (job)
    dir         TEXT    NOT NULL DEFAULT '',
    role        TEXT    NOT NULL DEFAULT '',
    engine      TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Run history of every scheduled task (file or app), newest kept, pruned per task.
CREATE TABLE schedule_runs (
    id       INTEGER PRIMARY KEY,
    name     TEXT    NOT NULL,
    at       INTEGER NOT NULL,
    outcome  TEXT    NOT NULL,
    job_id   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX schedule_runs_name ON schedule_runs(name, id);
