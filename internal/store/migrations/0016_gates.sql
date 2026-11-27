-- 0016: the human gates must mean something (R5).
--
-- land_token binds a land request to the one question that approves it (R5-C01, R5-C09): the
-- request is reserved with a fresh random token under the jobs lock before its question exists,
-- the question carries the same token in its context (a key agents may not set), and the answer
-- runs the land only while the job still holds that token. A second request is refused while one
-- is reserved, and an answer to any other question, stale or forged, matches nothing.
ALTER TABLE jobs ADD COLUMN land_token TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN land_requested_at INTEGER NOT NULL DEFAULT 0; -- unix seconds the request was reserved
CREATE INDEX jobs_land_request ON jobs (land_request) WHERE land_request != '';

-- task_reviews records the human's decision on a scheduled task file that starts a job or
-- reaches a lead (R5-S3). Agents can write files, so such a task runs only once the human has
-- approved this exact content: a changed file is a new hash and is asked about again.
-- state is 'pending' (a question is open), 'approved' or 'denied'.
CREATE TABLE task_reviews (
    path        TEXT    NOT NULL,              -- the task file, absolute
    hash        TEXT    NOT NULL,              -- hex SHA-256 of the file's bytes
    state       TEXT    NOT NULL,
    question_id INTEGER NOT NULL DEFAULT 0,    -- the gate question, 0 before it is asked
    at          INTEGER NOT NULL,              -- unix seconds of the last change
    PRIMARY KEY (path, hash)
);
