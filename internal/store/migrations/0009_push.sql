-- 0009: Web Push subscriptions (DESIGN.md 8.5). One row per browser that turned notifications on.
-- The endpoint and its keys come from the browser's PushManager; the endpoint URL is itself a
-- bearer capability (anyone holding it can push to that device), so it is never logged and never
-- returned whole to the browser: the API shows each device by id, user agent and host.
CREATE TABLE push_subscriptions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint         TEXT    NOT NULL UNIQUE,
    p256dh           TEXT    NOT NULL,        -- the browser's public key, base64url
    auth             TEXT    NOT NULL,        -- the browser's auth secret, base64url
    user_agent       TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,        -- unix seconds
    last_success_at  INTEGER NOT NULL DEFAULT 0,
    last_failure_at  INTEGER NOT NULL DEFAULT 0,
    failures         INTEGER NOT NULL DEFAULT 0  -- consecutive failed sends (not 404/410, which prune)
);
