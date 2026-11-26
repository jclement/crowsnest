# crowsnest

One Go binary that starts a [herdr](https://herdr.dev) session and a Claude Code orchestrator in
it, and serves a mobile-friendly web app for running that session from a browser: chat with the
orchestrator, answer questions from agents, drive a terminal, and see every agent in the fleet.

> **Status: M5 (terminals).** crowsnest serves the login, the API health and auth endpoints,
> the event stream and the web app; it connects to (or starts) its herdr session and serves the
> fleet, agent screens, dialog key presses, and live terminals: the whole herdr session (its TUI)
> in the Terminal view, and any one pane from the Fleet view. The orchestrator, questions and
> talk mode arrive in later milestones; see [DESIGN.md](DESIGN.md) section 18 and
> [STATUS.md](STATUS.md).

A web terminal is remote code execution, so crowsnest fails closed: it binds to localhost by
default, refuses to start without a login (or an explicit `--no-auth`), and checks Host and
Origin on every request. [DESIGN.md](DESIGN.md) section 4 is the security model.

## Install

Releases are built by CI from `v*` tags: one tar.gz per platform (linux and darwin, amd64 and
arm64; no Windows), each holding the `crowsnest` binary with the web app embedded, this README
and the LICENSE, plus a `checksums.txt` (sha256). While the repository is private, download from
the [releases page](https://github.com/jclement/crowsnest/releases) signed in, or with `gh`:

```sh
gh release download --repo jclement/crowsnest --pattern "crowsnest_*_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" --pattern checksums.txt
shasum -a 256 --ignore-missing -c checksums.txt
tar xzf crowsnest_*.tar.gz crowsnest && mv crowsnest ~/.local/bin/
```

<!-- When the repository is public (DESIGN.md section 19 item 4):
brew install jclement/tap/crowsnest
-->

Release archives are not signed yet: cosign and the Homebrew tap are switched on when the
repository goes public (DESIGN.md section 19 item 4).

## Quick start

```sh
git clone git@github.com:jclement/crowsnest.git && cd crowsnest
mise run setup            # tools, Go modules, web dependencies (idempotent)
mise run build            # web app, then ./bin/crowsnest

CROWSNEST_PASSWORD='something long' ./bin/crowsnest --username jeff
# open http://localhost:2769
```

crowsnest needs [herdr](https://herdr.dev) on `PATH`. On start it connects to the herdr session
named by `--session` (default `crowsnest`) and, if nothing answers, starts
`herdr --session <name> server` detached, logging to
`~/.local/state/crowsnest/<session>/herdr-server.log`. It never stops that server: herdr and its
agents outlive crowsnest. `--demo` needs no herdr at all; it serves an in-memory fake fleet that
changes every few seconds, and its terminals are a built-in toy that echoes what you type (no
shell ever runs in demo mode).

**Terminals.** The Terminal view is the herdr TUI for the whole session (`herdr --session
<name>` in a PTY; closing the tab kills only that client, never the server or an agent). In the
Fleet view, an agent's Terminal tab, or any plain shell pane, opens just that pane through
herdr's pane-control stream, which fits a phone. One browser controls a pane at a time: opening
it elsewhere takes it over and the first browser says so and offers Reconnect. On phones a key
bar under the terminal supplies Esc, Tab, a sticky Ctrl, arrows, Enter, Paste and the font size.
Note that herdr's TUI shows every machine your herdr config names, remote ones included, so the
Terminal view reaches those too.

For development, `mise run dev` runs the Go server with `--no-auth --demo` on :2769 and Vite on
:5173 (open http://localhost:5173; Vite proxies `/api` to Go).

To work on the UI with no server at all, open http://localhost:5173/fleet?fixtures=1 on the Vite
dev server: every API call is answered in memory from `web/src/lib/fixtures.ts` (a fleet with
agents in every state and two dialogs), and a "fixtures" chip shows in the header. `?fixtures=0`
turns it off. Fixtures mode exists only in development builds; it is compiled out of
`mise run build`.

On a phone over Tailscale, bind the tailnet address, or keep localhost and use `tailscale serve`,
which also gives the HTTPS secure context the microphone needs later:

```sh
./bin/crowsnest --bind tailnet --username jeff --password-file ~/.config/crowsnest/password
```

## Commands

| Command | Does |
|---|---|
| `crowsnest` / `crowsnest serve` | Serve the web app (the default) |
| `crowsnest version [--json]` | Version, commit, build date, Go version, platform, a tagline |
| `crowsnest --version` | `crowsnest v1.2.3 (abc1234, 2026-01-15T10:30:00Z)` |
| `crowsnest health [--json]` | GET `/api/v1/health` on the configured address; exit 0 healthy, 1 not (Docker `HEALTHCHECK`) |

Exit codes: `0` success, `1` runtime error, `2` usage error.

## Configuration

Precedence: flag, then environment, then `~/.config/crowsnest/config.yaml` (keys are the flag
names without the leading `--`, e.g. `username: jeff`, `bind: [localhost, tailnet]`), then the
default. List values (`--bind`, `--allowed-host`) repeat as flags, are comma-separated in the
environment, and are a YAML list or string in the file. An unknown key in the file is an error.

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--username` | `CROWSNEST_USERNAME` | none | Login name |
| `--password` | `CROWSNEST_PASSWORD` | none | Login password. Prefer the env var or `--password-file`: a flag is visible in `ps` |
| `--password-file` | `CROWSNEST_PASSWORD_FILE` | none | File holding the password (first line) |
| `--no-auth` | `CROWSNEST_NO_AUTH` (`SUPPRESS_AUTH=true` also honoured) | `false` | No login. For a trusted localhost or tailnet only |
| `--bind` | `CROWSNEST_BIND` | `localhost` | `localhost`, `tailnet`, an interface name, an IP, or `ip:port`. Repeatable |
| `--port` | `CROWSNEST_PORT` | `2769` | Port for binds that do not name one (C-R-O-W on a keypad) |
| `--session` | `CROWSNEST_SESSION` | `crowsnest` | herdr session to create or connect to |
| `--dir` | `CROWSNEST_DIR` | `<data-dir>/orchestrator` | Orchestrator's working directory |
| `--model` | `CROWSNEST_MODEL` | Claude's default | Orchestrator model |
| `--permission-mode` | `CROWSNEST_PERMISSION_MODE` | `auto` | Orchestrator permission mode (`acceptEdits`, `auto`, `bypassPermissions`, `dontAsk`, `manual`, `plan`) |
| `--poke-interval` | `CROWSNEST_POKE_INTERVAL` | `15m` | Timer poke interval; `0` disables timer pokes |
| `--no-orchestrator` | `CROWSNEST_NO_ORCHESTRATOR` | `false` | Serve the UI over the session without starting Claude |
| `--allowed-host` | `CROWSNEST_ALLOWED_HOSTS` | derived | Extra Host/Origin name (reverse proxy, MagicDNS). Repeatable |
| `--tls-cert` | `CROWSNEST_TLS_CERT` | none | PEM certificate: serve HTTPS directly |
| `--tls-key` | `CROWSNEST_TLS_KEY` | none | PEM key for `--tls-cert` |
| `--elevenlabs-api-key` | `ELEVENLABS_API_KEY` | none | Enables talk mode |
| `--elevenlabs-voice` | `CROWSNEST_ELEVENLABS_VOICE` | a stock voice id | Voice for spoken replies |
| `--elevenlabs-url` | `CROWSNEST_ELEVENLABS_URL` | `https://api.elevenlabs.io` | ElevenLabs API base URL (tests point it at a fake) |
| `--notify-url` | `CROWSNEST_NOTIFY_URL` | none | POST a one-line notice here when something needs you (ntfy-style) |
| `--demo` | `CROWSNEST_DEMO` | `false` | In-memory fake herdr and scripted orchestrator |
| `--i-know-this-is-open` | `CROWSNEST_I_KNOW_THIS_IS_OPEN` | `false` | Allow `--no-auth` on a bind that is not loopback or tailnet |
| `--allow-default-session` | `CROWSNEST_ALLOW_DEFAULT_SESSION` | `false` | Allow `--session default` (your main herdr session) |
| `--data-dir` | `CROWSNEST_DATA_DIR` | `~/.local/share/crowsnest` | Where the database (`<session>.db`) lives |
| `--config` | `CROWSNEST_CONFIG` | `~/.config/crowsnest/config.yaml` | Config file to read |

`--web-dev` (`CROWSNEST_WEB_DEV`) is a hidden development flag used by `mise run dev`: the UI is
served by Vite, so the binary does not require embedded web assets.

`--session`, `--allow-default-session` and `--demo` take effect now. The settings for the
orchestrator, talk mode and notices are parsed and validated now and take effect in their
milestones.

### Startup refusals

Crowsnest refuses to start, and says what to pass instead, when:

- there are neither credentials nor `--no-auth` (or both are given);
- `--no-auth` is combined with a bind that is not loopback or a Tailscale address (including
  `0.0.0.0`), unless `--i-know-this-is-open` is also given;
- `--session default` is given without `--allow-default-session`;
- `--bind tailnet` finds no Tailscale address (100.64.0.0/10 or fd7a:115c:a1e0::/48); it never
  falls back to all interfaces.

## HTTP API

Routes under `/api/v1`. Responses are `{"data": …}` (lists add `"meta"`), errors
`{"error": {"message", "code"}}`. Every non-GET request needs a same-origin `Origin` and
`X-Crowsnest: 1`.

| Route | Auth | Purpose |
|---|---|---|
| `GET /health` | none | `{status, version, update_available}` |
| `POST /auth/login` | none | `{username, password}` → session cookie; `429` with `Retry-After` after 5 failures a minute per IP or 20 overall |
| `POST /auth/logout` | none | Deletes the session and clears the cookie |
| `GET /auth/me` | session | `{username, features: {auth, voice, demo, session, herdr_version}}` |
| `GET /events` | session | Server-sent events: `hello` on connect, `heartbeat` every 20 s, `fleet` (data: the new fleet) whenever it changes; later `message`, `question`, … |
| `GET /fleet` | session | The herdr session: workspaces → tabs → panes with their agents, a flat `agents` list, `counts`, `revision`; `503 UNAVAILABLE` when herdr does not answer |
| `GET /agents/{target}/screen` | session | One agent's (or pane's) visible screen: `text`, `ansi`, `screen_hash`, `dialog` (options, form, warnings), `draft`. `{target}` is an agent name or pane id |
| `POST /agents/{target}/keys` | session | `{keys: ["1"], screen_hash}` → sends the keys if the screen still has that hash, else `409 CONFLICT` and nothing is sent. Only `esc`/`ctrl+c` may omit the hash |
| `GET /term/tui` (WebSocket) | session | The herdr TUI in a PTY. Binary frames are terminal bytes both ways; text frames are JSON control: the browser sends `{"type":"resize","cols","rows"}` first (the process starts on it), the server ends with `{"type":"exit","reason"}` |
| `GET /term/pane/{pane_id}` (WebSocket) | session | One pane through `herdr terminal session control --takeover`, same protocol. `404` unless the pane is in the fleet |

## Tasks

| Task | Does |
|---|---|
| `mise run setup` | Install tools, Go modules and web dependencies. Idempotent |
| `mise run dev` | Go server (`--no-auth`, `--web-dev`, `--demo`) on :2769 and Vite on :5173. Long-running |
| `mise run build` | Build the web app, then `./bin/crowsnest` with version ldflags |
| `mise run test` | Go tests with `-race`, then Vitest (single run) |
| `mise run test:integration` | Go tests against real herdr in throwaway `crowsnest-test-*` sessions (created, stopped and deleted by the tests): the socket client, and the pane and TUI terminal bridges |
| `mise run lint` | `go vet`, golangci-lint, gofmt check, `tsc`, eslint. Read-only |
| `mise run fmt` | gofmt and eslint `--fix` |
| `mise run check` | lint + test: the gate before calling anything done |
| `mise run e2e` | Playwright suite (not built until M9; fails with a message) |
| `mise run e2e:real` | Builds, then Playwright against the real binary (`web/e2e/real`): the Fleet view on `--demo` with no fixtures, and the terminals on a throwaway `crowsnest-test-*` herdr session. Each spec starts and stops its own instance on a random localhost port |
| `mise run screenshots` | Playwright with fixtures: Fleet screenshots at 375×812 and 1440×900, light and dark, into `web/screenshots/`, plus the 375 px checks (no sideways scroll, 44 px targets) |
| `mise run snapshot` | GoReleaser snapshot into `dist/`: the four archives and `checksums.txt`, nothing published |
| `mise run release` | Interactive tag bump and push (Jeff's step) |
| `mise run dev:reset` | Remove build output, web dist, Vite cache and the dev database |

## Releasing

`mise run release` is the only way to cut a release, and it is Jeff's to run. It is interactive:
it reads the latest tag, offers the next patch, minor or major version, creates an annotated tag
and asks before pushing it. The pushed `v*` tag starts `.github/workflows/release.yml`, which runs
`mise run setup` and `mise run check`, then GoReleaser (`.goreleaser.yml`) builds the archives and
checksums and attaches them to a GitHub release (a `-rc.N` style tag becomes a prerelease). The
workflow uses only `GITHUB_TOKEN`. Never build release artifacts by hand or tag around a broken
pipeline: fix the pipeline.

`mise run snapshot` runs the same GoReleaser config locally with `--snapshot` (no tag, nothing
published) to prove the packaging; the archives land in `dist/`, which is gitignored.

## Files

| Path | Holds |
|---|---|
| `~/.config/crowsnest/config.yaml` | Optional config file |
| `~/.local/share/crowsnest/<session>.db` | SQLite: login sessions (hashed) and settings |
| `~/.local/state/crowsnest/<session>/herdr-server.log` | Output of a herdr server crowsnest started |

## License

MIT. (C) 2026 Jeff Clement. See [LICENSE](LICENSE).
