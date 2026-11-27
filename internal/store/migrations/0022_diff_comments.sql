-- 0022: line comments on a diff, sent back to the agent as one message (DESIGN.md 6.21).
--
-- diff_comments is one note the human left on a subject's diff: on a range of lines of one side
-- (old or new) of a file, or on the whole file (side 'file', lines 0). subject is j_<n>,
-- project:<id> or orchestrator, as diff_marks. The anchor is the file's content at the time:
-- hash is the file's diff hash (landing.FileHash) and old_blob / new_blob the two sides' blobs,
-- so a comment is "outdated" once the file's work-tree content (new_blob) is no longer what it
-- was written against; quote is the commented lines as git held them then (read by crowsnest from
-- the blob, never taken from the browser), kept so an outdated comment still shows what it was
-- about. scope is the comparison the comment was made in.
--
-- A comment is a draft until it is sent (sent_at, unix seconds; 0 = not sent); re-sending one puts
-- sent_at back to 0. dismissed_at hides an outdated comment for good. No threads, no resolution
-- state beyond draft / sent / outdated. A job's comments go when it lands, is discarded, torn down
-- or deleted, with its viewed marks.
CREATE TABLE diff_comments (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    subject      TEXT    NOT NULL,
    author       TEXT    NOT NULL DEFAULT 'you',
    scope        TEXT    NOT NULL DEFAULT 'all',
    path         TEXT    NOT NULL,
    side         TEXT    NOT NULL,                -- old | new | file
    line_start   INTEGER NOT NULL DEFAULT 0,
    line_end     INTEGER NOT NULL DEFAULT 0,
    quote        TEXT    NOT NULL DEFAULT '',
    body         TEXT    NOT NULL,
    hash         TEXT    NOT NULL,
    old_blob     TEXT    NOT NULL DEFAULT '',
    new_blob     TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    sent_at      INTEGER NOT NULL DEFAULT 0,
    dismissed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX diff_comments_subject ON diff_comments (subject, path);
