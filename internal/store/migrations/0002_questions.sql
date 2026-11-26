-- 0002: questions agents ask the human (DESIGN.md sections 8.1 and 11).
--
-- A question is shown in the Inbox until it is answered, expires or is cancelled. The JSON
-- columns hold exactly what the agent sent (validated before insert), so the row can be served
-- back without a join. Times are unix seconds.
--
-- Delivery bookkeeping (section 6.3, "Answers come back as messages"): when nobody is waiting on
-- the long-poll, the settled answer is typed into the asker's pane. delivery_claimed_at is the
-- write-ahead mark set just before typing, so a crash between the mark and the keystrokes leaves a
-- claimed-but-undelivered row that is never retried: at most once, by construction.
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
CREATE INDEX questions_status ON questions (status, created_at);
CREATE INDEX questions_pending_delivery ON questions (delivery, delivered_at);
