-- 0005: the journal (DESIGN.md sections 7.4 and 11) and the Stop-hook mark the herdr-side
-- backstop reads (6.3).

-- One row per thing crowsnest did or decided on its own, or did for the human, that is worth
-- seeing later: pokes and skipped pokes (with the reason), prompts sent from the web app,
-- answers, orchestrator restarts, supervisor alerts. Newest first by id.
CREATE TABLE journal (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          INTEGER NOT NULL,              -- unix seconds
    kind        TEXT    NOT NULL,              -- poke, skip, send, answer, restart, alert, backstop
    summary     TEXT    NOT NULL,
    detail_json TEXT    NOT NULL DEFAULT '{}'
);
CREATE INDEX journal_kind ON journal (kind, id);

-- When the job's agent last ran its Stop hook. The backstop leaves a stop alone when its hook
-- ran, so one stop is never nudged twice (once by the hook, once by the backstop).
ALTER TABLE jobs ADD COLUMN last_stop_at INTEGER NOT NULL DEFAULT 0;
