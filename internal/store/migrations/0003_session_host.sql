-- 0003: bind each login session to the Host:port it was issued for (R1 M2).
-- Browsers share a host's cookies across ports, so another server on the same host name (a dev
-- server on localhost:5173, a container's published port) receives crowsnest's cookie. A
-- session presented with any other Host:port is refused. Rows from before this migration have
-- an empty host, match nothing, and simply mean signing in again once.
ALTER TABLE auth_sessions ADD COLUMN host TEXT NOT NULL DEFAULT '';
