-- 0015: day thirty (DESIGN.md 4.17 to 4.21, 11): archived jobs, scoped API tokens and the other
-- crowsnest machines this one shows.
--
-- Archiving is its own table rather than a column on jobs, so the jobs row and every query that
-- reads it stay as they were: the board leaves out what is listed here, and a search finds it.
CREATE TABLE job_archive (
    job_id      INTEGER PRIMARY KEY,           -- jobs.id
    archived_at INTEGER NOT NULL               -- unix seconds
);

-- Scoped API tokens (cnk_…): only a SHA-256 of the token is kept. scope is 'read' (GET
-- /api/v1/summary and nothing else) in this round. A revoked token stays listed with revoked_at.
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    scope        TEXT    NOT NULL,
    hash         TEXT    NOT NULL UNIQUE,      -- hex SHA-256 of the whole token
    hint         TEXT    NOT NULL,             -- the token's last four characters, to tell them apart
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0
);

-- Other crowsnest instances shown in Machines. token is the read-only token minted on that
-- machine, kept as given (it has to be presented); a backup blanks it.
CREATE TABLE machines (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    url         TEXT    NOT NULL UNIQUE,
    token       TEXT    NOT NULL,
    created_at  INTEGER NOT NULL
);
