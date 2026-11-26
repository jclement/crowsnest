# crowsnest

One Go binary that starts a [herdr](https://herdr.dev) session and a Claude Code orchestrator in
it, and serves a mobile-friendly web app for running that session from a browser: give an agent a
job and watch it, chat with the orchestrator, answer questions from agents, drive a terminal, and
see every agent in the fleet.

> **Status: M11 (talk mode), M7 (jobs), M6 (questions and the agent API), M5 (terminals) and M4 (orchestrator and
> Chat).** Jobs are the default view: one text box gives a job to a new agent (in a git worktree
> for a builder) or to the orchestrator; every job has a declared state, a heartbeat and a
> progress line, and Claude Code hooks refuse a turn that ends with nothing declared (DESIGN.md
> section 6).
> crowsnest serves the login, the API, the event stream and the web app; it connects to (or
> starts) its herdr session and serves the fleet, agent screens, dialog key presses, and live
> terminals. It keeps the session's orchestrator running and serves the Chat view and every Claude
> agent's conversation, read live from Claude's transcripts. Agents ask the human questions over a
> unix socket (`crowsnest ask`, or the `ask` MCP tool the orchestrator is started with); the Inbox
> shows them beside terminal dialogs, and an answer is typed back into the asker's pane. With an
> ElevenLabs key, Chat has talk mode: push to talk, and the orchestrator's replies spoken. The
> orchestrator is **poked** when something it should know about changes (another agent finishes
> or hits a dialog, a job finishes or gets stuck, a question is settled), only when it is idle with
> an empty input box, and never when nothing is due; job agents without hooks get a herdr-side
> backstop nudge. A Journal records pokes, skips, sends, answers, restarts and alerts. Budgets and
> review gates arrive in later milestones; see [DESIGN.md](DESIGN.md) section 18 and
> [STATUS.md](STATUS.md).

A web terminal is remote code execution, so crowsnest fails closed: it binds to localhost by
default, refuses to start without a login (or an explicit `--no-auth`), and checks Host and
Origin on every request. [DESIGN.md](DESIGN.md) section 4 is the security model.

## Install

```sh
brew install jclement/tap/crowsnest      # macOS and Linux; pulls in herdr from homebrew-core
```

Or download a release by hand. CI builds one tar.gz per platform from each `v*` tag (linux and
darwin, amd64 and arm64; no Windows), each holding the `crowsnest` binary with the web app
embedded, this README and the LICENSE, plus `checksums.txt` (sha256) and
`checksums.txt.sigstore.json`, a keyless [cosign](https://docs.sigstore.dev) signature over the
checksums made by this repository's release workflow. Verify the signature, then the archive:

```sh
VERSION=1.2.3   # the release you want, without the v: https://github.com/jclement/crowsnest/releases
OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
base=https://github.com/jclement/crowsnest/releases/download/v$VERSION
curl -fsSLO "$base/crowsnest_${VERSION}_${OS}_${ARCH}.tar.gz" -O "$base/checksums.txt" -O "$base/checksums.txt.sigstore.json"

cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/jclement/crowsnest/\.github/workflows/release\.yml@refs/tags/v'
shasum -a 256 --ignore-missing -c checksums.txt
tar xzf "crowsnest_${VERSION}_${OS}_${ARCH}.tar.gz" crowsnest && mv crowsnest ~/.local/bin/
```

crowsnest needs [herdr](https://herdr.dev) on `PATH` (`brew install herdr`).

**Updating.** A release build checks GitHub for a newer release once at start and then daily
(in the background, 10 s timeout, never blocking; not for a development build, `--demo` or a
container). When it finds one, `/api/v1/health` reports `update_available` and the footer links
to it. `crowsnest update` installs it over the running binary: it verifies the cosign signature
on `checksums.txt` (Sigstore's TUF root, a Fulcio certificate for exactly this repository's
release workflow on a `v*` tag, its Rekor entry) and the archive's sha256 before replacing
anything, and refuses a release without a signature. `crowsnest update --check` only reports.
Signed in to the web app, `POST /api/v1/admin/update` does the same and then exits, so run
crowsnest under something that restarts it (launchd `KeepAlive`, systemd `Restart=always`), or
start it again; herdr and every agent keep running across the restart. Installed with Homebrew:
`brew upgrade crowsnest`. In a container: pull a new image.

## Quick start

```sh
git clone git@github.com:jclement/crowsnest.git && cd crowsnest
mise run setup            # tools, Go modules, web dependencies (idempotent)
mise run build            # web app, then ./bin/crowsnest

./bin/crowsnest
# first start: open the setup link it prints and pick a username and password
```

**First run.** With no login configured, crowsnest starts locked: it serves nothing but a setup
page and prints a link to it with a one-time token, to its log and (on a terminal) plainly to
stderr:

```
  crowsnest is not set up yet. Open this link to choose a username and password:

    http://127.0.0.1:2769/setup#token=SZq05xlVK_nNdajE2A3RRbKvt1VqUVAhylMcoUf3-IM
```

Open it, choose a username, a password (at least 12 characters; a few unrelated words make a good
one) and the instance's name, and you are signed in. Only then does crowsnest start its herdr
session and the orchestrator. The login is stored in the database (an argon2id hash); change it
later in the account menu's **Account** sheet, or on the machine with `crowsnest reset-password`.
`crowsnest reset-setup` goes back to the first-run state. Until setup is done every other page
and API call is refused (`/api/v1/health` says `setup_required: true`); the token is new at every
start, works once, is never stored, and is replaced (and the new link logged) after ten wrong
tries. Anyone who can read crowsnest's log can complete setup, which is the point: that is you,
on your machine or as the container's operator.

For an unattended install, give the login instead and nothing is stored or asked:
`CROWSNEST_PASSWORD='something long' ./bin/crowsnest --username jeff` (or `--password-file`). A
login given that way always wins over a stored one, and the Account sheet shows it read-only.
`--no-auth` turns the login off entirely, with the rules under Startup refusals.

crowsnest needs [herdr](https://herdr.dev) on `PATH`. On start it connects to the herdr session
named by `--session` (default `crowsnest`) and, if nothing answers, starts
`herdr --session <name> server` detached, logging to
`~/.local/state/crowsnest/<session>/herdr-server.log`. It never stops that server: herdr and its
agents outlive crowsnest. `--demo` needs no herdr at all; it serves an in-memory fake fleet that
changes every few seconds; its terminals are a built-in toy that echoes what you type (no
shell ever runs in demo mode), and a scripted orchestrator answers in Chat after a short
"working" period. A demo keeps all its state (database, transcripts, the orchestrator's files)
in a fresh temporary directory that is removed when it exits: it never touches the real
database, state directory or `~/.claude`, and a login it hands out is no good anywhere else.

Unless `--no-orchestrator` is given, crowsnest then makes sure a Claude agent named
`orchestrator` runs in the session: it adopts a live one, or starts `claude` in a workspace
labelled `orchestrator` (cwd `--dir`), resuming the stored conversation when its transcript
exists. The first start in a new directory usually stops at Claude's folder-trust prompt; Chat
then shows "Needs terminal" until it is answered in the pane. Conversations are read from
Claude's JSONL transcripts in `$CLAUDE_CONFIG_DIR` (default `~/.claude`).

The instance has a name (`--name`, else the short hostname, such as `quark`): it is in the
header, the page title, push notification titles and `/auth/me`. The orchestrator is started
with Claude Code's Remote Control on, named `<name> crowsnest` (plus the session when it is not
`crowsnest`), so the same conversation is in the Claude app and claude.ai/code. A login that
cannot use Remote Control (an API key, a plan without it, an organisation policy) just starts
without it; the orchestrator's status says whether it came up. `--no-remote-control` turns it
off; `--job-remote-control` turns it on for job agents too (`<name> <job id> <title>`).

**Keeping the orchestrator small.** Every turn re-reads the whole conversation, and an
orchestrator poked all day grows past a few hundred thousand tokens. Once its context passes
`--compact-at` (150000 tokens) and it is idle with an empty input box, crowsnest types `/compact`
with a focus that keeps open jobs, unanswered questions, your standing instructions and the reply
contract; it does not compact again until the context has grown or a compaction took. With
`--fresh-at 04:00`, once a day after that time and after you have been quiet 20 minutes, it is
asked to write a hand-over (`handoff.md` in the state directory) and is then started on a new
conversation whose first context includes that file, its open questions and its jobs (the same
standing context is re-sent after every compaction). The Chat menu shows the context size with
a bar, and has Compact now. Both are journalled (`upkeep`).

**Attachments.** Paste a screenshot (Cmd+V), drop a file, or tap the paperclip (on a phone it
offers the photo library, the camera and files; a camera button opens the camera directly) in
Chat, the job box, a job's message box or a question's note. Files upload at once with progress,
can be retried or removed, and a send waits for them. A paste over 2,000 characters or 40 lines
becomes a `.txt` or `.md` file by default (a toast offers "Paste as text instead"), because long
pastes into a terminal are fragile. crowsnest copies each file into the agent's inbox under the
state directory (`orchestrator/attachments/`, `jobs/j_N/attachments/`) and types one line naming
it, `Attached: <path> (image/png, 240 KB)`; a job's files are listed in its brief file. Claude
reads the file, and sees an image it reads; the agents crowsnest starts may read their inbox
without a permission prompt. Agents attach files to a question or a result with
`ask(attachments=[…])`, `crowsnest ask --attach`, `done(attachments=[…])` or `crowsnest job done
--attach`, only from inside their own working or job directory. Pictures show as thumbnails that
open full size; other files are downloads. Only raster images, PDF and plain text are ever shown
in the browser; SVG, HTML and everything else are downloaded, never rendered (DESIGN.md 8.6).
Files go in `<data-dir>/attachments/`, at most `--attachment-max-mb` (25) each and
`--attachment-quota-mb` (1024) in all; past the quota the oldest files nobody needs any more (of
finished jobs, settled questions, week-old chats, abandoned uploads) are deleted.

**Terminals.** The Terminal view is the herdr TUI for the whole session (`herdr --session
<name>` in a PTY; closing the tab kills only that client, never the server or an agent). In the
Fleet view, an agent's Terminal tab, or any plain shell pane, opens just that pane through
herdr's pane-control stream, which fits a phone. One browser controls a pane at a time: opening
it elsewhere takes it over and the first browser says so and offers Reconnect. On phones a key
bar under the terminal supplies Esc, Tab, a sticky Ctrl, arrows, Enter, Paste and the font size.
The terminals use the font chain of Jeff's Ghostty: JetBrainsMono Nerd Font Mono if it is
installed, else self-hosted JetBrains Mono with the Nerd Font symbols, DejaVu Sans Mono, Noto Sans
Math and Noto Sans Symbols 2 behind it, each cut into small files a browser fetches only when a
glyph in them is on screen (DESIGN.md 12, "Terminal font"). Setting localStorage
`crowsnest-terminal-local-font` to `off` skips the installed font.
Note that herdr's TUI shows every machine your herdr config names, remote ones included, so the
Terminal view reaches those too.

**Command palette.** ⌘K on a Mac (Ctrl K elsewhere), or the "Search or run…" field in the
header (a search icon on a phone, which opens it full screen with the keyboard up). One box
searches everything the app already has live: jobs (by title, id, state, agent or directory, with
their state), open questions and agents at a dialog, agents and shell panes, the views,
Notifications, Journal, Keyboard shortcuts, the theme and Sign out, and actions (Poke now, New
conversation, Restart orchestrator, Send test notification, New job form). Whatever you type is
also offered as **Give job: …** (Enter gives it to the orchestrator; ⌘/Ctrl Enter opens the full
form with it filled in) and **Send to orchestrator: …** (a Chat prompt); start with `>` or `job `
to offer only Give job. → (or Tab) shows an item's other actions: nudge, message or cancel a job,
open an agent's conversation or terminal, and answer a confirm or single-choice question from its
options. Restart, New conversation and Cancel ask first, in the same words as the views. Esc goes
back a level, then closes and puts focus back where it was. With an empty box, recently used
items come first (kept in localStorage, `crowsnest-palette-recent`). Inside a terminal only ⌘K
opens it: Ctrl K belongs to the shell. `?` lists every shortcut, and `g` then `j`, `c`, `i`, `f`
or `t` goes to Jobs, Chat, Inbox, Fleet or Terminal; neither fires while you type in a field or a
terminal.

For development, `mise run dev` builds the single binary and runs the real thing on one port
(http://localhost:2769) against its own herdr session. `mise run dev:hot` adds Vite with hot reload
on :5173 (Vite proxies `/api` to Go), and `mise run dev:demo` runs the scripted demo with no herdr
or Claude.

To work on the UI with no server at all, open http://localhost:5173/fleet?fixtures=1 on the Vite
dev server: every API call is answered in memory from `web/src/lib/fixtures.ts` (a fleet with
agents in every state and two dialogs), and a "fixtures" chip shows in the header. `?fixtures=0`
turns it off. Fixtures mode exists only in development builds; it is compiled out of
`mise run build`.

On a phone over Tailscale, bind the tailnet address, or keep localhost and use `tailscale serve`,
which also gives the HTTPS secure context the microphone needs for talk mode:

```sh
./bin/crowsnest --bind tailnet --username jeff --password-file ~/.config/crowsnest/password
```

**Notifications on your phone (Web Push).** Account menu → **Notifications** → *Turn on for this
device*. From then on the phone (or laptop) gets a notification when an agent asks a question
(blocking or normal), waits at a terminal dialog, a job gets stuck, fails or finishes, or the
orchestrator needs the terminal, with no crowsnest tab open; tapping it opens the item. The sheet
switches each kind on or off, sends a test, and lists and removes devices (at most 20). Push needs
a **secure context**: `localhost` works as is; on a tailnet use `tailscale serve` (HTTPS on your
MagicDNS name) or `--tls-cert`/`--tls-key`, since a plain `http://100.x.y.z` page cannot subscribe.
On **iPhone and iPad** Web Push works only for the app added to the Home Screen (Safari, Share,
Add to Home Screen; iOS 16.4 or later), opened from there. When something is missing the sheet
says which. The server makes its VAPID key pair on first start and keeps it in the database; it
POSTs only to public `https://` push-service addresses, and notices carry a title, one line and a
link, nothing more (the same scrubbing and rate limits as `--notify-url`).

**Talk mode.** Start crowsnest with an ElevenLabs API key (`ELEVENLABS_API_KEY`; prefer the
environment to the flag, which `ps` shows) and two things appear:

- **The microphone**, a mic button beside Send in Chat, always there. Hold it and speak (or tap
  it to lock recording on and tap again to stop); on a desktop, **hold Space** anywhere outside a
  text field, or **Option+Space** (Ctrl+Space off a Mac) while typing in the box. A recording can
  run **five minutes**: the elapsed time shows as you speak, a warning comes in the last 30 s, and
  at the cap it stops and is sent. A press too short to hold any speech sends nothing. What you
  said is transcribed by ElevenLabs, shown for 1.5 s with Cancel, then sent to the orchestrator
  under a `[crowsnest voice: …]` header. The first press asks for the microphone; nothing needs
  switching on first.
- **Speak mode**, the speaker switch in the header of every view, remembered per browser. The
  orchestrator begins **every** reply with `SAY: <one sentence>` (its `CROWSNEST.md` says so,
  the Earshot contract), then the detail; Chat always shows that sentence as the reply's caption,
  with a play button. With speak mode on, each new reply's sentence (and anything an agent says
  with `crowsnest say`) is spoken wherever you are in the app, with a now-playing strip under the
  header; `SAY: -` and `noop` are silent, a reply with no SAY line chimes. Speech is made only
  when a browser asks to hear it, and cached, so with speak mode off nothing is spent.

**Voice** in the account menu picks the ElevenLabs voice (the account's voices, with a Preview),
the speed (0.8× to 1.5×: ElevenLabs speaks up to 1.2×, the rest is played faster in the browser
with the pitch kept) and the model (fast, `eleven_flash_v2_5`, or higher quality,
`eleven_multilingual_v2`, which takes about three times as long to start). The choice is stored
in crowsnest's database and applies from the next reply; `--elevenlabs-voice` is only the default
until one is chosen, and the sheet says where the current voice comes from. In the Inbox, a
question can be read aloud and its note dictated. The key never leaves the server: the browser
can only ask to hear texts the server holds (by id), never arbitrary text. The microphone needs a
secure context (`https://` or `localhost`); over plain HTTP on a tailnet address the mic explains
that when pressed, and spoken replies still work. Without a key every `/voice` route is a 404 and
nothing changes.

## Container (devbox)

`ghcr.io/jclement/crowsnest` is a ready-made devbox: crowsnest, herdr and mise in one container
(linux amd64 and arm64), with Claude Code and node installed by mise into a volume on first
start. A browser gets the herdr session, a terminal and (once the orchestrator lands) Claude.

```sh
cp deploy/crowsnest.env.sample deploy/crowsnest.env
docker compose -f deploy/compose.yaml up -d
docker compose -f deploy/compose.yaml logs -f crowsnest   # first start: mise install, then the setup link
```

Run it, open the setup link it prints (`…/setup#token=…`), pick a username and password. The
login is stored in the volume; `docker compose exec crowsnest crowsnest reset-password` sets a
new one. For an unattended install set `CROWSNEST_USERNAME` in `crowsnest.env`, write the
password to `deploy/crowsnest_password` (mode 0600, uid 1000) and uncomment the secret lines in
`deploy/compose.yaml`.

While the repository is private its GHCR package is private too: `docker login ghcr.io` (a
token with `read:packages`, e.g. `gh auth token | docker login ghcr.io -u <you> --password-stdin`)
before the first pull.

Or without compose:

```sh
docker run -d --name crowsnest -p 127.0.0.1:2769:2769 -v crowsnest-home:/home/crow ghcr.io/jclement/crowsnest
docker logs crowsnest 2>&1 | grep -o 'http://[^"]*/setup#token=[^"]*'   # the setup link
```

**The one manual step is signing Claude in.** Nothing personal is in the image, Claude's login
included: after setup, open **Terminal**, run `claude`, and
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

1. **Checks the login settings**: with no `CROWSNEST_USERNAME` crowsnest uses the login stored in
   the volume, or on first start enters setup mode (the link is in `docker logs`). It exits 64
   with a message when a password is set without a username, when `CROWSNEST_PASSWORD_FILE` is
   unreadable, or when `CROWSNEST_NO_AUTH=true` lacks `CROWSNEST_I_KNOW_THIS_IS_OPEN=true`. It then
   drops `TUNNEL_TOKEN` and `TS_AUTHKEY` if they reached it (they belong in the sidecars' own env
   files), and notes when the password came as a variable rather than a file.
2. **On first start only** writes `~/.config/mise/config.toml` (node LTS, and Claude Code from
   mise's `claude` registry entry, the native binary) and a herdr config that skips herdr's
   first-run screen. Existing files are never overwritten; edit them to change the tool set.
3. **Runs `mise install`** (with crowsnest's secrets removed from its environment) and waits up to
   `CROWSNEST_MISE_WAIT` seconds (default 300) for it. A failure is logged with the last lines of `~/.local/state/crowsnest/mise-install.log` and a slow
   install carries on in the background; neither stops crowsnest from starting.
4. **Execs `crowsnest serve --bind 0.0.0.0`**, passing through any arguments given to the
   container. Every other setting is a `CROWSNEST_*` variable (see Configuration). Change the
   port with `CROWSNEST_PORT`, not `--port`, so the health check probes the same port.

Upgrading tools needs no rebuild: `mise upgrade` in the web terminal (Claude Code's own
auto-updater is off in the image for that reason). Upgrading crowsnest, herdr or mise is a new
image; the volume carries over.

**First-start installs are `latest`, on purpose.** The seeded mise config asks for `node = "lts"`
and `claude = "latest"`, so a new volume gets whatever is current that day, checked by mise
against the release checksums (aqua) but not pinned. That is a trade: Claude Code moves fast and
an image-pinned copy would be stale within a week. To pin, edit `~/.config/mise/config.toml` in
the volume (for example `claude = "2.1.332"`, `node = "26.13.0"`) and run `mise install`; the
entrypoint never overwrites that file. Everything the image itself installs is pinned: the base
images by digest, herdr and mise by version and SHA-256, gh by version with its apt key's
fingerprints checked, and the web build runs `npm ci --ignore-scripts`.

**Secrets in the container.** Agents run as `crow`, the same user as crowsnest, so they can read
any environment in the container through `/proc/<pid>/environ`. crowsnest re-executes itself
without its secrets and gives its children none, but tini (PID 1) keeps the environment Docker
started it with. A login chosen at setup is in no environment at all (only its argon2id hash, in
the database); for a configured one `deploy/compose.yaml` mounts the password as a file
(`CROWSNEST_PASSWORD_FILE`; `deploy/crowsnest_password`, mode 0600, owned by uid 1000), and each
sidecar's token lives in that sidecar's own env file (`cloudflared.env`, `tailscale.env`), never
in `crowsnest.env`. `CROWSNEST_NOTIFY_URL` and `ELEVENLABS_API_KEY` have no file form yet: set in
`crowsnest.env`, an agent can read them. A file of crowsnest's is readable by the same user too;
the only hard boundary would be a separate uid for crowsnest, which the image does not have.

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
| `crowsnest health [--json]` | GET `/api/v1/health` on the configured address; exit 0 healthy, 1 not (Docker `HEALTHCHECK`). A server waiting for first-run setup is healthy and says "setup required" |
| `crowsnest reset-password [--password-file F]` | Set a new password for the login chosen at setup, and sign every browser out (at once, even while crowsnest runs). The password comes from `--password-file` (first line, mode 600), else a piped stdin, else it is asked for twice. Use the same `--session`/`--data-dir` as serve |
| `crowsnest reset-setup [--yes] [--force]` | Remove the stored login and every session, so the next start is first-run setup again (the instance name is kept). Asks for confirmation (or `--yes`); refused while crowsnest is running unless `--force`, after which nobody can sign in until it is restarted |
| `crowsnest update [--check]` | Install the latest release over this binary after verifying its signature and checksum (see Install, Updating); `--check` only reports. Refused for a development build, in a container and for a Homebrew install |

Exit codes: `0` success, `1` runtime error, `2` usage error.

### Agent side

These run inside agent panes and talk to the server over the unix socket named by
`CROWSNEST_SOCKET` (crowsnest sets it, and puts its own directory first on `PATH`, in the
orchestrator's pane). Who is asking comes from `CROWSNEST_TOKEN`, a random token crowsnest mints
for each agent it starts (in the pane's environment, and in its MCP server's) and maps to that
pane and job; a pane or job a caller names itself is never believed. Without a token a caller is
unidentified: it may ask, but its answer only comes back through `--wait`, and it may not
withdraw questions. Without `CROWSNEST_SOCKET`, or with no server answering, they fail with exit
1 and say so.

| Command | Does |
|---|---|
| `crowsnest ask --file q.json\|- [--wait] [--timeout 12h] [--attach file]` | Post a question (DESIGN.md 8.1 shape: `title`, `body_md`, `kind` confirm/choice/ack/manual/notice, `multi`, `options`, `recommend`, `context`, `urgency`, `expires_in_hours`, `files`, `attachments`). `attachments` (or `--attach`, repeatable) are files shown with the question: paths inside your working or job directory. Without `--wait` it prints the id (`q_7`) and returns; the answer is typed into the pane later as `[crowsnest answer q_7] <answer> (note: …); the question was "<title>"` (or, when that would not fit on one line, a line saying to read it with `crowsnest question`). With `--wait` it blocks and prints `{id, status, answer_value, answer_note}`: exit `0` answered, `3` expired or cancelled, `1` error. `expires_in_hours` is between 5 minutes (0.084) and 168 |
| `crowsnest question <id>` | Print a question with its whole answer, `{id, status, title, answer_value, answer_labels, answer_note}`; never shortened |
| `crowsnest notify "text" [--urgency low\|normal\|blocking]` | A notice (no answer needed); the first line is its title. Prints the id |
| `crowsnest say "sentence"` | Speak one sentence in every browser with speak mode on; a no-op without an ElevenLabs key. Only an agent crowsnest started (with its `CROWSNEST_TOKEN`) may speak |
| `crowsnest mcp` | A stdio MCP server exposing `ask`, `get_question`, `say`, `job_update`, `done`, `fail`, `job_create` and `job_get`, proxied to the socket. Claude starts it from the `mcp.json` crowsnest writes. `ask` with `wait=true` returns the whole answer and note; `ask` with `job_id` (the orchestrator, for the job it holds) files the question under that job, and the socket refuses a job the caller does not hold |
| `crowsnest job update "doing now" [--state blocked --reason "…"] [--waiting]` | The job protocol for agents without MCP (Codex) and scripts: a heartbeat with a progress line; `--state blocked` with a reason, or `running`; `--waiting` declares that nothing is left the open questions do not block (refused with no open question). The job is `--job j_<n>` or `CROWSNEST_JOB` |
| `crowsnest job done --result "…" --verified "…" --not-verified "…" [--attach file]` | The job is finished, with evidence (repeat the list flags; `none` for an empty list; `--result-file` reads the result; `--attach`, repeatable, attaches a file from your working or job directory to the result) |
| `crowsnest job fail "reason"` | It cannot be finished |
| `crowsnest job get [j_<n>] [--json]` | The job as text for an agent (brief, state, progress, questions and answers), or the full record with its timeline |
| `crowsnest job create "brief" [--dir] [--role] [--done-when] [--engine] [--model] [--isolate]` | Delegate a child job; the caller's job is its parent. Inside the caller's own directory, with its engine, it starts at once; another directory, no directory, another engine or `--isolate=false` from an isolated job waits for the human to allow it in the Inbox ("own directory" is the one crowsnest recorded, resolved, when it opened the caller's pane; a job given in `~` or `/` grants none). Only a plain Allow starts it: an Allow with a note cancels it, and the note reaches the caller. At most 3 levels deep, 8 children per job, 16 live agent-created jobs, 2 requests per caller waiting for the human. The orchestrator may use any directory the human has given a job before (not `~` or `/`), needs the human for Codex, and may have 8 live jobs of its own while it holds none |
| `crowsnest job list [--state …]` | Jobs, newest first |
| `crowsnest hook <event> [--job j_<n>] [--socket PATH] [--token-file PATH]` | The Claude Code hook entry point crowsnest writes into each agent's `--settings` (`stop`, `session-start`, `notification`, `stop-failure`, `permission-denied`, `session-end`, `post-tool-use`): stdin JSON to the server, its reply on stdout. Fails open: any failure prints nothing and exits 0 |

The socket is `$XDG_RUNTIME_DIR/crowsnest/<session>.sock` when `XDG_RUNTIME_DIR` is set, else
`~/.local/state/crowsnest/<session>/agent.sock` (or, when that path is too long for a unix socket,
`crowsnest-<uid>/<hash>.sock` under the OS temporary directory); `serve` logs it at start. Its
directory is `0700` and must belong to you; the socket is `0600`.
Its routes are Meat Prompt's (`POST /api/v1/questions`, `GET /api/v1/questions/{id}?wait=90`,
`GET /api/v1/questions?status=`, `DELETE /api/v1/questions/{id}`) plus `POST /api/v1/say`,
`GET /api/v1/health`, the job protocol (`POST /api/v1/jobs`, `GET /api/v1/jobs`,
`GET /api/v1/jobs/{id}`, `GET /api/v1/jobs/{id}/context`, `POST /api/v1/jobs/{id}/update|done|fail`)
and the hooks (`POST /api/v1/hooks/{event}`).

The caller's token travels as `X-Crowsnest-Token`. Job verbs follow it: anyone may read a job,
but `update`, `done` and `fail` work only on the job the token was issued for, or the job held in
the token's pane (the orchestrator's case); creating a job needs a token; a hook counts for the
token's job, or the job in the token's pane, and otherwise for nothing. Each agent start writes
its token to an `agent-token` file (0600) that the hook command lines name with `--token-file`.
Quotas, per caller (all unidentified callers share one): 10 asks at once then one per 30 s, 30
open questions (200 in all), 8 concurrent long-polls, and a say rate limit; past one the answer is
`429` with `Retry-After` and a sentence saying what to do.

Roles are prompts: `lead`, `builder`, `reviewer` and `tester` are built in; a file
`~/.config/crowsnest/roles/<name>.md` adds a role or replaces a built-in one. The job protocol is
appended to every role's prompt.

## Scheduled work

Periodic work is a markdown file in `<--dir>/tasks/` or `~/.config/crowsnest/tasks/` (the first
wins on a name clash), or a schedule made in the app (below). The files are re-read at every
check, so adding, editing or removing one needs no restart. The format is claude-agent's front
door's, so its task files run unchanged:

```markdown
---
every: 15m        # 15m, 60m, 4h, 1d, 3d (calendar days, local time), poke or change
after: 17:00      # optional: not before this local time of day
as: job           # optional: start a crowsnest job instead of telling the orchestrator
dir: ~/Work/thing # as: job only: where the job runs (none: the orchestrator takes it)
role: builder     # as: job only
project: webapp   # optional: a project's slug; a poke goes to that project's lead, a job joins it
---
# Daily digest

What to do, in plain words. The orchestrator (or the job's agent) reads this file and does it.
```

- A due task becomes one line of the next poke: `scheduled task digest is due (every 1d after
  17:00, last run …): read <file> and do what it says`. Nothing due still means no poke. The
  task is marked run when the poke carrying it is delivered, by crowsnest, so it never repeats
  because the model forgot to mark it, and never runs twice on one calendar day.
- `every: poke` rides along with any poke that has something else to say; it never makes one by
  itself. `every: change` runs when the fleet or the jobs changed since its last run (at most
  every 5 minutes). Neither can be `as: job`.
- `as: job` starts a crowsnest job with the body as its brief and the full supervisor; a job
  task whose previous job is still live waits for it rather than stacking a second copy.
- `project: <slug>` sends a poke task to that project's lead instead of the orchestrator (same
  due rules, stamps and gate), and files an `as: job` task's job under the project. A slug that
  names no live project is a problem.
- A file that cannot be used (no frontmatter, an unknown key, `every: 1w`, an unknown project)
  is skipped and reported once in the Journal, and listed in the Schedule view.
- **App schedules** are made, edited and deleted in the app (`POST`/`PUT`/`DELETE /schedule`)
  and stored in the database: a name, a cadence (every N minutes of at least 5, hours or days;
  daily at HH:MM; on poke; on change, which are `every`/`after` in the file grammar), a target
  (the orchestrator, a project's lead, or a job with its directory, role and engine) and a
  body. They follow exactly the rules a file does. A poke-type schedule's body is written to
  `<state dir>/schedules/<name>.md` so the poke line can name it. Names are shared with task
  files and a **file wins**: an app schedule whose name a file has is listed as shadowed and
  never runs, and making one with a file's name is refused.
- Every run (a poke delivered, a job created, a job that could not be created, waiting for the
  previous run) is kept in a short history, the last 20 per task, shown in the Schedule view.
- The **Schedule** view (Chat menu, the Jobs view's Schedule button, or the command palette)
  lists every task with its cadence, target, last run, next time, last outcome and recent runs,
  a Run now button and an on/off switch. The switch is crowsnest's (kept in its database) for
  both kinds; a file stays the source of what it says, and only app schedules are editable.

`examples/tasks/` holds three to copy: `check.md` (every 15 minutes: anything stuck or waiting
on you?), `tidy.md` (hourly: bring the orchestrator's notes up to date) and `digest.md` (daily
after 17:00: one notice with the day's digest). They do nothing until copied into a tasks
directory.

## Spend

Every job shows what its agent has used, and the orchestrator's own conversation is a line of
its own: tokens in, out, written to and read from the prompt cache, model calls, tool calls, wall
time, and an **estimated** cost in USD. The figures come from each agent's Claude transcript
(`~/.claude/projects/…/<session>.jsonl` and its subagents' files beside it), read every 10
seconds and kept in crowsnest's database, so they survive a restart and a deleted transcript. A
job that is restarted under a new Claude session keeps counting.

- **It is an estimate, not a bill.** The cost is what the same tokens would cost on the Claude
  API at list prices (input, output, 5-minute and 1-hour cache writes, cache reads; fast mode,
  Haiku 5.5's long-context tier and US-only inference included). A Claude subscription is not
  billed per token. Every cost is marked "est.".
- **Prices** are built in, with the date they were checked against Anthropic's pricing page
  (shown in the Spend sheet). To correct or add one, write `~/.config/crowsnest/prices.json` in
  the same shape; its models replace built-in rows by id or add new ones, and it reprices
  history too (costs are computed when read):

  ```json
  {"checked": "2027-01-15", "models": [
    {"id": "claude-opus-5-5", "aliases": ["opus"], "input": 4, "output": 20,
     "cache_write_5m": 5, "cache_write_1h": 8, "cache_read": 0.20, "fast": 2}
  ]}
  ```

  Prices are USD per million tokens; `fast` multiplies every rate in fast mode; a `long` object
  (`{"over": 100000, "input": …}`) is a long-context tier. An unknown key is an error, logged at
  start, and the built-in table is used instead. A model with no price shows its tokens and
  "cost unknown", never $0.
- **Not measured**: Codex jobs (crowsnest does not read Codex's usage records), shown as "not
  measured" rather than zero. Jobs the orchestrator holds itself are counted on the
  orchestrator's line.
- **Where it shows**: a chip on each job card; a Spend section in the job's detail (the
  breakdown, the share of the prompt read from cache, and a small chart of tokens per model
  call); today's and this week's totals above the board, which open the Spend sheet (by job, by
  model or by day; today, this week or 30 days; sortable, CSV export); the orchestrator's line in
  the Chat menu; "Spend" in the command palette. `crowsnest job get` and the `job_get` tool show
  a job its own spend.

Days are the server's local calendar days; weeks start on Monday.

## Configuration

Precedence: flag, then environment, then `~/.config/crowsnest/config.yaml` (keys are the flag
names without the leading `--`, e.g. `username: jeff`, `bind: [localhost, tailnet]`), then the
default. List values (`--bind`, `--allowed-host`) repeat as flags, are comma-separated in the
environment, and are a YAML list or string in the file. An unknown key in the file is an error.

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--username` | `CROWSNEST_USERNAME` | none | Login name. With its password it wins over the login stored at first-run setup; with neither (and no `--no-auth`) crowsnest uses the stored login, or starts locked in setup mode |
| `--password` | `CROWSNEST_PASSWORD` | none | Login password. Prefer the env var or `--password-file`: a flag is visible in `ps` |
| `--password-file` | `CROWSNEST_PASSWORD_FILE` | none | File holding the password (first line). Refused if its group or others can read or write it: `chmod 600` |
| `--no-auth` | `CROWSNEST_NO_AUTH` (`SUPPRESS_AUTH=true` also honoured, on a localhost-only bind) | `false` | No login. For a trusted localhost or tailnet only; on a tailnet, every peer your ACL lets reach the port has a shell, including nodes shared into your tailnet |
| `--bind` | `CROWSNEST_BIND` | `localhost` | `localhost`, `tailnet`, an interface name, an IP, or `ip:port`. Repeatable |
| `--port` | `CROWSNEST_PORT` | `2769` | Port for binds that do not name one (C-R-O-W on a keypad) |
| `--session` | `CROWSNEST_SESSION` | `crowsnest` | herdr session to create or connect to |
| `--dir` | `CROWSNEST_DIR` | `<data-dir>/orchestrator` | Orchestrator's working directory |
| `--model` | `CROWSNEST_MODEL` | `sonnet` | Orchestrator model. An explicit empty value (`--model=` or `CROWSNEST_MODEL=`) means Claude's own default |
| `--job-model` | `CROWSNEST_JOB_MODEL` | unset (Claude's default) | Model for job agents whose job names none (a delegated job still inherits its parent's) |
| `--permission-mode` | `CROWSNEST_PERMISSION_MODE` | `auto` | Orchestrator permission mode (`acceptEdits`, `auto`, `bypassPermissions`, `dontAsk`, `manual`, `plan`) |
| `--poke-interval` | `CROWSNEST_POKE_INTERVAL` | `15m` | Timer poke interval; `0` disables timer pokes |
| `--no-orchestrator` | `CROWSNEST_NO_ORCHESTRATOR` | `false` | Serve the UI over the session without starting Claude |
| `--name` | `CROWSNEST_NAME` | the name chosen at setup or in the Account sheet, else the short hostname | This instance's name (letters, digits, `-`, `_`; up to 32): in the header, page title, notices, `/auth/me` and Remote Control. Given here, it wins over the stored one and the Account sheet shows it read-only |
| `--no-remote-control` | `CROWSNEST_NO_REMOTE_CONTROL` | `false` | Start the orchestrator without Claude Code's Remote Control (on by default, named `<name> crowsnest`) |
| `--job-remote-control` | `CROWSNEST_JOB_REMOTE_CONTROL` | `false` | Start job agents with Remote Control too, named `<name> <job id> <title>` |
| `--compact-at` | `CROWSNEST_COMPACT_AT` | `150000` | Compact the orchestrator (`/compact` with a focus) once its context passes this many tokens; `0` disables |
| `--fresh-at` | `CROWSNEST_FRESH_AT` | off | `HH:MM`: once a day after this local time, when you have been quiet 20 minutes, the orchestrator writes a hand-over and starts a new conversation |
| `--attachment-max-mb` | `CROWSNEST_ATTACHMENT_MAX_MB` | `25` | Largest file you can paste, drop or attach, and that an agent can attach to a question or a result, in MB |
| `--attachment-quota-mb` | `CROWSNEST_ATTACHMENT_QUOTA_MB` | `1024` | Disk all attachments may take, in MB; past it the oldest files nobody needs any more are deleted, else an upload is refused |
| `--allowed-host` | `CROWSNEST_ALLOWED_HOSTS` | derived | Extra Host/Origin name (reverse proxy, MagicDNS). Repeatable |
| `--trusted-proxy` | `CROWSNEST_TRUSTED_PROXY` | none | `loopback`, an IP or a CIDR: a reverse proxy or tunnel whose client-IP header (`--trusted-proxy-header`) is believed, for the login rate limit, the per-client caps and the Secure cookie. Repeatable. Set `loopback` when a tunnel shares crowsnest's network namespace (the compose example) |
| `--trusted-proxy-header` | `CROWSNEST_TRUSTED_PROXY_HEADER` | `x-forwarded-for` | Which header a trusted proxy names its client in: `x-forwarded-for` (its last hop; tailscale serve, most proxies) or `cf-connecting-ip` (cloudflared only, which overwrites it; any other proxy passes a client's own copy through, R3-L1) |
| `--tls-cert` | `CROWSNEST_TLS_CERT` | none | PEM certificate: serve HTTPS directly |
| `--tls-key` | `CROWSNEST_TLS_KEY` | none | PEM key for `--tls-cert` |
| `--elevenlabs-api-key` | `ELEVENLABS_API_KEY` | none | Enables talk mode |
| `--elevenlabs-api-key-file` | `ELEVENLABS_API_KEY_FILE` | none | File holding the ElevenLabs key (first line, mode 600): keeps it out of the environment |
| `--elevenlabs-voice` | `CROWSNEST_ELEVENLABS_VOICE` | a stock voice id (`21m00Tcm4TlvDq8ikWAM`) | Default voice for spoken replies, until one is chosen in the app (account menu, Voice) |
| `--elevenlabs-url` | `CROWSNEST_ELEVENLABS_URL` | `https://api.elevenlabs.io` | ElevenLabs API base URL: https, or plain http only to a loopback address (tests point it at a local fake). Redirects are never followed |
| `--notify-url` | `CROWSNEST_NOTIFY_URL` | none | POST a one-line notice here when something needs you (ntfy-style) |
| `--notify-url-file` | `CROWSNEST_NOTIFY_URL_FILE` | none | File holding the notify URL (first line, mode 600) |
| `--demo` | `CROWSNEST_DEMO` | `false` | In-memory fake herdr and scripted orchestrator |
| `--i-know-this-is-open` | `CROWSNEST_I_KNOW_THIS_IS_OPEN` | `false` | Allow `--no-auth` on a bind that is not loopback or tailnet |
| `--allow-default-session` | `CROWSNEST_ALLOW_DEFAULT_SESSION` | `false` | Allow `--session default` (your main herdr session) |
| `--data-dir` | `CROWSNEST_DATA_DIR` | `~/.local/share/crowsnest` | Where the database (`<session>.db`) lives |
| `--config` | `CROWSNEST_CONFIG` | `~/.config/crowsnest/config.yaml` | Config file to read |

`--web-dev` (`CROWSNEST_WEB_DEV`) is a hidden development flag used by `mise run dev`: the UI is
served by Vite, so the binary does not require embedded web assets.

`--session`, `--allow-default-session`, `--demo`, `--dir`, `--model`, `--permission-mode`,
`--no-orchestrator` and `--notify-url` (one plain-text line, the question's id and scrubbed title,
POSTed for each `blocking` question, at most 3 at once and then one a minute per asking pane; the
URL is never logged) and the ElevenLabs settings (talk mode, M11) take effect now. When `serve`
finds `CROWSNEST_PASSWORD`, `CROWSNEST_PASSWORD_FILE`, `ELEVENLABS_API_KEY` or
`CROWSNEST_NOTIFY_URL` in its environment it re-executes itself once with them moved to an
inherited pipe, so the environment the OS reports for the process (`/proc/<pid>/environ`,
`ps eww`) holds none of them. `--poke-interval` is the timer poke (M8): at each tick the
orchestrator is poked only if the list of open work (stuck jobs, jobs that need you, open
questions) changed since the last timer poke; `0` turns the timer off (event pokes and Poke now
still work).

### Startup refusals

Crowsnest refuses to start, and says what to pass instead, when:

- credentials and `--no-auth` are both given (neither is not a refusal: it is the locked
  first-run start above, also on a bind that is not loopback, since nothing but the token-gated
  setup page is served; the log says so loudly);
- `--no-auth` is combined with a bind that is not loopback or a Tailscale address (including
  `0.0.0.0`), unless `--i-know-this-is-open` is also given;
- the login is off through `SUPPRESS_AUTH` (rather than `--no-auth` or `CROWSNEST_NO_AUTH`) on a
  bind that is not localhost-only: that generic variable is often exported by other projects'
  environments, so it never opens anything beyond this machine;
- `--session default` is given without `--allow-default-session`;
- `--bind tailnet` finds no Tailscale address (100.64.0.0/10 or fd7a:115c:a1e0::/48); it never
  falls back to all interfaces.

## HTTP API

Routes under `/api/v1`. Responses are `{"data": …}` (lists add `"meta"`), errors
`{"error": {"message", "code"}}`. Every non-GET request needs a same-origin `Origin` and
`X-Crowsnest: 1`.

| Route | Auth | Purpose |
|---|---|---|
| `GET /health` | none | `{status, version, setup_required, update_available}`, plus `latest_version` and `release_url` when a newer release was found |
| `GET /setup` | none | `{required, default_name?, min_password_length}`: whether first-run setup is waiting |
| `POST /setup` | none (the setup token) | `{token, username, password, name}` → `201` and a session cookie; `403 BAD_TOKEN`, `400` for a field to fix, `429` with `Retry-After` (about five tries a minute, for everyone), `410` once setup is done. While setup waits, every other API route answers `403 SETUP_REQUIRED` and every page redirects to `/setup` |
| `GET /account` | session | `{username, source: database\|config\|none, name, name_managed, min_password_length}` |
| `POST /account/password` | session | `{current_password, new_password}` → signs every other session out and re-issues this browser's cookie; `403 WRONG_PASSWORD` (counted like a failed sign-in), `409` when the login comes from flags or the environment |
| `PUT /account/name` | session | `{name}` → the instance name, stored; `409` when `--name`/`CROWSNEST_NAME` sets it |
| `POST /admin/update` | session | Verify and install the newer release (as `crowsnest update`), answer `{updated, version, restarting}`, then exit for a supervisor to restart. `409 REFUSED` with the reason (container, development build, Homebrew install, already current), `409 UNSUPPORTED` when self-update is off (development build, `--demo`, container), `502 UPDATE_FAILED` when a download or check failed (nothing changed; the log says why) |
| `POST /auth/login` | none | `{username, password}` → session cookie; `429` with `Retry-After` after 5 failures a minute per IP or 20 overall |
| `POST /auth/logout` | none | Deletes the session and clears the cookie |
| `GET /auth/me` | session | `{username, features: {auth, voice, demo, session, herdr_version, name, attachments}}`; `name` is this instance's (`--name`, else the one chosen at setup or in the Account sheet, else the short hostname); `attachments` is `{max_bytes, max_files}` |
| `GET /events` | session | Server-sent events: `hello` on connect, `heartbeat` every 20 s, `fleet` (data: the new fleet) whenever it changes, `message` (`{pane_id, cursor}`) when a followed conversation changes, `orchestrator` (its status) on every state change, `question` (`{id, status, title, kind, urgency, change}`) when a question is asked, settled or delivered, `say` (voice on: `{kind: speak\|chime, source: say\|reply, text_id, text, pane_id, at}`, played by browsers with speak mode on; voice off: `{text, pane_id, at}`, which nothing plays); `job` (`{id, state, stuck, cause, title, change}`) when a job changes; `poke` (a poke's result, as `POST /orchestrator/poke` returns it) when a poke is sent or held; `journal` (`{id, kind, summary}`) for each new journal entry; `schedule` (`{at}`) when a scheduled task's stamp or switch changed; `attachment` (`{owner}`, such as `job:j_3`) when an owner's files changed; `spend` (`{at}`) when measured spend changed |
| `GET /fleet` | session | The herdr session: workspaces → tabs → panes with their agents, a flat `agents` list, `counts`, `revision`; `503 UNAVAILABLE` when herdr does not answer |
| `GET /agents/{target}/screen` | session | One agent's (or pane's) visible screen: `text`, `ansi`, `screen_hash`, `dialog` (options, form, warnings), `draft`. `{target}` is an agent name or pane id |
| `POST /agents/{target}/keys` | session | `{keys: ["1"], screen_hash}` → sends the keys if the screen still has that hash, else `409 CONFLICT` and nothing is sent. Only `esc`/`ctrl+c` may omit the hash |
| `GET /agents/{target}/messages?after=&limit=` | session | A page of the agent's conversation from its Claude transcript: `{source, session_id, cursor, reset, earlier, more, messages}`. Without `after` (or with a stale cursor) the newest `limit` (200) messages and `reset: true`; with it, only what changed since. No transcript: one `pane` message with the pane's recent text |
| `POST /agents/{target}/prompt` | session | `{text, force_over_draft, voice, attachments}` (`attachments`: upload ids, copied into the agent's inbox and named on `Attached:` lines after the text) (`voice: true` adds the voice header; 404 when talk mode is off) → `{pane_id, status, queued, started, note}`. `409 BLOCKED` (with the dialog in `error.detail`) when a dialog is up, `409 DRAFT` (with the draft) when text was typed at the terminal; nothing is typed in either case |
| `GET /orchestrator` | session | `{state, pane_id, session_id, dir, model, detail, restarts, remote_control, remote_control_note}`; `model` is the one Claude was started with (empty: Claude's default); `remote_control` is the Remote Control name Claude was started with, `remote_control_note` `active` once Claude said so, or a sentence when it did not within 45 s; state is `idle`, `working`, `blocked`, `needs_terminal`, `starting`, `restarting`, `failed` or `disabled` |
| `POST /orchestrator/restart`, `POST /orchestrator/new` | session | Restart Claude on the same conversation (`--resume`), or with a fresh one |
| `POST /orchestrator/poke` | session | "Poke now" → `{outcome: sent\|skipped, reason, reasons, manual, started, note, at}`. Skips the two-minute limit between agent pokes; `skipped` with `reason` "nothing is due" when there is nothing to say, or why the orchestrator cannot take one (working, a dialog, a draft) |
| `GET /orchestrator/context` | session | The orchestrator's context: `{tokens, known, at, compacted_at, human_at, threshold, last_compact_at, fresh_at, fresh_day}` (`known` is false before the first reading and right after a compaction that recorded no size) |
| `POST /orchestrator/compact` | session | "Compact now": `/compact` with the focus, at any size → the context as above; `409 CONFLICT` when the orchestrator is busy, at a dialog or has a draft |
| `GET /schedule?project=` | session | Scheduled tasks: `{tasks: [{name, title, path, origin: app\|dir\|user, editable, every, after, schedule, as, target: orchestrator\|lead\|job, project_id, project, dir, role, engine, body_md, shadowed, enabled, last_run_at, next_due_at, last_outcome, job_id, job_state, run_now, runs: [{at, outcome, job_id}]}], problems: [{path, name, error}], dirs}`; `project=<id>` keeps one project's tasks (`0`: the orchestrator's and unfiled) |
| `POST /schedule` | session | A new app schedule `{name, every, after, as: poke\|job, project_id, body_md, dir, role, engine}` → `201` and the list. `400` invalid, `409` the name is taken (by a file or app schedule) |
| `POST /schedule/{name}/run` | session | Run now: a poke task goes with the next poke the orchestrator can take, a job task starts its job → the list. `404` for an unknown name |
| `PUT /schedule/{name}` | session | `{enabled}`: switch a task off or on (kept by crowsnest; a file is untouched); and/or every field of `POST /schedule` (but `name`) to replace an app schedule → the list. `409` "read-only: edit the file" for a task file's edit |
| `DELETE /schedule/{name}` | session | Delete an app schedule (its history stays) → the list. `409` for a task file, `404` unknown |
| `GET /spend?from=&to=&group=` | session | Spend for the local days `from`..`to` (YYYY-MM-DD, inclusive; default this week, Monday to today, at most a year) grouped by `job` (the orchestrator is its own row), `model` or `day`: `{from, to, group, rows: [{key, label, …summary}], total, prices_checked, prices_source, prices_file, note}`. A summary is `{measured, reason, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, total_tokens, turns, tool_calls, wall_seconds, cost_usd (null when unknown), cost_partial, unpriced_tokens, cache_hit_share, models, first_at, last_at}` |
| `GET /spend/summary` | session | `{today, week, orchestrator_today, orchestrator_total, today_date, week_start, prices_checked, note}`: the Jobs header's totals and the Chat menu's line |
| `GET /journal?kind=&before=&limit=` | session | The journal, newest first: `{entries: [{id, at, kind, summary, detail}], total, next_before}`. `kind` filters (comma-separated: `poke`, `skip`, `send`, `answer`, `restart`, `alert`, `backstop`, `task`, `upkeep`); `before` is the previous page's `next_before`; `limit` defaults to 50, at most 200 |
| `GET /questions?status=` | session | Questions with a status (`open`, `answered`, `expired`, `cancelled`, a comma-separated list, or `all`), open first; file contents left out (`file_count` says how many) |
| `GET /questions/{id}` | session | One question with its `files`, the agent's `attachments` and the `answer_attachments` sent with the answer |
| `POST /questions/{id}/answer` | session | `{answer_value, answer_note, attachments}` (`attachments`: upload ids, copied into the asker's inbox and named in the answer line): a key, an array of keys for a multi-select, nothing for ack/notice; a `manual` answer is the note. `409 CONFLICT` once settled |
| `DELETE /questions/{id}` | session | Cancel an open question; the asker is told |
| `GET /inbox/summary` | session | `{open, blocking}` for the Inbox badge |
| `POST /attachments` | session | One file: the raw body with its name in `X-Filename` (URL-encoded), or multipart `file` → `201` the pending attachment `{id, name, type, size, sha256, image, thumb, inline, …}`. The type is sniffed from the bytes. `413 TOO_LARGE` past `--attachment-max-mb`, `507 QUOTA_FULL` when the quota cannot be freed |
| `GET /attachments/{id}` | session | The file: inline only for PNG, JPEG, GIF, WebP, BMP, PDF and plain text; everything else (SVG, HTML, …) as a download; always `nosniff`, and a sandboxing CSP on all but an inline PDF. `?download=1` forces a download. `/thumb` is a JPEG thumbnail of a picture, `/meta` the record |
| `GET /attachments?owner=&role=`, `?answer=` | session | An owner's files (`job:j_3`, `question:q_7`, `orchestrator:`), or the files sent with an answer (`answer=q_7`) |
| `DELETE /attachments/{id}` | session | Remove an upload that was never sent (`409` once sent) |
| `GET /jobs?state=` | session | The board: every job, newest first (`state` filters, comma-separated), each with its `spend` summary |
| `POST /jobs` | session | `{brief, dir?, role?, done_when?, engine?, model?, isolate?, attachments?}` (upload ids, listed in the brief) → the queued job (`201`); the agent starts in the background. No `dir`: the orchestrator takes it |
| `GET /jobs/{id}` | session | The job with its `timeline` (state changes, progress, nudges, hooks, questions, restarts, retries, messages), `questions`, `spend` and `spend_turns` (its latest model calls, oldest first: `{at, model, tokens, output, cost_usd}`) |
| `POST /jobs/{id}/message` | session | `{text, force_over_draft, attachments}`: typed into the job's agent as `[crowsnest message j_<n>] …`; errors like a Chat send |
| `POST /jobs/{id}/cancel`, `/nudge`, `/resume` | session | Cancel (Esc to the agent, then `cancelled`; the pane stays), nudge by hand, or resume a stuck job (retry now after a limit, restart a gone agent, else nudge) |
| `GET /roles`, `GET /dirs` | session | Role names with a one-line summary; remembered job directories, then the cwds of the session's panes |
| `POST /voice/stt` | session | Talk mode. The body is one recording as MediaRecorder made it (`audio/webm`, `audio/mp4`, `audio/ogg`, …; up to five minutes: at most 8.2 MB, and it must arrive within 2 minutes) → `{text}`. 12 a minute per session (`429` with `Retry-After`); ElevenLabs failures are `502`/`503`/`422` with code `VOICE_UPSTREAM` and never its own error text |
| `GET /voice/tts?text_id=` | session | The speech of a server-held text (a `say`, a SAY line, a read-aloud), streamed (`audio/mpeg`). `404` for an unknown or expired id (texts live an hour). 30 a minute per session; a replay comes from a cache |
| `POST /voice/read` | session | `{question_id}` → `{text_id}`: the question's title and options, to play through `/voice/tts` |
| `POST /voice/reply` | session | `{target, message_id}` → `{text_id, text}`: that reply's SAY line, read from the transcript on the server, to play through `/voice/tts` (Chat's play button). `404` when it has none |
| `GET /voice/settings` | session | `{voice_id, voice_source, default_voice_id, speed, speed_min, speed_max, playback_rate, model, models}`: how replies are spoken. `voice_source` is `settings` (chosen in the app) or where the default came from (`--elevenlabs-voice`, `CROWSNEST_ELEVENLABS_VOICE`, the config file, `default`); `playback_rate` is the browser's share of the speed |
| `PUT /voice/settings` | session | `{voice_id, speed, model}` (`voice_id` empty: the default; speed 0.8 to 1.5; model `fast` or `quality`) → as GET. Stored in the database; applies from the next text spoken |
| `GET /voice/voices` | session | The ElevenLabs account's voices `[{voice_id, name, category, description, labels}]`, cached for five minutes |
| `POST /voice/preview` | session | `{voice_id, speed, model}` (unsaved choices) → `{text_id, playback_rate}`: a fixed sample sentence to play through `/voice/tts` |
| `GET /push/key` | session | `{public_key}`: the VAPID key for `pushManager.subscribe` |
| `GET /push/settings`, `PUT /push/settings` | session | `{kinds, order, devices, max_devices}`; PUT `{kinds: {question, dialog, job, done, orchestrator: bool}}`. A device is `{id, user_agent, service, created_at, last_success_at, last_failure_at, failures, endpoint_hash}`, never its endpoint |
| `POST /push/subscriptions` | session | The browser's `PushSubscription` JSON → `201` and the device. `400` for a non-https or non-public endpoint or malformed keys; `409` past 20 devices |
| `DELETE /push/subscriptions`, `DELETE /push/subscriptions/{id}` | session | `{endpoint}`: this browser turning push off; or remove a device by id |
| `POST /push/test` | session | `{endpoint?}`: a test notification to this browser, or every device → `{sent, failed, pruned}`. 5 a minute |
| `GET /term/tui` (WebSocket) | session | The herdr TUI in a PTY. Binary frames are terminal bytes both ways; text frames are JSON control: the browser sends `{"type":"resize","cols","rows"}` first (the process starts on it), the server ends with `{"type":"exit","reason"}` |
| `GET /term/pane/{pane_id}` (WebSocket) | session | One pane through `herdr terminal session control --takeover`, same protocol. `404` unless the pane is in the fleet |

## Tasks

| Task | Does |
|---|---|
| `mise run setup` | Install tools, Go modules and web dependencies. Idempotent |
| `mise run dev` | The real thing, locally, as one binary on one port (`scripts/dev.sh`): builds `./bin/crowsnest` with the web app embedded and runs it (`--no-auth`, http://localhost:2769) against its own herdr session `crowsnest-dev` (`CROWSNEST_DEV_SESSION` to change; `default` is refused), with a real Claude orchestrator in `.dev/orchestrator`, its database in `.dev/data` and an empty `.dev/config.yaml` so nothing of a real install is read. Every `HERDR_*` variable is removed first, so it is safe to run from inside a herdr pane. The orchestrator's directory is new, so the first start stops at Claude's folder-trust prompt: answer it once in the Terminal view. Long-running; Ctrl+C stops it (the herdr session keeps running) |
| `mise run dev:hot` | As `dev`, for UI work: the Go server by `go run` (`--web-dev`) on :2769 plus Vite with hot reload on :5173. Two ports; open :5173 |
| `mise run dev:demo` | Go server (`--no-auth`, `--web-dev`, `--demo`) on :2769 and Vite on :5173. No herdr or Claude, throwaway state. Long-running |
| `mise run dev:container` | Build the devbox image for this machine (`image`), then run it in the foreground (`scripts/dev-container.sh`): container `crowsnest-dev` (`--rm`), `127.0.0.1:2770` (`CROWSNEST_DEV_CONTAINER_PORT`), home in the volume `crowsnest-dev-home`, user `crow` with a fresh random password printed once. The password reaches the container only as a 0600 file owned by `crow` in a throwaway volume (`crowsnest-dev-secret`, removed on exit) named by `CROWSNEST_PASSWORD_FILE`: never argv, environment or image. Ctrl+C stops it |
| `mise run dev:container:reset` | Remove the `crowsnest-dev` container and its volumes |
| `mise run build` | Build the web app, then `./bin/crowsnest` with version ldflags |
| `mise run test` | Go tests with `-race`, then Vitest (single run) |
| `mise run test:integration` | Go tests against real herdr in throwaway `crowsnest-test-*` sessions (created, stopped and deleted by the tests): the socket client, the pane and TUI terminal bridges, and the fleet cache following herdr's events. Also self-update's real Sigstore check of a published release's bundle (needs the network) |
| `mise run test:claude` | Real-Claude smoke (a few cents): a real orchestrator on haiku in a throwaway session answers one prompt through the HTTP API, then Restart and New conversation; a manual poke with one synthetic reason is delivered through `POST /orchestrator/poke` and answered; and it calls the `ask` MCP tool, the question is answered through the browser API, and the answer arrives in its transcript; and a PNG with a word drawn in it, uploaded and sent with a prompt under the `default` permission mode, is read (no permission prompt) and the word named. Then **the stop test**: three haiku job agents in a scratch git repo, where an undeclared stop is refused by the Stop hook, `done` stops cleanly, and a non-blocking `ask` answered through the browser API reaches the agent, which finishes |
| `mise run fonts:terminal` | Regenerate the terminal's fallback webfonts (`web/src/fonts/terminal/`, committed) with `web/scripts/terminal-fonts.py`: downloads the pinned Nerd Fonts symbols release and Unicode emoji data (checked by SHA-256), reads DejaVu and Noto from the pinned `@fontsource` devDependencies, and writes the unicode-range chunks and their CSS. Run after changing a pin |
| `mise run lint` | `go vet`, golangci-lint, gofmt check, `tsc`, eslint. Read-only |
| `mise run fmt` | gofmt and eslint `--fix` |
| `mise run check` | lint + test: the gate before calling anything done |
| `mise run test:clone` | Clone HEAD into a temporary directory and run `setup` and `build` there: proves a clean checkout builds. Slow (a full `npm ci`) |
| `mise run e2e` | Builds, then the hermetic browser suite (`web/e2e/hermetic`): Playwright against `./bin/crowsnest --demo`, a fresh instance per test (the auth specs with the login on), desktop Chromium 1440×900 plus 375×812 Chromium and WebKit. User journeys for auth, Jobs, Chat, Inbox, Fleet, terminals and talk mode, and every view's 375 px, theme, offline and axe checks. Also in CI. See [docs/TESTING.md](docs/TESTING.md) |
| `mise run e2e:real` | Builds, then Playwright against the real binary (`web/e2e/real`): the Fleet view on `--demo` with no fixtures; on a throwaway `crowsnest-test-*` herdr session the terminals, a tab made through herdr appearing live, a scripted dialog in a pane answered from the Inbox, and the login-on WebSocket refusals. Each spec starts and stops its own instance |
| `mise run e2e:claude` | Opt-in, a few cents (haiku): real Claude through the browser on a throwaway session: chat, a job from the Jobs box run to done, a job's question answered in the Inbox, a nudge on a timeline, `crowsnest ask --wait` from the orchestrator's pane, a real typed draft. Uses your real HOME for Claude's login; scratch files stay under `web/e2e/claude/` |
| `mise run screenshots` | Playwright with fixtures: Fleet screenshots at 375×812 and 1440×900, light and dark, into `web/screenshots/`, plus the 375 px checks (no sideways scroll, 44 px targets) |
| `mise run screenshots:inbox` | (The three `screenshots:*` tasks run `web/e2e/screenshots` in the `screenshots` Playwright project, a fresh demo instance per test.) Builds, then Playwright against `./bin/crowsnest --demo`: the Inbox list and a question (mermaid and a diff) at 375×812 and 1440×900, light and dark, a multi-select, the 375 px checks, and an answer round trip |
| `mise run screenshots:jobs` | Builds, then Playwright against `./bin/crowsnest --demo`: the Jobs board (light and dark), the new-job form, a running, a stuck and a done job at 375×812 and 1440×900, the 375 px checks, and giving a job |
| `mise run screenshots:palette` | Builds, then Playwright against `./bin/crowsnest --demo`: the command palette open, with a query (highlights, Give job), and a job's actions at 375×812 and 1440×900 in light and dark, plus the keyboard shortcuts sheet |
| `mise run screenshots:chat` | Builds, then Playwright against `./bin/crowsnest --demo`: a real prompt round trip in Chat, screenshots at 375×812 and 1440×900, light and dark |
| `mise run e2e:voice` | Talk mode alone (also part of `e2e`). Builds, then Playwright against `./bin/crowsnest --demo` with talk mode on and a fake ElevenLabs (`web/e2e/support/fake-elevenlabs.mjs`, never the real API), Chromium's fake microphone (and WebKit's mock device for the keyboard cases): the mic, the transcript sent, the SAY reply fetched from `/voice/tts` and played with speak mode on and not synthesised with it off, the play button; hold Space with real key events (3 s and 30 s with auto-repeat, a focused button, a quick tap, Option+Space in the composer, blur mid-hold); the Voice sheet (voices, preview, a saved voice and speed); screenshots of Chat recording at 375×812 and 1440×900 |
| `mise run snapshot` | GoReleaser snapshot into `dist/`: the four archives, `checksums.txt` and the Homebrew cask (`dist/homebrew/Casks/crowsnest.rb`); nothing signed, nothing published |
| `mise run image` | Build the devbox image (`deploy/Dockerfile`) for this machine's architecture as `crowsnest:dev`. Needs Docker |
| `mise run publish` | Build the devbox image for amd64 and arm64 and push `ghcr.io/jclement/crowsnest:<version>` and `:latest`. Jeff's step |
| `mise run release` | Interactive tag bump and push (Jeff's step) |
| `mise run dev:reset` | Remove build output, web dist, Vite cache and `.dev/`, and stop and delete the `crowsnest-dev` herdr session (only that one, by exact name) and its crowsnest state directory |

## Releasing

`mise run release` is the only way to cut a release, and it is Jeff's to run. It is interactive:
it reads the latest tag, offers the next patch, minor or major version, creates an annotated tag
and asks before pushing it. The pushed `v*` tag starts `.github/workflows/release.yml`, which runs
`mise run setup` and `mise run check`, then GoReleaser (`.goreleaser.yml`) builds the archives and
checksums, signs `checksums.txt` with keyless cosign (the release job's GitHub OIDC identity;
`id-token: write` is granted to that job alone), attaches them to a GitHub release (a `-rc.N`
style tag becomes a prerelease) and pushes the cask to
[jclement/homebrew-tap](https://github.com/jclement/homebrew-tap). When that succeeds, its
`image` job builds the devbox image for amd64 and arm64 and pushes
`ghcr.io/jclement/crowsnest:<tag>` (and `:latest`, unless the tag is a prerelease). Secrets:
`GITHUB_TOKEN`, and `HOMEBREW_TAP_TOKEN` (a token that can push to the tap; a repository secret
Jeff sets) in the release job only. Never build release artifacts by hand or tag around a broken
pipeline: fix the pipeline.

`mise run snapshot` runs the same GoReleaser config locally with `--snapshot` (no tag, nothing
published) to prove the packaging; the archives land in `dist/`, which is gitignored.

## Files

| Path | Holds |
|---|---|
| `~/.config/crowsnest/config.yaml` | Optional config file |
| `~/.local/share/crowsnest/<session>.db` | SQLite: login sessions (hashed), settings, questions, jobs and their timelines, the journal, and the poke bookkeeping (`kv` key `poke.state`) |
| `~/.local/state/crowsnest/<session>/herdr-server.log` | Output of a herdr server crowsnest started |
| `~/.local/state/crowsnest/<session>/CROWSNEST.md` | The orchestrator's instructions (`--append-system-prompt-file`), rewritten from the embedded template on every start |
| `~/.local/state/crowsnest/<session>/claude-settings.json` | The orchestrator's `--settings` file: `permissions.allow: ["mcp__crowsnest__*"]` and the supervisor's hooks |
| `~/.local/state/crowsnest/<session>/jobs/j_<n>/` | A job agent's `prompt.md` (role prompt and job protocol), `brief.md`, `settings.json` (hooks with `--job j_<n>`, the allow rule) `mcp.json` (with the agent's token) and `agent-token` (the token, for the hooks); each written fresh and read-only (0400) |
| `~/.config/crowsnest/roles/*.md` | Optional role prompts that add to or replace the built-in ones |
| `~/.config/crowsnest/prices.json` | Optional price overrides for the spend estimate (see Spend) |
| `~/.local/state/crowsnest/<session>/mcp.json` | The orchestrator's `--mcp-config`: the `crowsnest mcp` stdio server, its `CROWSNEST_SOCKET` and its agent token (`0600`); `agent-token` beside it holds the same start's token for the hooks |
| `~/.local/state/crowsnest/<session>/agent.sock` | The agent API socket (when `XDG_RUNTIME_DIR` is unset) |
| `$TMPDIR/crowsnest-demo-*/` | `--demo` only: the demo's database, state (the demo agents' transcripts, its agent socket) and orchestrator directory, removed on exit |
| `~/.local/state/crowsnest/mise-install.log` | Devbox image only: the entrypoint's last `mise install` |

## License

MIT. (C) 2026 Jeff Clement. See [LICENSE](LICENSE).
