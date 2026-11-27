-- 0019: a diff at any time, with viewed tracking (DESIGN.md 6.20).
--
-- diff_marks is a reviewer's "Viewed" tick on one file of a diff. It is tied to the file's
-- content: hash is the file's diff hash (both blobs, both paths, both modes, landing.FileHash), so
-- when the file changes again the stored hash no longer matches and the mark no longer counts;
-- old_blob and new_blob are what was seen, for the "since you viewed it" comparison.
-- subject is j_<n> (a job's checkout), project:<id> (a project's main checkout) or orchestrator;
-- scope is the comparison the mark was made in (all, committed, uncommitted). One human today,
-- but keyed by reviewer so a second could be added. A job's marks go when it lands, is
-- discarded or is deleted.
CREATE TABLE diff_marks (
    subject   TEXT    NOT NULL,
    reviewer  TEXT    NOT NULL DEFAULT 'you',
    scope     TEXT    NOT NULL,
    path      TEXT    NOT NULL,
    hash      TEXT    NOT NULL,
    old_blob  TEXT    NOT NULL DEFAULT '',
    new_blob  TEXT    NOT NULL DEFAULT '',
    viewed_at INTEGER NOT NULL,                -- unix seconds
    PRIMARY KEY (subject, reviewer, scope, path)
);
