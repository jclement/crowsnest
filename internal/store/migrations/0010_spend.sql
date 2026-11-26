-- 0010: spend per job and for the orchestrator (DESIGN.md 7.11). Token counts are read from the
-- agents' Claude transcripts and persisted here, so they survive a crowsnest restart and a
-- transcript that is rotated or deleted; costs are computed from them when read.
--
-- A subject is a job ('j_12') or the orchestrator's own conversation ('orchestrator').

-- Running totals per subject, plus the reader's state (file cursors, the session ids seen, the
-- recent message ids for deduplication) so a restart resumes exactly where reading stopped.
CREATE TABLE spend_totals (
    subject         TEXT    PRIMARY KEY,
    input           INTEGER NOT NULL DEFAULT 0,
    output          INTEGER NOT NULL DEFAULT 0,
    cache_write_5m  INTEGER NOT NULL DEFAULT 0,
    cache_write_1h  INTEGER NOT NULL DEFAULT 0,
    cache_read      INTEGER NOT NULL DEFAULT 0,
    turns           INTEGER NOT NULL DEFAULT 0,  -- model calls (one assistant API response each)
    tool_calls      INTEGER NOT NULL DEFAULT 0,
    first_at        INTEGER NOT NULL DEFAULT 0,  -- unix seconds of the first and last call seen
    last_at         INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0,
    state_json      TEXT    NOT NULL DEFAULT '{}'
);

-- Per local calendar day, subject and pricing key (model plus +fast/+long/+us): the rollup that
-- makes "today", "this week", the by-model breakdown and every cost cheap.
CREATE TABLE spend_daily (
    day             TEXT    NOT NULL,            -- YYYY-MM-DD in the server's local time
    subject         TEXT    NOT NULL,
    model           TEXT    NOT NULL,            -- pricing key
    input           INTEGER NOT NULL DEFAULT 0,
    output          INTEGER NOT NULL DEFAULT 0,
    cache_write_5m  INTEGER NOT NULL DEFAULT 0,
    cache_write_1h  INTEGER NOT NULL DEFAULT 0,
    cache_read      INTEGER NOT NULL DEFAULT 0,
    turns           INTEGER NOT NULL DEFAULT 0,
    tool_calls      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, subject, model)
);
CREATE INDEX spend_daily_subject ON spend_daily (subject);

-- The latest model calls per subject (at most a few hundred each), for the job's chart.
CREATE TABLE spend_turns (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject         TEXT    NOT NULL,
    message_id      TEXT    NOT NULL,            -- the call's message.id: a late, larger copy adds to its row
    at              INTEGER NOT NULL,            -- unix seconds
    model           TEXT    NOT NULL,            -- pricing key
    input           INTEGER NOT NULL DEFAULT 0,
    output          INTEGER NOT NULL DEFAULT 0,
    cache_write_5m  INTEGER NOT NULL DEFAULT 0,
    cache_write_1h  INTEGER NOT NULL DEFAULT 0,
    cache_read      INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX spend_turns_message ON spend_turns (subject, message_id);
CREATE INDEX spend_turns_subject ON spend_turns (subject, id);
