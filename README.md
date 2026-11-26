# crowsnest

One Go binary that starts a [herdr](https://herdr.dev) session and a Claude Code orchestrator in
it, and serves a mobile-friendly web app for running that session from a browser: chat with the
orchestrator, answer questions from agents, drive a terminal, and see every agent in the fleet.

> **Status: M6 (questions and the agent API), M5 (terminals) and M4 (orchestrator and Chat).**
> crowsnest serves the login, the API, the event stream and the web app; it connects to (or
> starts) its herdr session and serves the fleet, agent screens, dialog key presses, and live
> terminals. It keeps the session's orchestrator running and serves the Chat view and every Claude
> agent's conversation, read live from Claude's transcripts. Agents ask the human questions over a
> unix socket (`crowsnest ask`, or the `ask` MCP tool the orchestrator is started with); the Inbox
> shows them beside terminal dialogs, and an answer is typed back into the asker's pane. Jobs,
> pokes and talk mode arrive in later milestones; see [DESIGN.md](DESIGN.md) section 18 and
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
changes every few seconds; its terminals are a built-in toy that echoes what you type (no
shell ever runs in demo mode), and a scripted orchestrator answers in Chat after a short
"working" period (its transcripts are written under the state directory, never `~/.claude`).

Unless `--no-orchestrator` is given, crowsnest then makes sure a Claude agent named
`orchestrator` runs in the session: it adopts a live one, or starts `claude` in a workspace
labelled `orchestrator` (cwd `--dir`), resuming the stored conversation when its transcript
exists. The first start in a new directory usually stops at Claude's folder-trust prompt; Chat
then shows "Needs terminal" until it is answered in the pane. Conversations are read from
Claude's JSONL transcripts in `$CLAUDE_CONFIG_DIR` (default `~/.claude`).

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

## Container (devbox)

`ghcr.io/jclement/crowsnest` is a ready-made devbox: crowsnest, herdr and mise in one container
(linux amd64 and arm64), with Claude Code and node installed by mise into a volume on first
start. A browser gets the herdr session, a terminal and (once the orchestrator lands) Claude.

```sh
cp deploy/.env.sample deploy/.env          # set CROWSNEST_USERNAME and CROWSNEST_PASSWORD
docker compose -f deploy/compose.yaml up -d
docker compose -f deploy/compose.yaml logs -f crowsnest   # first start: watch mise install
open http://127.0.0.1:2769
```

While the repository is private its GHCR package is private too: `docker login ghcr.io` (a
token with `read:packages`, e.g. `gh auth token | docker login ghcr.io -u <you> --password-stdin`)
before the first pull.

Or without compose:

```sh
docker run -d --name crowsnest -p 127.0.0.1:2769:2769 \
  -e CROWSNEST_USERNAME=jeff -e CROWSNEST_PASSWORD_FILE=/run/secrets/pw \
  -v "$PWD/pw:/run/secrets/pw:ro" -v crowsnest-home:/home/crow ghcr.io/jclement/crowsnest
```

**The one manual step is signing Claude in.** Nothing personal is in the image, Claude's login
included: after the first start, log in to crowsnest, open **Terminal**, run `claude`, and
complete its login (it prints a URL to open on any device). The login is kept in the volume.

What is in it, and where things live:

| | |
|---|---|
| Base | `debian:trixie-slim`, `tini` as PID 1, user `crow` (uid 1000), `HOME=/home/crow` |
| Baked in | crowsnest, herdr (pinned, checksummed), mise (pinned, checksummed), git, gh, openssh-client, curl, build-essential, ripgrep, jq, less, en_US.UTF-8 |
| Volume `/home/crow` | Claude's login (`~/.claude`), mise tools (`~/.local/share/mise`), herdr sessions, the crowsnest database, your projects. Keep it across upgrades |
| Port | 2769, published on 127.0.0.1 only by `deploy/compose.yaml` |
| Health | `HEALTHCHECK` runs `crowsnest health` |

The entrypoint (`deploy/entrypoint.sh`) on every start:

1. **Refuses to start without credentials**: `CROWSNEST_USERNAME` plus `CROWSNEST_PASSWORD` or
   `CROWSNEST_PASSWORD_FILE`, or else both `CROWSNEST_NO_AUTH=true` and
   `CROWSNEST_I_KNOW_THIS_IS_OPEN=true`. It exits 64 with a message saying which to set.
2. **On first start only** writes `~/.config/mise/config.toml` (node LTS, and Claude Code from
   mise's `claude` registry entry, the native binary) and a herdr config that skips herdr's
   first-run screen. Existing files are never overwritten; edit them to change the tool set.
3. **Runs `mise install`** and waits up to `CROWSNEST_MISE_WAIT` seconds (default 300) for it. A
   failure is logged with the last lines of `~/.local/state/crowsnest/mise-install.log` and a slow
   install carries on in the background; neither stops crowsnest from starting.
4. **Execs `crowsnest serve --bind 0.0.0.0`**, passing through any arguments given to the
   container. Every other setting is a `CROWSNEST_*` variable (see Configuration). Change the
   port with `CROWSNEST_PORT`, not `--port`, so the health check probes the same port.

Upgrading tools needs no rebuild: `mise upgrade` in the web terminal (Claude Code's own
auto-updater is off in the image for that reason). Upgrading crowsnest, herdr or mise is a new
image; the volume carries over.

**Blast radius.** Whoever logs in has a shell as `crow`, and so does every agent in the
container. They reach what is mounted and what the network reaches, nothing else on the host.
That is the point: mount only what you would hand an agent (never the Docker socket or your real
home), and put a tunnel or tailnet in front rather than publishing the port
(`deploy/compose.yaml` has commented cloudflared and tailscale sidecars).

`mise run image` builds `crowsnest:dev` locally for this machine's architecture.
`mise run publish` builds amd64 and arm64 and pushes `ghcr.io/jclement/crowsnest:<version>` and
`:latest` (token from `GHCR_TOKEN`, or `gh auth token` with `write:packages`); the release
workflow does the same on every `v*` tag.

## Commands

| Command | Does |
|---|---|
| `crowsnest` / `crowsnest serve` | Serve the web app (the default) |
| `crowsnest version [--json]` | Version, commit, build date, Go version, platform, a tagline |
| `crowsnest --version` | `crowsnest v1.2.3 (abc1234, 2026-01-15T10:30:00Z)` |
| `crowsnest health [--json]` | GET `/api/v1/health` on the configured address; exit 0 healthy, 1 not (Docker `HEALTHCHECK`) |

Exit codes: `0` success, `1` runtime error, `2` usage error.

### Agent side

These run inside agent panes and talk to the server over the unix socket named by
`CROWSNEST_SOCKET` (crowsnest sets it, and puts its own directory first on `PATH`, in the
orchestrator's pane). The asking pane is taken from `HERDR_PANE_ID` (where answers are typed back)
and the job from `CROWSNEST_JOB` (M7). Without `CROWSNEST_SOCKET`, or with no server answering,
they fail with exit 1 and say so.

| Command | Does |
|---|---|
| `crowsnest ask --file q.json\|- [--wait] [--timeout 12h]` | Post a question (DESIGN.md 8.1 shape: `title`, `body_md`, `kind` confirm/choice/ack/manual/notice, `multi`, `options`, `recommend`, `context`, `urgency`, `expires_in_hours`, `files`). Without `--wait` it prints the id (`q_7`) and returns; the answer is typed into the pane later as `[crowsnest answer q_7] <title> → <answer> (note: …)`. With `--wait` it blocks and prints `{id, status, answer_value, answer_note}`: exit `0` answered, `3` expired or cancelled, `1` error |
| `crowsnest notify "text" [--urgency low\|normal\|blocking]` | A notice (no answer needed); the first line is its title. Prints the id |
| `crowsnest say "sentence"` | Ask for one sentence to be spoken in talk mode (M11 speaks it; now it is published as a `say` event) |
| `crowsnest mcp` | A stdio MCP server exposing `ask` and `say` (M7 adds the job tools), proxied to the socket. Claude starts it from the `mcp.json` crowsnest writes |

The socket is `$XDG_RUNTIME_DIR/crowsnest/<session>.sock` when `XDG_RUNTIME_DIR` is set, else
`~/.local/state/crowsnest/<session>/agent.sock` (or, when that path is too long for a unix socket,
`crowsnest-<uid>/<hash>.sock` under the OS temporary directory); `serve` logs it at start. Its
directory is `0700` and must belong to you; the socket is `0600`.
Its routes are Meat Prompt's (`POST /api/v1/questions`, `GET /api/v1/questions/{id}?wait=90`,
`GET /api/v1/questions?status=`, `DELETE /api/v1/questions/{id}`) plus `POST /api/v1/say` and
`GET /api/v1/health`.

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

`--session`, `--allow-default-session`, `--demo`, `--dir`, `--model`, `--permission-mode`,
`--no-orchestrator` and `--notify-url` (one plain-text line, the question's id and scrubbed title,
POSTed for each `blocking` question) take effect now. `--poke-interval` (M8) and talk mode are
parsed and validated now and take effect in their milestones.

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
| `GET /events` | session | Server-sent events: `hello` on connect, `heartbeat` every 20 s, `fleet` (data: the new fleet) whenever it changes, `message` (`{pane_id, cursor}`) when a followed conversation changes, `orchestrator` (its status) on every state change, `question` (`{id, status, title, kind, urgency, change}`) when a question is asked, settled or delivered, `say` (`{text, pane_id, at}`); later `job`, … |
| `GET /fleet` | session | The herdr session: workspaces → tabs → panes with their agents, a flat `agents` list, `counts`, `revision`; `503 UNAVAILABLE` when herdr does not answer |
| `GET /agents/{target}/screen` | session | One agent's (or pane's) visible screen: `text`, `ansi`, `screen_hash`, `dialog` (options, form, warnings), `draft`. `{target}` is an agent name or pane id |
| `POST /agents/{target}/keys` | session | `{keys: ["1"], screen_hash}` → sends the keys if the screen still has that hash, else `409 CONFLICT` and nothing is sent. Only `esc`/`ctrl+c` may omit the hash |
| `GET /agents/{target}/messages?after=&limit=` | session | A page of the agent's conversation from its Claude transcript: `{source, session_id, cursor, reset, earlier, more, messages}`. Without `after` (or with a stale cursor) the newest `limit` (200) messages and `reset: true`; with it, only what changed since. No transcript: one `pane` message with the pane's recent text |
| `POST /agents/{target}/prompt` | session | `{text, force_over_draft}` → `{pane_id, status, queued, started, note}`. `409 BLOCKED` (with the dialog in `error.detail`) when a dialog is up, `409 DRAFT` (with the draft) when text was typed at the terminal; nothing is typed in either case |
| `GET /orchestrator` | session | `{state, pane_id, session_id, dir, detail, restarts}`; state is `idle`, `working`, `blocked`, `needs_terminal`, `starting`, `restarting`, `failed` or `disabled` |
| `POST /orchestrator/restart`, `POST /orchestrator/new` | session | Restart Claude on the same conversation (`--resume`), or with a fresh one |
| `GET /questions?status=` | session | Questions with a status (`open`, `answered`, `expired`, `cancelled`, a comma-separated list, or `all`), open first; file contents left out (`file_count` says how many) |
| `GET /questions/{id}` | session | One question with its `files` |
| `POST /questions/{id}/answer` | session | `{answer_value, answer_note}`: a key, an array of keys for a multi-select, nothing for ack/notice; a `manual` answer is the note. `409 CONFLICT` once settled |
| `DELETE /questions/{id}` | session | Cancel an open question; the asker is told |
| `GET /inbox/summary` | session | `{open, blocking}` for the Inbox badge |
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
| `mise run test:claude` | Real-Claude smoke (a few cents): a real orchestrator on haiku in a throwaway session answers one prompt through the HTTP API, then Restart and New conversation; and it calls the `ask` MCP tool, the question is answered through the browser API, and the answer arrives in its transcript |
| `mise run lint` | `go vet`, golangci-lint, gofmt check, `tsc`, eslint. Read-only |
| `mise run fmt` | gofmt and eslint `--fix` |
| `mise run check` | lint + test: the gate before calling anything done |
| `mise run e2e` | Playwright suite (not built until M9; fails with a message) |
| `mise run e2e:real` | Builds, then Playwright against the real binary (`web/e2e/real`): the Fleet view on `--demo` with no fixtures, and the terminals on a throwaway `crowsnest-test-*` herdr session. Each spec starts and stops its own instance on a random localhost port |
| `mise run screenshots` | Playwright with fixtures: Fleet screenshots at 375×812 and 1440×900, light and dark, into `web/screenshots/`, plus the 375 px checks (no sideways scroll, 44 px targets) |
| `mise run screenshots:inbox` | Builds, then Playwright against `./bin/crowsnest --demo`: the Inbox list and a question (mermaid and a diff) at 375×812 and 1440×900, light and dark, a multi-select, the 375 px checks, and an answer round trip |
| `mise run screenshots:chat` | Builds, then Playwright against `./bin/crowsnest --demo`: a real prompt round trip in Chat, screenshots at 375×812 and 1440×900, light and dark |
| `mise run snapshot` | GoReleaser snapshot into `dist/`: the four archives and `checksums.txt`, nothing published |
| `mise run image` | Build the devbox image (`deploy/Dockerfile`) for this machine's architecture as `crowsnest:dev`. Needs Docker |
| `mise run publish` | Build the devbox image for amd64 and arm64 and push `ghcr.io/jclement/crowsnest:<version>` and `:latest`. Jeff's step |
| `mise run release` | Interactive tag bump and push (Jeff's step) |
| `mise run dev:reset` | Remove build output, web dist, Vite cache and the dev database |

## Releasing

`mise run release` is the only way to cut a release, and it is Jeff's to run. It is interactive:
it reads the latest tag, offers the next patch, minor or major version, creates an annotated tag
and asks before pushing it. The pushed `v*` tag starts `.github/workflows/release.yml`, which runs
`mise run setup` and `mise run check`, then GoReleaser (`.goreleaser.yml`) builds the archives and
checksums and attaches them to a GitHub release (a `-rc.N` style tag becomes a prerelease). When
that succeeds, its `image` job builds the devbox image for amd64 and arm64 and pushes
`ghcr.io/jclement/crowsnest:<tag>` (and `:latest`, unless the tag is a prerelease). The
workflow uses only `GITHUB_TOKEN` (`packages: write` for the image job). Never build release artifacts by hand or tag around a broken
pipeline: fix the pipeline.

`mise run snapshot` runs the same GoReleaser config locally with `--snapshot` (no tag, nothing
published) to prove the packaging; the archives land in `dist/`, which is gitignored.

## Files

| Path | Holds |
|---|---|
| `~/.config/crowsnest/config.yaml` | Optional config file |
| `~/.local/share/crowsnest/<session>.db` | SQLite: login sessions (hashed), settings and questions |
| `~/.local/state/crowsnest/<session>/herdr-server.log` | Output of a herdr server crowsnest started |
| `~/.local/state/crowsnest/<session>/CROWSNEST.md` | The orchestrator's instructions (`--append-system-prompt-file`), rewritten from the embedded template on every start |
| `~/.local/state/crowsnest/<session>/claude-settings.json` | The orchestrator's `--settings` file: `permissions.allow: ["mcp__crowsnest__*"]` (M7 adds hooks) |
| `~/.local/state/crowsnest/<session>/mcp.json` | The orchestrator's `--mcp-config`: the `crowsnest mcp` stdio server and its `CROWSNEST_SOCKET` |
| `~/.local/state/crowsnest/<session>/agent.sock` | The agent API socket (when `XDG_RUNTIME_DIR` is unset) |
| `~/.local/state/crowsnest/<session>/demo-claude/` | `--demo` only: the demo agents' transcripts |
| `~/.local/state/crowsnest/mise-install.log` | Devbox image only: the entrypoint's last `mise install` |

## License

MIT. (C) 2026 Jeff Clement. See [LICENSE](LICENSE).
