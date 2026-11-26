-- 0001: login sessions and the key-value table (DESIGN.md section 10).
-- Questions and the journal arrive with their own milestones as later migrations.

-- One row per browser login. The session id itself is never stored, only its SHA-256, so a
-- copy of this database cannot be replayed as a cookie. cred_digest ties the session to the
-- credentials it was created under: change the password and every old row stops matching.
CREATE TABLE auth_sessions (
    id_hash      TEXT    PRIMARY KEY,
    cred_digest  TEXT    NOT NULL,
    created_at   INTEGER NOT NULL, -- unix seconds
    last_seen_at INTEGER NOT NULL, -- unix seconds
    expires_at   INTEGER NOT NULL, -- unix seconds; sliding, pushed forward on use
    user_agent   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX auth_sessions_expires_at ON auth_sessions (expires_at);

-- Small settings and bookkeeping: the orchestrator's session id, poke state, preferences.
CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
