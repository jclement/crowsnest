-- 0010: attachments (DESIGN.md 8.6). Files the human pastes, drops or picks in the web app, and
-- files agents attach to a question or a finished job. The bytes live on disk under the data
-- directory, named by their SHA-256 (so two uploads of one screenshot are one file); this table
-- says what each upload is and who it belongs to. Never a path a client supplied.
CREATE TABLE attachments (
    id           TEXT    PRIMARY KEY,           -- a_<24 hex>, random
    name         TEXT    NOT NULL,              -- display name, cleaned (never used as a path)
    type         TEXT    NOT NULL,              -- sniffed media type, never the client's
    size         INTEGER NOT NULL,              -- bytes
    sha256       TEXT    NOT NULL,              -- hex; also the blob's file name
    has_thumb    INTEGER NOT NULL DEFAULT 0,    -- a thumbnail was made (images)
    origin       TEXT    NOT NULL,              -- 'human' (browser upload) or 'agent' (ask, done)
    origin_pane  TEXT    NOT NULL DEFAULT '',   -- the agent's pane, for origin 'agent'
    -- Who it belongs to. '' while an upload waits in a composer. 'job' (j_N), 'orchestrator',
    -- 'pane' (a pane id: an agent crowsnest did not start) and 'question' (q_N: an agent's).
    owner_kind   TEXT    NOT NULL DEFAULT '',
    owner_id     TEXT    NOT NULL DEFAULT '',
    -- What it was for: 'brief', 'message', 'prompt', 'answer' (copied into the asker's inbox for
    -- question ref), 'ask', 'result'.
    role         TEXT    NOT NULL DEFAULT '',
    ref          TEXT    NOT NULL DEFAULT '',   -- e.g. the question an answer's file answered
    path         TEXT    NOT NULL DEFAULT '',   -- the copy the agent reads, '' when none was made
    created_at   INTEGER NOT NULL,              -- unix seconds
    attached_at  INTEGER NOT NULL DEFAULT 0,    -- when it got its owner
    released_at  INTEGER NOT NULL DEFAULT 0     -- its owner settled: first in line for cleanup
);
CREATE INDEX attachments_owner ON attachments(owner_kind, owner_id);
CREATE INDEX attachments_sha ON attachments(sha256);
CREATE INDEX attachments_created ON attachments(created_at);
