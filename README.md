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

**The manual.** How to use crowsnest, and the ideas behind it, are in the built-in manual:
[docs/manual](docs/manual/README.md), the same pages the app shows under **Help** (on a phone
under More, `g` then `m`, or a question typed into the command palette, such as "how do I land a
job"), with a **?** beside each view and confusing card. Start with [Start
here](docs/manual/start-here.md). This README is the reference for installing, configuring and
building it; the manual explains using it:

- [Now and the attention queue](docs/manual/concepts/attention-and-now.md): one queue of what
  needs you, one number everywhere, answerable in place (DESIGN.md 22).
- [Jobs](docs/manual/concepts/jobs.md) and [the supervisor](docs/manual/concepts/supervisor.md):
  states, nudges, stuck causes and the one action each needs (DESIGN.md 6).
- [Questions, approvals and terminal dialogs](docs/manual/concepts/questions-approvals-dialogs.md),
  risk levels and receipts, and [answering them](docs/manual/how-to/answer.md) from Now, the tray,
  the decision packet or the lock screen (DESIGN.md 8.7, 23).
- [Approval rules](docs/manual/how-to/projects.md#approval-rules), [rescue without a
  terminal](docs/manual/how-to/rescue.md), [landing](docs/manual/how-to/review-and-land.md) and
  [acceptance](docs/manual/concepts/acceptance.md).
- [What crowsnest protects, and what it does not](docs/manual/safety.md).

## Getting started

```sh
brew install jclement/tap/crowsnest   # brings herdr; have Claude Code (or Codex) installed and signed in
crowsnest                             # prints a one-time setup link and opens it in your browser
#   choose a password → a short checklist, already mostly green → Start → Chat, with a hello
crowsnest doctor                      # anything not green: what is missing, and the command that fixes it
```

That is all: no flags, no config file. Everything below is for when you want more.

**What happens on that first start.** crowsnest comes up even when herdr or every agent is
missing, and says so in one line (`herdr 0.9.5 ✓  claude 2.1.332 ✓ signed in  codex not found`).
The setup page asks for a username and password; then a checklist shows what it found (herdr,
Claude Code and whether it is signed in, Codex, the orchestrator's folder) with the exact install
or sign-in command for anything missing, and notices by itself when you have fixed it. Claude's
sign-in needs a terminal, so the checklist opens one in the page with `claude` typed in. With both
Claude Code and Codex it asks which runs the orchestrator. Start starts the orchestrator; Claude's
one-time "do you trust this folder?" question appears right there as a card with its buttons;
then you are in Chat and the orchestrator says hello. Later, the account menu has **System** (the
same checks live, versions, uptime, recent errors, a redacted "Copy diagnostics" for an issue)
and **Settings** (models, engine, permission mode, pokes, compaction, Remote Control, attachments,
notify URL, protected hosts and branches).

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

crowsnest needs [herdr](https://herdr.dev) on `PATH` to do anything useful (`brew install
herdr`, or `curl -fsSL https://herdr.dev/install.sh | sh`), and Claude Code or Codex; it starts
without them and tells you what is missing.

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

## From source

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

crowsnest needs [herdr](https://herdr.dev) on `PATH`, but starts without it: the app explains,
and crowsnest connects as soon as herdr appears (it looks every five seconds), with no restart.
It connects to the herdr session named by `--session` (default `crowsnest`) and, if nothing
answers, starts `herdr --session <name> server` detached, logging to
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
exists. It waits (state `waiting`, with the reason) while the first-run checklist is unfinished,
herdr is not connected or no agent is installed, and starts by itself when that changes. The
first start in a new directory stops at Claude's folder-trust question; the checklist, the Inbox
and Chat show it as a card with its buttons. Conversations are read from
Claude's JSONL transcripts in `$CLAUDE_CONFIG_DIR` (default `~/.claude`).

The instance has a name (`--name`, else the short hostname, such as `quark`): it is in the
header, the page title, push notification titles and `/auth/me`. The orchestrator is started
with Claude Code's Remote Control on, named `<name> crowsnest` (plus the session when it is not
`crowsnest`), so the same conversation is in the Claude app and claude.ai/code. A login that
cannot use Remote Control (an API key, a plan without it, an organisation policy) just starts
without it; the orchestrator's status says whether it came up. `--no-remote-control` turns it
off; `--job-remote-control` turns it on for job agents too (`<name> <job id> <title>`).

Crowsnest captures each standing agent's Remote Control session link (from Claude's banner, or its
transcript) and shows **Open in Claude** on Chat's header, a lead's card and Now's rows whenever a
current link is known; otherwise a short line says which session name to look for in the Claude
app. Its "?" explains the split: crowsnest for the overview, decisions, approvals, many projects
and landing; the Claude app for a long conversation with one agent. Work you give an agent in the
app is recorded as a crowsnest job (the agent calls `job_adopt` and says the job id), so it is on
the board and supervised. Under `--demo` the links are made up and lead nowhere.

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
header: jobs, questions, agents, views, sheets, actions and the manual in one box; whatever you
type is also offered as **Give job: …** and **Send to orchestrator: …**. Every shortcut is in the
manual's [keyboard shortcuts](docs/manual/reference/keyboard-shortcuts.md), generated from the
app's own table (`?` in the app shows the same list).

For development, `mise run dev` builds the single binary and runs the real thing on one port
(http://localhost:2769) against its own herdr session. `mise run dev:hot` adds Vite with hot reload
on :5173 (Vite proxies `/api` to Go), and `mise run dev:demo` runs the scripted demo with no herdr
or Claude.

To work on the UI with no server at all, open http://localhost:5173/fleet?fixtures=1 on the Vite
dev server: every API call is answered in memory from `web/src/lib/fixtures.ts` (a fleet with
agents in every state and two dialogs), and a "fixtures" chip shows in the header. `?fixtures=0`
turns it off. Fixtures mode exists only in development builds; it is compiled out of
`mise run build`.

On a phone over Tailscale, publish it from the System page ([Publish to your
tailnet](#publish-to-your-tailnet)), which also gives the HTTPS secure context the microphone
needs for talk mode, or bind the tailnet address:

```sh
./bin/crowsnest --bind tailnet --username jeff --password-file ~/.config/crowsnest/password
```

**Notifications on your phone (Web Push).** Account menu → **Notifications** → *Turn on for this
device*. Push needs a **secure context** (`localhost`, `tailscale serve`, or
`--tls-cert`/`--tls-key`), and on an iPhone the app added to the Home Screen. The server makes its
VAPID key pair on first start and keeps it in the database; it POSTs only to public `https://`
push-service addresses. The manual's [Reach it from your phone](docs/manual/how-to/phone.md) and
[Notifications](docs/manual/reference/notifications.md) cover the kinds, the buttons and routing.

**Talk mode.** Start crowsnest with an ElevenLabs API key (`ELEVENLABS_API_KEY`, or
`--elevenlabs-api-key-file`; prefer either to the flag, which `ps` shows) and Chat gets a
microphone and speak mode (each reply's `SAY:` sentence spoken). The key never leaves the server;
the browser can only ask to hear texts the server holds (by id). Without a key every `/voice`
route is a 404. How to use it, and speak mode vs the mic: [Talk and
listen](docs/manual/how-to/voice.md).

## Doctor, System and Settings

`crowsnest doctor` checks, read-only and in a few seconds, everything crowsnest depends on and
prints one line per check with the fix for anything that is not fine: herdr (installed, at least
0.9.5, the session reachable: it never starts one, it says "crowsnest will start it"; herdr's
first-run screen), the agents (Claude Code and Codex: where, which version, signed in, from their
own `auth status` / `login status`, never a model call), what the orchestrator and jobs would run
on, the Claude Code flags and hooks crowsnest uses, the orchestrator folder's trust, the
directories, the database (opened read-only), the agent socket's path length, the port (free, or
already a running crowsnest), what the bind resolves to, the secure context voice and push need,
git, disk, the clock, Tailscale, optional features, where each setting came from, and
`CROWSNEST_*` variables that look like typos. Against a running instance it also asks the
instance, over its agent socket, for its orchestrator state, the hooks it has seen fire and its
last errors. `--json` for scripts; `--deep` also makes one tiny real call per agent ("reply OK",
a few tokens, labelled as such) and checks the ElevenLabs key. Exit 0 when nothing failed
(warnings included), 1 when something did, 2 for a usage error.

The app's **System** page (account menu → System, or the palette) is the same report from the
running server, with Re-check, versions, the agents and which runs what, uptime, recent warnings
and errors, paths, the machine (CPU, memory, load, disk, each agent's process tree), and **Copy
diagnostics**: the report as text with no secrets or tokens and your home directory as `~`, for
pasting into an issue. The header carries a live machine strip (CPU with a one-minute sparkline,
memory, load against the cores, and from wide screens free disk and working agents), sampled every
two seconds only while a browser is connected; in a container with cgroup limits the figures are
the container's.

**Settings** (account menu → Settings) changes what makes sense to change while crowsnest runs:
the orchestrator's model, the job model, the default engine, the permission mode, Remote Control
(the orchestrator's and jobs'), the poke interval, compaction and the daily fresh start,
attachment limits, the notify URL (write-only), protected hosts and branches, and whether to
publish to your tailnet. Each says where its
value comes from and when a change applies (at once, when the orchestrator restarts, or when
crowsnest restarts); every change is journalled. A setting given by a flag or an environment
variable is read-only there ("set by the environment: change it there"): precedence is flag,
then environment, then a value saved in the app, then the config file, then the default. The
login, the bind, the session and the data directory are never changed from a browser.

**Appearance** (account menu → Appearance, the Settings sheet, or the palette) picks a theme and,
separately, the mode (System, Light or Dark; the header's toggle cycles the mode). Five themes
ship, each a light and a dark partner: Crowsnest (the house theme), Phosphor (a green-screen
terminal, all in mono), Deep sea, Ember and High contrast (every text colour at WCAG AAA). The
choice is kept per browser; **Use for new browsers** makes a theme the default for browsers that
have not chosen (the `theme` setting, also `--theme` / `CROWSNEST_THEME`). **T** cycles themes
from anywhere outside a text field or terminal. Adding a theme is one file in `web/src/themes/`
(DESIGN.md 12.10).

## On your tailnet with Tailscale

Three ways to reach crowsnest from your phone, best first.

### Publish to your tailnet

crowsnest can run `tailscale serve` for you. When Tailscale is installed and signed in on the
machine, the **System** page (and the first-run checklist, under *Reach it from your phone*) shows
a **Publish to your tailnet** card with the exact address it will create,
`https://<machine>.<tailnet>.ts.net`, and what that means: anyone on your tailnet who can reach
this machine gets crowsnest's sign-in page there; the login still stands in front of everything.
Press **Publish** and the card shows the address as a link, a QR code for the phone (drawn by
crowsnest from the address, nothing fetched), **Stop publishing**, and the state: publishing,
stopped, or failed with the reason and the fix.

```sh
crowsnest --tailscale-serve                # or CROWSNEST_TAILSCALE_SERVE=true, or Settings → Tailnet
export CROWSNEST_TRUSTED_PROXY=loopback    # so the login limiter sees each client, not one proxy
```

- **Remembered.** Publishing is the `tailscale-serve` setting: the card saves it, every start
  publishes again, and the start-up log (and the terminal) print the address. Flag, environment,
  app, file: the usual precedence. A flag or variable that holds it on makes the card read-only;
  Stop publishing then stops it until crowsnest restarts.
- **Lives exactly as long as crowsnest.** It runs `tailscale serve --https=<port>
  http://127.0.0.1:<port>` in the foreground, as a supervised child in its own process group,
  never `--bg`. A stop, a crash or a `kill -9` of crowsnest ends it (a watchdog notices crowsnest
  is gone through a pipe only crowsnest holds), and Tailscale then drops the address: nothing
  persistent is left in Tailscale's config. If it stops by itself while publishing is on, crowsnest
  restarts it with backoff.
- **Never clobbers.** It reads `tailscale serve status --json` first and never touches a port
  another serve config uses: 443, else 8443, else 10000 (or the one `--tailscale-serve-port`
  names); if none is free it refuses and says why. A serve config that already proxies to
  crowsnest (one you made by hand) is reported and left alone.
- **Never public.** It never uses Funnel, `reset` or `off`: every Tailscale command line it can
  run is on an allowlist of four (`version`, `status --json`, `serve status --json`, the
  foreground serve).
- **Needs a login.** With `--no-auth` it refuses (the flag is a startup error), because a
  localhost crowsnest with no login would become a shell for the whole tailnet. A fresh instance
  in first-run setup publishes only once the owner exists: the setup link is never on the tailnet.
- **When it cannot**, the card says exactly what to switch: Tailscale logged out or not running,
  MagicDNS or HTTPS certificates off (Tailscale admin console → DNS), Serve not yet enabled for the
  tailnet (with Tailscale's own link), a CLI older than 1.52, or (on Linux) a user that is not the
  Tailscale operator (`sudo tailscale set --operator=$USER`). On macOS the app's CLI is found in
  `/Applications/Tailscale.app` when `tailscale` is not on `PATH`.

Then, on an iPhone, open the address in Safari, Share → **Add to Home Screen**, open it from
there, and turn notifications on in the account menu.

To look at it, or undo it, by hand: `tailscale serve status` shows the foreground session while
crowsnest publishes; stopping crowsnest (or Stop publishing) removes it. crowsnest never needs
`tailscale serve reset`, and you should not need it either.

### `tailscale serve` by hand

The manual way still works, and is what to use if you want the address to outlive crowsnest:

```sh
tailscale serve --bg 2769                 # once; a persistent config Tailscale keeps
export CROWSNEST_TRUSTED_PROXY=loopback
crowsnest
```

crowsnest finds the MagicDNS name at start (`tailscale status --json`, two seconds at most) and
allows it as a host (`--no-tailscale-detect` turns that off). Undo it with `tailscale serve
--https=443 off`. While such a config proxies to crowsnest, the card says so and does not publish
a second copy.

### `--bind tailnet`

crowsnest listens on its Tailscale address directly, plain HTTP over WireGuard: simpler, but
without the secure context, so no microphone or Web Push.

### Sign in with Tailscale

Off by default. Start crowsnest with `--tailscale-auth` (`CROWSNEST_TAILSCALE_AUTH=true`) and
`tailscale serve` signs you in by your tailnet identity: no password to type on the phone.

1. Start with `--tailscale-auth`, and publish (the card above, or `tailscale serve` by hand).
2. Open `https://<machine>.<tailnet>.ts.net`, sign in with the password once, open System,
   **Sign in with Tailscale**, and press **Allow me (you@github)**. Add others by login if you
   want (`friend@github`); remove them the same way.
3. From then on a browser that comes through `tailscale serve` as an allowed login is signed in.

crowsnest believes the `Tailscale-User-Login` header only when the request comes from loopback
(serve runs on this machine), a `tailscale serve` config on this machine proxies that Host to
crowsnest's port (checked with `tailscale serve status --json`, cached 30 s), and the option is
on. Serve removes any copy of the header a client sends and sets it only for tailnet users (not
tagged devices or Funnel). A session made this way stays valid only while each request still
arrives through serve for that login and the login is still allowed, so removing someone ends
their sessions at once. Only a browser signed in with the password can change the list. Password
sign-in always remains. What it does not protect against: any program on this machine can open a
loopback connection and send the header (it already runs as you; on a shared machine, leave the
option off). Another local proxy is the same kind of risk: a request carrying Cloudflare's or a
`Forwarded` header is never believed, and `--tailscale-auth` refuses to start with
`--trusted-proxy-header cf-connecting-ip`. Do not run another loopback proxy (Caddy, nginx) that
forwards a client's Host beside this option. DESIGN.md 4.20 and 4.22 have the threat notes.

## Backup and restore

```sh
crowsnest backup                         # crowsnest-<session>-<time>.tar.gz here; --out FILE to choose
crowsnest restore --verify FILE          # check it, write nothing
crowsnest restore FILE                   # into this machine's data directory; --force over existing data
```

A backup is one `.tar.gz`: a manifest (crowsnest and schema versions, every file's size and
SHA-256, and what is not included), a consistent copy of the database made with SQLite's
`VACUUM INTO` (safe beside a running crowsnest), every attachment, and the files beside the
config that steer agents (`roles/*.md`, `tasks/*.md`, `risk.yaml`) and the orchestrator's
`tasks/`. It never holds the password in clear or any token: login sessions, agent tokens, API
tokens, tokens for other machines, notification action tokens, the Web Push key and
subscriptions and the notify URL are removed from the copy. Not included: Claude Code's
transcripts, herdr's state (sessions, panes, scrollback), the repositories and worktrees jobs
used, and the config file (it may hold the password).

`restore` checks every file against the manifest before writing anything, refuses a backup made
by a newer schema, refuses a data directory that already holds a database (and a running
crowsnest) unless `--force`, and then keeps the replaced database beside it
(`<session>.db.before-restore-<time>`, scrubbed of sessions and tokens like a backup). The
backup's approval rules arrive revoked (add back the ones you want), and its role files are marked
unread. A project's custom check command comes back as automatic, the backup's command shown on
the project page until you use it or keep the current check, and the backup's instruction layers
reach no agent until you read and apply each one in Instructions. An existing role, task or risk file is left alone unless
`--force`. The restored instance **starts paused**: crowsnest first compares the restored jobs
with the panes herdr really has (the journal says which still have one), and the pause switch
holds every job start, restart, retry, poke and scheduled task until you resume (account menu,
or the pause banner). Every browser signs
in again; every device turns notifications on again. A restore is all or nothing: every file is
prepared beside its destination first, a replaced file is kept as `.before-restore-<time>`, and a
failure part-way puts everything back as it was. A backup listing more than 32 GiB or 200,000
files is refused. Treat a backup from somewhere else as you would a script: it carries its own
login, role, task and `risk.yaml` files.

**Automatic backups** are off by default: `--backup-every 24h` (or Settings, Backups and
storage) writes `auto-<session>-<time>.tar.gz` into `<data-dir>/backups` and keeps the newest
`--backup-keep` (7); none is made while crowsnest is paused. **Download backup** (Settings, and the System page's Storage card) streams a
fresh one to the browser.

**Upgrades.** Migrations only go forward. Before a start that will change the database's schema,
or the first start of a different crowsnest version, crowsnest copies the database to
`<data-dir>/backups/pre-migration-<session>-<time>-s<schema>-<version>.db` (the newest three are
kept), with the same credentials removed as a backup. A database written by a newer crowsnest is refused with what to do (run that version
again, or restore a backup). After an update the System page shows "Updated from vA to vB" with
a link to the release notes until dismissed.

## Retention

Settled jobs, their timelines, questions, approvals, journal rows, spend detail and attachments
pile up; crowsnest tidies them once a night (03:00 local, or whenever none ran for two days; not
while paused):

- **Archive** settled jobs after `--archive-after-days` (14): they leave the boards and stay
  searchable (`GET /api/v1/jobs?archived=1&q=text`).
- **Delete** settled history after `--delete-after-days` (180; `0` never): settled jobs with
  their timelines, questions, approvals and what their agents loaded; settled questions and
  approvals of no job; journal rows; per-call spend detail (daily totals stay); attachments no
  longer needed, except evidence (an agent's `result` and `ask` files), which is kept; uploads
  never sent (after a day); notification devices that keep failing (after 30 days).
- **Never without asking**: a settled job that worked on its own branch or worktree may hold
  work nobody has landed. It is listed as held and deleted only when you tick it in **Clean up
  now**; the nightly run never deletes it. A job that is not settled is never touched.
- **Job folders** (`<state>/jobs/j_<n>`: the brief and files given to the agent) go once their
  job is archived or gone. Worktrees are not touched here: the landing sweep removes them, since
  it knows what landed.

The System page's **Storage** card shows what uses how much (database, attachments, job
folders, backups), the rows per table with the oldest, the last clean-up, and **Clean up now**,
which shows exactly what it would do first.

## Machines

Run one crowsnest per machine, each with its own herdr, and give each a name (`--name`). The
System page's **Machines** card shows the others side by side:

1. On the other machine, open System, Machines, and **Mint a token** (name it after the machine
   that will read it). Copy it: it is shown once.
2. Here, add that machine with its address on the tailnet (`https://bedev.tail1234.ts.net`, or
   plain `http://` to a tailnet address) and the token.

Each row shows what needs you there, its running jobs, whether it is paused, CPU and load, and
links to it. A read token opens that machine's `GET /api/v1/summary` and nothing else (no
terminal, job, question or setting); tokens are kept hashed and can be revoked. Summaries are
fetched by this server (five seconds at most, no redirects), so the browser never sees a token.
There is no remote control of another instance: sign in there.

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
| `crowsnest pause [--until HH:MM] [--reason …]` / `crowsnest resume` / `crowsnest pause --status` | Pause or resume crowsnest's own automation (no pokes, nudges, scheduled tasks, new starts or restarts; agents keep running). Works from your shell with the `--session` in effect; with the server down `pause` writes `~/.config/crowsnest/paused` |
| `crowsnest heavy [--status] -- <command…>` | Run a build or test suite through the heavy-command gate (two at a time by default, niced, parallelism capped). Uses an outside `heavy` on PATH when there is one (`CROWSNEST_HEAVY_DEFER=0` turns that off). Exit 75: no slot came free |
| `crowsnest doctor [--json] [--deep]` | Check this machine can run crowsnest and print the fix for anything missing (Doctor, System and Settings). Read-only; `--deep` makes one tiny real call per agent. Exit 0 nothing failed, 1 something did |
| `crowsnest open` | Open the running crowsnest in your browser (the same opener rule as `--open`); prints the URL either way |
| `crowsnest backup [--out FILE]` | Write a backup (see Backup and restore): safe beside a running crowsnest |
| `crowsnest restore FILE [--force] [--verify]` | Check a backup and put it in this machine's data directory; it starts paused. Refused over existing data or a running crowsnest unless `--force`; `--verify` only checks |
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
| `crowsnest mcp` | A stdio MCP server exposing `ask`, `get_question`, `say`, `job_update`, `done`, `fail`, `job_adopt` (the orchestrator or a lead records work given in conversation as a job it holds, and gets its id), `job_create` and `job_get`, proxied to the socket. Claude starts it from the `mcp.json` crowsnest writes. `ask` with `wait=true` returns the whole answer and note; `ask` with `job_id` (the orchestrator, for the job it holds) files the question under that job, and the socket refuses a job the caller does not hold |
| `crowsnest project list`, `crowsnest project get <slug>` | The projects with their leads and job counts; one project with its defaults and live jobs |
| `crowsnest job update "doing now" [--state blocked --reason "…"] [--waiting]` | The job protocol for agents without MCP (Codex) and scripts: a heartbeat with a progress line; `--state blocked` with a reason, or `running`; `--waiting` declares that nothing is left the open questions do not block (refused with no open question). The job is `--job j_<n>` or `CROWSNEST_JOB` |
| `crowsnest job done --result "…" --verified "…" --not-verified "…" [--attach file]` | The job is finished, with evidence (repeat the list flags; `none` for an empty list; `--result-file` reads the result; `--attach`, repeatable, attaches a file from your working or job directory to the result) |
| `crowsnest job fail "reason"` | It cannot be finished |
| `crowsnest job get [j_<n>] [--json]` | The job as text for an agent (brief, state, progress, questions and answers), or the full record with its timeline |
| `crowsnest job create "brief" [--dir] [--role] [--done-when] [--engine] [--model] [--isolate]` | Delegate a child job; the caller's job is its parent. Inside the caller's own directory, with its engine, it starts at once; another directory, no directory, another engine or `--isolate=false` from an isolated job waits for the human to allow it in the Inbox ("own directory" is the one crowsnest recorded, resolved, when it opened the caller's pane; a job given in `~` or `/` grants none). Only a plain Allow starts it: an Allow with a note cancels it, and the note reaches the caller. At most 3 levels deep, 8 children per job, 16 live agent-created jobs, 2 requests per caller waiting for the human. The orchestrator may use any directory the human has given a job before (not `~` or `/`), needs the human for Codex, and may have 8 live jobs of its own while it holds none |
| `crowsnest job list [--state …]` | Jobs, newest first |
| `crowsnest hook <event> [--job j_<n>] [--socket PATH] [--token-file PATH] [--rules PATH]` | The Claude Code hook entry point crowsnest writes into each agent's `--settings` (`stop`, `session-start`, `notification`, `stop-failure`, `permission-denied`, `session-end`, `post-tool-use`, `permission-request`, `pre-tool-use`): stdin JSON to the server, its reply on stdout. `permission-request` waits for the human's decision on the approval it posts (hook timeout 86400 s; crowsnest's approvals expire after 12 h); `pre-tool-use` is the guard, decided from `--rules` (`guard.json`) with no server round trip. Fails open: any failure prints nothing and exits 0, which for `permission-request` leaves Claude's own dialog |

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

Instruction layers: standing instructions you manage in the app, added to the prompt file of every
agent crowsnest starts, in order global (every agent), role (the built-in roles, editable, with
reset to the built-in text; a role file wins and shows read-only), project, then the job's brief.
Each change is a revision (the newest 20 kept, any one restorable). Crowsnest never edits your
`AGENTS.md`, `CLAUDE.md` or skills; it records which of those files Claude loaded (the
`InstructionsLoaded` hook) by path only, and tells you when an agent's layers changed after it
started (DESIGN.md 21.3).

## Projects and leads

A project is a repository's main checkout: a name, its directory, defaults for the jobs given in
it (role, engine, model, worktree or not), its own instruction layer, and optionally a **lead**:
a long-lived Claude agent in that checkout, kept exactly as the orchestrator is (same hooks, MCP
tools, SAY line, recovery), with Remote Control named `<instance> <project>`. Make them in the
**Projects** view ("New project" has a folder picker, Browse…, or takes a typed path); archive, never delete.

**The folder picker** browses your home, the directories projects and jobs already use, and any
`--browse-root` (`/srv`, `/Volumes`; `/` only when named), read-only and only from your signed-in
browser (not a read token, not an agent). It floats git repositories to the top, takes a filter
or a pasted path (`/…`, `~/…`), and before you choose shows what the folder is: "git repo on
GitHub, branch main, has a check task, AGENTS.md present", what Land can do with its remote (pull
requests on GitHub only), and a warning when it is not a repository, is inside another project,
or is your home folder. Choosing one prefills the project's name and check command. It is also
behind the job form's Where and the palette's "New project in…".

- A job given **to the project** with no directory goes to its lead, which plans it and may
  delegate child jobs inside the project's directory (anything wider asks you first). Given
  **as a job**, or with a directory inside the project, a job agent runs it directly, as before.
  A job whose directory is inside a project joins it by itself; others are "Unfiled".
- The lead starts when you press **Start lead**, or by itself when the project is opened or
  given a job (the project's "lead starts" setting), and comes back after a crowsnest restart
  until you stop it.
- The orchestrator stays the front door: it sees every project (`project_list`, `project_get`,
  `crowsnest project list|get`) and can hand one work. `--no-orchestrator` works with leads alone.
- **A project's lead owns its jobs.** When one of them finishes, gets stuck or has a question
  settled, only the lead is poked. The orchestrator hears the outcome of a job it handed to the
  project, and escalations: the project has no lead running, a job has been stuck or waiting on
  you for 30 minutes, or the lead itself failed. A lead also gets the timer poke (its project's
  open work), compaction at two thirds of `--compact-at`, the `--fresh-at` daily new
  conversation with its own hand-over, and its conversation is metered for spend (`lead:<slug>`).
- The Jobs board filters by project (remembered per browser); Chat can talk to the orchestrator
  or any lead (`/chat?target=lead-<slug>`); each standing agent can speak in its own voice,
  opted into per agent, with spoken replies queued so two never overlap.

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
- **A task file that starts a job (`as: job`) or pokes a lead (`project:`) runs only after you
  approve it.** Agents can write files, including the orchestrator's `tasks/` and
  `~/.config/crowsnest/tasks`, so such a file is listed as **new or changed: review**, with its
  text, until you press **Approve as it reads now** in the Schedule view. Any edit asks again, and
  Run now does not approve. Poke tasks for the orchestrator and schedules made in the app need no
  review. A restore forgets every approval.
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

## Landing, limits, the pause and the digest

- **Review and land.** A job that ran in its own worktree has a **Review** tab, while it runs
  and when it is done: commits ahead of its base, whether the tree is clean and the branch
  pushed, the live diff viewer (below), and beside it the agent's result, evidence and
  walkthrough, and the review rounds.
  **Merge** (into the base branch in the main checkout; `--merge merge|ff`), **Push and open
  PR** (GitHub with `gh`; no reviewers assigned; other hosts are pushed only) or **Discard**,
  each after a confirmation saying exactly what happens. crowsnest's own git calls run none of
  the repository's hooks, fsmonitor, external diff or textconv commands, so **no hook runs on a
  Land** (a merge driver, a filter, ssh and gpg still can; DESIGN.md 4.22). Then the agent is stopped, the
  worktree and its workspace removed through herdr (a dirty tree is refused, never forced) and
  the branch deleted when merged or discarded. "N to land" above the board lists finished jobs
  whose worktrees are still around. Agents can only *ask* to land (`job_land`): you decide.
- **Review gate.** Builder jobs in a worktree are reviewed before they count as done
  (`--review auto`: the other engine when installed, else the same one): a reviewer job runs
  read-only in the same worktree, its findings go back to the builder, at most two rounds, then
  done, or it needs you.
- **Limits.** At most `--max-running-jobs` job agents at once (a quarter of the cores, at least
  two; leads and the orchestrator not counted); the rest wait queued with the reason and start
  in order, or you press **Start anyway**. New starts also wait while the load per core stays
  above `--load-hold` for a minute. Agents run builds and suites through `crowsnest heavy`. The
  board's "2 of 4 running" opens the Limits sheet.
- **Pause.** Account menu → Pause automation (or the palette, or `crowsnest pause`): agents keep
  working, crowsnest stops acting by itself, a banner says so until you resume.
  `~/.config/crowsnest/paused` pauses it too.
- **Presence.** Your open tab tells crowsnest whether you are here, maybe, or away (only those
  states are kept): here, things reach you in the tray and never as a push; away, Web Push and
  the notify URL. Account menu → Do not disturb holds non-blocking notifications until a time.
  Agents see `human=here|maybe|away` on their pokes.
- **Daily digest.** One switch and a time at the top of the Schedule sheet (off by default), or
  "Digest now" in the palette: what finished, what waits on you, each lead's day, the spend, what
  is next, written from crowsnest's records (by the orchestrator when it is up), as a notice in
  the Inbox; past digests under "Past digests".

## The diff view

Every checkout has a live, syntax-highlighted diff (DESIGN.md 6.21), at any time:

- **A job**: its Review tab (a worktree) or Changes tab (a job in a folder of its own), while it
  runs. Switch between **All changes**, **Commits on this branch**, **Uncommitted** (untracked
  files included) and **Since you viewed**. It follows the agent: a few seconds after the agent
  edits a file the view updates by itself (Refresh is there too; git is read at most once every
  4 seconds per job, and only while someone has the view open).
- **A project's main checkout** (the lead's work): the lead's Chat and the project page have
  **Changes**: uncommitted work and commits since the upstream (or the default branch), since the
  lead's conversation started, or since a ref you pick. The project page lists every job's
  checkout with its diffstat.
- **The viewer**: unified or split (split from a wide screen, remembered), line numbers both
  sides, the words that changed emphasised, expand context 20 lines at a time or the whole file,
  collapse and expand per file and all, sticky file headers, a file list with status, diffstat
  bars and a filter, whitespace and wrap toggles, copy path. Keys: **j/k** next and previous
  file, **n/p** change, **v** viewed, **f** full screen (Esc leaves it). Lockfiles, generated
  code, images and changes over 3,000 lines start collapsed, with the reason; binary and
  untracked files over 2 MB are named, not shown.
- **Viewed**, as in Azure DevOps: tick a file and it collapses; **Hide viewed** takes it off
  the list ("12 of 30 viewed", and a bar on the job's card). The tick is tied to the file's
  content: when the agent changes the file again the tick clears by itself and the file comes
  back flagged "changed since you viewed it", with **Show since viewed** for only what changed.
  Ticks live on the server (every device, survives restarts) and go when the job lands or is
  discarded. The Land card and its confirmation say how many files are not viewed; Settings →
  "Require all files viewed before Land" (`--require-viewed-before-land`, off) makes it a refusal.
- **Full screen** is its own page, `/diff?job=j_12` or `/diff?project=<slug>`: the app's header,
  nav and tray hidden, so it opens in its own tab ("Open in a new tab") and deep-links; entering
  and leaving keeps the scope, the file and where you were. On a phone it shows one file at a
  time: the file list as a sheet, Previous, Viewed and Next at the bottom, swipe between files.
- The palette has **Changes: <job>** and **Changes: <project>**.
- Read-only git with crowsnest's protective options (no hooks, external diff, textconv, pager
  or fsmonitor), and the repository's clean/smudge filters switched off by name: the work tree is
  read through a temporary index, never the agent's own.

## Acceptance, budgets and waits

- **Done is checked, not believed.** When an agent working in a git checkout calls done,
  crowsnest refuses a dirty tree, and a worktree branch with no commits unless the agent says
  why. Then it runs the project's **check command** itself, under `crowsnest heavy`: automatic
  is `mise run check` when the checkout's mise.toml has a `check` task; set another, or `none`,
  in the project's "Acceptance and budget" card. A pass makes the job **Accepted** (then the
  review gate looks at that commit); a failure goes back to the agent with the output, up to
  `--check-rounds` times, then the job needs you. No check command: **Unverified**. The check is
  pinned to its commit: a later commit makes it **stale**, and a base branch that moved on makes
  the job **need a rebase** ("Ask an agent to rebase"). Land refuses both unless you tick the
  override. What Accepted proves: the project's own check passed on that commit in a clean tree.
  The branch can change that check, so the card names any change to its check definition; it is
  not independent evidence, and the reviewer and you are the independent part. By default the
  check runs in the job's working tree (with its dependencies), the agent's tools held meanwhile
  and the tree's content compared before and after; the card says "checked in the working tree".
  Tick **Isolated check** on the card (with an optional **prepare command**, such as `npm ci`)
  to have it run in a temporary copy of exactly the commit instead: "checked in an isolated copy".
- **Holds.** A budget reached, a check that still fails, a review that still objects, an approval
  waiting, a usage limit: each is a hold on the job that only you (or crowsnest's own recovery)
  lift. Nothing the agent declares moves it out of that.
- **Budgets.** Per job (the job view's Raise), per project (dollars, minutes) or in Settings
  (`--job-budget-usd`, `-tokens`, `-minutes`, `-turns`); delegated jobs count toward their
  parent. At 80% the card warns and the agent is told; at 100% the agent is asked to stop at a
  safe point and the job needs you: raise it, let it finish the step, or stop it. Nothing is
  killed. `--daily-ceiling-usd` bounds jobs with no budget and holds new starts once today's
  spend reaches it; `--orchestrator-ceiling-usd` and `--lead-ceiling-usd` hold the standing
  agents' automatic pokes. Spend that is not measured (Codex) shows as "not measured".
- **Usage limits.** When one agent hits its plan's usage limit, one banner counts down to the
  reset; that engine's new starts, nudges, retries and pokes wait, then resume one at a time.
- **Spinning.** An agent repeating the same few tool calls for ten minutes or more with no change
  to its files or its failing tests is flagged "Busy, but not getting anywhere", with the evidence.
- **Waits.** `blocked` needs a wake-up (`--recheck-in 30m`, `--recheck-at`, `--until-job j_N`,
  `--until-human`); crowsnest wakes the agent then. Every live job says who it waits on; the
  System page lists any that do not. An agent that ends its turn asking you something in prose is
  told to use `ask`, and stuck or waiting cards show what it last said.
- **Answers.** The Inbox shows whether an answer is stored, held, uncertain, delivered or
  received by the agent; "Did it arrive?" checks its conversation before anything is typed again.

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

Precedence: flag, then environment, then a value saved in the app's Settings (only for the
settings listed there), then `~/.config/crowsnest/config.yaml` (keys are the flag names without
the leading `--`, e.g. `username: jeff`, `bind: [localhost, tailnet]`), then the default. A flag or
variable wins because whoever starts the process is being explicit; the app's value wins over the
file because a change made in the app should take. This table stays the way to configure an
unattended install. List values (`--bind`, `--allowed-host`) repeat as flags, are comma-separated in the
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
| `--permission-mode` | `CROWSNEST_PERMISSION_MODE` | `auto` | Permission mode of the orchestrator and job agents (`acceptEdits`, `auto`, `bypassPermissions`, `dontAsk`, `manual`, `plan`; `default`, `manual`'s older name, is taken too) |
| `--poke-interval` | `CROWSNEST_POKE_INTERVAL` | `15m` | Timer poke interval; `0` disables timer pokes |
| `--no-orchestrator` | `CROWSNEST_NO_ORCHESTRATOR` | `false` | Serve the UI over the session without starting Claude |
| `--engine` | `CROWSNEST_ENGINE` | `auto` | Agent for the orchestrator and for jobs that name none: `auto` (Claude when signed in, else Codex), `claude` or `codex`. Editable in Settings |
| `--no-tailscale-detect` | `CROWSNEST_NO_TAILSCALE_DETECT` | `false` | Do not add this machine's Tailscale MagicDNS name to the allowed hosts at start (it is detected with `tailscale status --json`, so `tailscale serve` works without `--allowed-host`) |
| `--open` | `CROWSNEST_OPEN` | `auto` | Open a browser at start on the setup link (or the app): `auto` only from a desktop terminal, `always`, `never` |
| `--name` | `CROWSNEST_NAME` | the name chosen at setup or in the Account sheet, else the short hostname | This instance's name (letters, digits, `-`, `_`; up to 32): in the header, page title, notices, `/auth/me` and Remote Control. Given here, it wins over the stored one and the Account sheet shows it read-only |
| `--no-remote-control` | `CROWSNEST_NO_REMOTE_CONTROL` | `false` | Start the orchestrator without Claude Code's Remote Control (on by default, named `<name> crowsnest`) |
| `--no-instruction-restart` | `CROWSNEST_NO_INSTRUCTION_RESTART` | `false` | Do not restart the orchestrator or a lead by itself when its instructions (prompt file, hooks, settings, MCP config) changed since it started; it is shown as "instructions changed since start" and Restart takes them. In the app: Settings, "Restart for new instructions" |
| `--job-remote-control` | `CROWSNEST_JOB_REMOTE_CONTROL` | `false` | Start job agents with Remote Control too, named `<name> <job id> <title>` |
| `--compact-at` | `CROWSNEST_COMPACT_AT` | `150000` | Compact the orchestrator (`/compact` with a focus) once its context passes this many tokens; `0` disables |
| `--fresh-at` | `CROWSNEST_FRESH_AT` | off | `HH:MM`: once a day after this local time, when you have been quiet 20 minutes, the orchestrator writes a hand-over and starts a new conversation |
| `--max-running-jobs` | `CROWSNEST_MAX_RUNNING_JOBS` | a quarter of the cores, at least 2 | Job agents running at once; more wait queued with the reason shown and start in order (the orchestrator and project leads are not counted). Changeable in the app, with per-project limits |
| `--load-hold` | `CROWSNEST_LOAD_HOLD` | `2` | Hold new job starts while the load average per core stays above this for a minute (running work is never touched); `0` off |
| `--heavy-slots` | `CROWSNEST_HEAVY_SLOTS` | `2` | Heavy commands (builds, test suites, typechecks) that may run at once through `crowsnest heavy` |
| `--review` | `CROWSNEST_REVIEW` | `auto` | The review gate for builder jobs in a worktree: `auto` (the other engine when installed, else the same one), `same`, `claude`, `codex` or `off`. A job can ask for its own |
| `--merge` | `CROWSNEST_MERGE` | `merge` | How Land merges a finished job's branch into its base: `merge` (a merge commit) or `ff` (fast-forward only) |
| `--digest-at` | `CROWSNEST_DIGEST_AT` | off | `HH:MM`: write the daily digest into the Inbox after this local time (also the switch in the Schedule sheet and in Settings) |
| `--theme` | `CROWSNEST_THEME` | `crowsnest` | the theme a browser gets until it chooses its own: `crowsnest`, `phosphor`, `deep-sea`, `ember` or `contrast` (Settings → Appearance sets it; each browser's own choice wins) |
| `--check-timeout` | `CROWSNEST_CHECK_TIMEOUT` | `30m` | Longest crowsnest's own check of a finished coding job may run (DESIGN 6.15) |
| `--require-viewed-before-land` | `CROWSNEST_REQUIRE_VIEWED_BEFORE_LAND` | `false` | Merge and push refuse a job until every file of its diff is marked viewed (off: Land warns); also in Settings (DESIGN 6.21) |
| `--check-rounds` | `CROWSNEST_CHECK_ROUNDS` | `3` | Failed checks sent back to the agent before the job needs you |
| `--job-budget-usd` | `CROWSNEST_JOB_BUDGET_USD` | `0` (none) | Default budget per job in estimated dollars, its delegated jobs included (DESIGN 6.16) |
| `--job-budget-minutes` | `CROWSNEST_JOB_BUDGET_MINUTES` | `0` (none) | Default budget per job in minutes since its agent started |
| `--job-budget-turns` | `CROWSNEST_JOB_BUDGET_TURNS` | `0` (none) | Default budget per job in ended turns, delegated jobs included |
| `--job-budget-tokens` | `CROWSNEST_JOB_BUDGET_TOKENS` | `0` (none) | Default budget per job in tokens, delegated jobs included |
| `--daily-ceiling-usd` | `CROWSNEST_DAILY_CEILING_USD` | `0` (none) | The fleet's spend ceiling per day: bounds jobs with no budget of their own, and holds new starts once reached |
| `--orchestrator-ceiling-usd` | `CROWSNEST_ORCHESTRATOR_CEILING_USD` | `0` (none) | The orchestrator's spend ceiling per day; past it automatic pokes wait |
| `--lead-ceiling-usd` | `CROWSNEST_LEAD_CEILING_USD` | `0` (none) | Each project lead's spend ceiling per day, where its spend is measured |
| `--attachment-max-mb` | `CROWSNEST_ATTACHMENT_MAX_MB` | `25` | Largest file you can paste, drop or attach, and that an agent can attach to a question or a result, in MB |
| `--attachment-quota-mb` | `CROWSNEST_ATTACHMENT_QUOTA_MB` | `1024` | Disk all attachments may take, in MB; past it the oldest files nobody needs any more are deleted, else an upload is refused |
| `--protected-host` | `CROWSNEST_PROTECTED_HOSTS` | none | A production host no agent may reach unattended: the guard hook refuses a command that would reach it, and an approval naming it is dangerous. Repeatable; added to the file below |
| `--protected-hosts-file` | `CROWSNEST_PROTECTED_HOSTS_FILE` | `~/Developer/claude-agent/config/protected-hosts` | File of protected hosts, one per line, `#` comments; read when it exists, `''` for none |
| `--protected-branch` | `CROWSNEST_PROTECTED_BRANCHES` | none | A branch agents may not commit or push onto, nor force-push (the guard hook). Repeatable |
| `--browse-root` | `CROWSNEST_BROWSE_ROOTS` | none | A directory the folder picker may browse besides your home and the directories projects and the remembered list use (e.g. `/srv`, `/Volumes`). `/` is browsable only when named here. Repeatable |
| `--terminal-machines` | `CROWSNEST_TERMINAL_MACHINES` | `false` | Let the Terminal view's herdr client show your saved SSH machines (`herdr machine add`). Off, it runs with a state directory of crowsnest's own and sees only this session, so signing in to crowsnest does not reach your other hosts |
| `--tailscale-serve` | `CROWSNEST_TAILSCALE_SERVE` | `false` | Publish crowsnest to your tailnet while it runs: a foreground `tailscale serve` that ends with crowsnest, on the first free of 443, 8443 and 10000. Needs a login (refused with `--no-auth`); never Funnel. Editable in Settings and from the System page. See [Publish to your tailnet](#publish-to-your-tailnet) |
| `--tailscale-serve-port` | `CROWSNEST_TAILSCALE_SERVE_PORT` | `0` | The tailnet HTTPS port `--tailscale-serve` uses; `0` picks the first free of 443, 8443 and 10000. A port another serve config uses is refused, never taken over |
| `--tailscale-auth` | `CROWSNEST_TAILSCALE_AUTH` | `false` | Sign in tailnet users the owner allowed (Settings, Sign in with Tailscale) from the identity headers `tailscale serve` on this machine adds. Password login stays. See [Sign in with Tailscale](#sign-in-with-tailscale) |
| `--backup-every` | `CROWSNEST_BACKUP_EVERY` | `0` (off) | Make an automatic backup this often (at least `1h`), into `<data-dir>/backups`. Also in Settings |
| `--backup-keep` | `CROWSNEST_BACKUP_KEEP` | `7` | Automatic backups kept |
| `--archive-after-days` | `CROWSNEST_ARCHIVE_AFTER_DAYS` | `14` | Settled jobs leave the boards after this many days (still searchable); `0` never |
| `--delete-after-days` | `CROWSNEST_DELETE_AFTER_DAYS` | `180` | Settled jobs and their timelines, settled questions and approvals, journal and spend detail are deleted after this many days; `0` never. Unlanded or unreviewed work is never deleted without asking |
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
| `GET /jobs/{id}/review`, `GET /jobs/{id}/review/diff?path=` | session | The Review tab: what a worktree job produced, the files in reading order, the review rounds, the land actions open (and why not), and one file's diff |
| `GET /changes/jobs/{id}`, `/changes/projects/{ref}`, `/changes/orchestrator` | session | The live diff (DESIGN.md 6.21): `?scope=all\|committed\|uncommitted\|since_viewed`, `&base=` (a main checkout: `""` upstream, `session`, or a ref), `&refresh=1`; the files with status, counts, content hashes and viewed marks, the scopes' totals, the base and commits |
| `GET /changes/{who}/file?path=&scope=&context=&ws=ignore&since=1` | session | One file's unified diff (only files the diff lists), with both sides' whole text when under 256 KB; `since=1` is the change since the version you marked viewed |
| `PUT /changes/{who}/viewed`, `DELETE /changes/{who}/viewed` | session | `{scope, files: [{path, hash}]}` or `{all: true}` marks files viewed (`409 STALE` when a file changed after it was shown); `{scope, paths}` clears (no paths: all in scope) |
| `GET /changes/projects/{ref}/jobs`, `GET /changes/progress` | session | A project's main checkout and job checkouts with diffstats; jobs' viewed counts for the cards. A `changes` event `{subject, revision}` says a diff or its marks changed |
| `POST /jobs/{id}/land` | session | `{action: merge\|pr\|discard, confirm: true}` → what was done and whether the teardown finished; `409 CONFIRM_REQUIRED` without `confirm`; `400` for a refusal (dirty checkout, conflict, protected branch) with nothing changed; `409 EVIDENCE_<CODE>` when crowsnest's check is stale, failed or missing, or the base moved on, unless `override: true`. Merge and pr need `expected_head` (the Review tab's `land.head`): exactly that commit lands, else `409 EVIDENCE_HEAD_MOVED` |
| `POST /jobs/{id}/teardown`, `POST /jobs/{id}/start-anyway` | session | Retry a landed job's teardown (`{confirm: true}`); start a held job past the limits |
| `GET /sweep`, `POST /sweep/discard` | session | Finished jobs whose worktrees are still around; discard several (`{ids, confirm: true}`) |
| `GET /queue`, `GET\|PUT\|DELETE /throttle`, `GET /system/heavy` | session | Running / limit / waiting; the limits (and back to the flags); the heavy slots |
| `GET\|POST /pause`, `POST /resume` | session | The pause switch (`{until?, reason?}`) |
| `POST /jobs/{id}/recheck`, `POST /jobs/{id}/rebase` | session | Run crowsnest's check of a job again; start a job that merges the base into its branch (then it is checked again) |
| `PUT /jobs/{id}/budget`, `POST /jobs/{id}/budget/finish-step` | session | Change (raise) a job's budget `{usd?, tokens?, minutes?, turns?}`; let a job at its budget finish the step it is in |
| `POST /jobs/{id}/wake` | session | Wake a blocked job now |
| `GET /limits`, `GET /system/waits` | session | The fleet's usage limits with their reset times; live jobs with no owner or wake-up |
| `POST /limits/{engine}/clear` | session | Clear hold: forget the engine's usage limit and let the jobs it held go (resumed one at a time) |
| `GET\|PUT /projects/{id}/accept` | session | A project's check command and budget defaults, and the check that would run now |
| `POST /questions/{id}/reconcile`, `POST /questions/{id}/retype` | session | Look for an answer in the agent's conversation; type an uncertain or failed one again (`{confirm: true}`) |
| `GET\|POST /presence`, `POST /presence/gone`, `PUT /presence/dnd` | session | Presence (the browser's heartbeat) and do not disturb (`{until}`) |
| `GET /digests`, `GET\|PUT /digest/settings`, `POST /digest/now` | session | The digest history, its switch and time, and one now |
| `GET /account` | session | `{username, source: database\|config\|none, name, name_managed, min_password_length}` |
| `POST /account/password` | session | `{current_password, new_password}` → signs every other session out and re-issues this browser's cookie; `403 WRONG_PASSWORD` (counted like a failed sign-in), `409` when the login comes from flags or the environment |
| `PUT /account/name` | session | `{name}` → the instance name, stored; `409` when `--name`/`CROWSNEST_NAME` sets it |
| `POST /admin/update` | session | Verify and install the newer release (as `crowsnest update`), answer `{updated, version, restarting}`, then exit for a supervisor to restart. `409 REFUSED` with the reason (container, development build, Homebrew install, already current), `409 UNSUPPORTED` when self-update is off (development build, `--demo`, container), `502 UPDATE_FAILED` when a download or check failed (nothing changed; the log says why) |
| `POST /auth/login` | none | `{username, password}` → session cookie; `429` with `Retry-After` after 5 failures a minute per IP or 20 overall |
| `POST /auth/logout` | none | Deletes the session and clears the cookie |
| `GET /auth/me` | session | `{username, features: {auth, voice, demo, session, herdr_version, name, attachments, onboarding}}` (`onboarding` true while the first-run checklist is unfinished: the app opens `/welcome`); `name` is this instance's (`--name`, else the one chosen at setup or in the Account sheet, else the short hostname); `attachments` is `{max_bytes, max_files}` |
| `GET /events` | session | Server-sent events: `hello` on connect, `heartbeat` every 20 s, `fleet` (data: the new fleet) whenever it changes, `message` (`{pane_id, cursor}`) when a followed conversation changes, `orchestrator` (its status) on every state change, `approval` (`{id, status, risk, tool, job_id, pane_id, change}`) when a tool-call approval is requested or settled, `question` (`{id, status, title, kind, urgency, change}`) when a question is asked, settled or delivered, `say` (voice on: `{kind: speak\|chime, source: say\|reply, text_id, text, pane_id, at}`, played by browsers with speak mode on; voice off: `{text, pane_id, at}`, which nothing plays); `job` (`{id, state, stuck, cause, title, change}`) when a job changes; `poke` (a poke's result, as `POST /orchestrator/poke` returns it) when a poke is sent or held; `journal` (`{id, kind, summary}`) for each new journal entry; `schedule` (`{at}`) when a scheduled task's stamp or switch changed; `attachment` (`{owner}`, such as `job:j_3`) when an owner's files changed; `spend` (`{at}`) when measured spend changed; `sys` (a machine sample, every 2 s while a browser is connected); `system` (`{verdict, summary}`) after a doctor run |
| `GET /system[?recheck=1]` | session | The doctor report as the running server sees it (`checks`, `verdict`, `summary`, `versions`, `agents`, `selection`, `paths`, `settings`, `live`: uptime, orchestrator, hooks seen, last errors); cached 30 s unless `recheck=1` |
| `GET /system/diagnostics` | session | The same as redacted `text/plain` for an issue |
| `GET /system/stats` | session | The machine: `{latest, history}` (CPU %, cores, memory, load, disk, scope `host`/`container`, working agents); `sys` events carry each new sample |
| `GET /system/agents` | session | Each agent's process tree: `[{pane_id, name, kind, cpu, rss, processes}]` |
| `GET /backup` | session | A fresh backup as a download (`application/gzip`) |
| `GET /updated`, `POST /updated/seen` | session | `{from, to, at, release_url}` after an update until seen, else `null` |
| `GET /storage` | session | The Storage card: `{areas, tables, policy, last_run, worktrees}` |
| `POST /storage/cleanup` | session | `{dry_run, confirm: ["j_12"]}` → what it did (or, with `dry_run` true or missing, would do): `{archive, delete_jobs, held, confirmed, sweeps, state_dirs, state_bytes, blobs_freed}` |
| `GET /summary` | session or a read token (`Authorization: Bearer cnk_…`) | `{name, version, attention, questions, blocking, approvals, needs_you, ready_to_land, review_objects, running, jobs, orchestrator, paused, stats, at}` (attention adds jobs ready to land and those whose reviewer still asks for changes, 6.11): what another machine's Machines card shows |
| `GET /tokens`, `POST /tokens`, `DELETE /tokens/{id}` | session | Scoped read tokens: list (never the token), mint `{name}` → `201 {token, record}` (the token shown once), revoke |
| `GET /machines`, `POST /machines`, `DELETE /machines/{id}` | session | Other instances with their summaries or an error; add `{name, url, token}`; remove |
| `GET /auth/tailscale`, `PUT /auth/tailscale`, `POST /auth/tailscale/allow-me` | session (changes: a password session) | Sign in with Tailscale: `{enabled, serve_configured, current_login, via_tailscale, allowed}`; replace the list `{logins}`; add this request's own tailnet login. `409` with the option off, `403` from a session signed in with Tailscale |
| `POST /system/demo-state` | session, `--demo` only | `{state}`: switch the scripted machine the demo's doctor sees (`ok`, `herdr-missing`, `claude-signed-out`, `codex-only`, `no-agent`, `both`) |
| `GET /settings` | session | Every setting the app may change: `{settings: [{name, label, section, kind, value, source, where, locked, apply, …}], bind, bind_command}` |
| `PUT /settings/{name}` | session | `{value}` or `{clear: true}` → validated by the config loader, saved, journalled, applied; `409` for a setting the app may not change or one a flag or variable sets, `400` with the loader's message |
| `GET /onboarding[?recheck=1]` | session | The first-run checklist: `{pending, report, choose_engine, engine, dir, orchestrator, codex_gaps}` |
| `POST /onboarding/start` | session | `{engine?}` → finishes the checklist and lets the orchestrator start; `409` with no agent installed |
| `POST /onboarding/shell` | session | `{command}` (only a fix command the report offers, such as `claude`) → `{pane_id}` of a new shell pane in the orchestrator's folder with the command typed in |
| `GET /fleet` | session | The herdr session: workspaces → tabs → panes with their agents, a flat `agents` list, `counts`, `revision`; `503 UNAVAILABLE` when herdr does not answer |
| `GET /agents/{target}/screen` | session | One agent's (or pane's) visible screen: `text`, `ansi`, `screen_hash`, `dialog` (options, form, warnings), `draft`. `{target}` is an agent name or pane id |
| `POST /agents/{target}/keys` | session | `{keys: ["1"], screen_hash}` → sends the keys if the screen still has that hash, else `409 CONFLICT` and nothing is sent. Only `esc`/`ctrl+c` may omit the hash |
| `GET /agents/{target}/messages?after=&limit=` | session | A page of the agent's conversation from its Claude transcript: `{source, session_id, cursor, reset, earlier, more, messages}`. Without `after` (or with a stale cursor) the newest `limit` (200) messages and `reset: true`; with it, only what changed since. No transcript: one `pane` message with the pane's recent text |
| `POST /agents/{target}/prompt` | session | `{text, force_over_draft, voice, attachments}` (`attachments`: upload ids, copied into the agent's inbox and named on `Attached:` lines after the text) (`voice: true` adds the voice header; 404 when talk mode is off) → `{pane_id, status, queued, started, note}`. `409 BLOCKED` (with the dialog in `error.detail`) when a dialog is up, `409 DRAFT` (with the draft) when text was typed at the terminal; nothing is typed in either case |
| `GET /orchestrator` | session | `{state, pane_id, session_id, dir, model, detail, restarts, remote_control, remote_control_note, remote_control_url}`; `remote_control_url` is the session's `https://claude.ai/code/session_…` link once captured (absent until then and after every start); `model` is the one Claude was started with (empty: Claude's default); `remote_control` is the Remote Control name Claude was started with, `remote_control_note` `active` once Claude said so, or a sentence when it did not within 45 s; state is `idle`, `working`, `blocked`, `needs_terminal`, `starting`, `restarting`, `failed`, `disabled` or `waiting` (the start gate: the first-run checklist unfinished, herdr not connected, or no agent; `detail` says which); `engine` is `claude` or `codex` |
| `POST /orchestrator/restart`, `POST /orchestrator/new` | session | Restart Claude on the same conversation (`--resume`), or with a fresh one |
| `POST /orchestrator/poke` | session | "Poke now" → `{outcome: sent\|skipped, reason, reasons, manual, started, note, at}`. Skips the two-minute limit between agent pokes; `skipped` with `reason` "nothing is due" when there is nothing to say, or why the orchestrator cannot take one (working, a dialog, a draft) |
| `GET /orchestrator/context` | session | The orchestrator's context: `{tokens, known, at, compacted_at, human_at, threshold, last_compact_at, fresh_at, fresh_day}` (`known` is false before the first reading and right after a compaction that recorded no size) |
| `POST /orchestrator/compact` | session | "Compact now": `/compact` with the focus, at any size → the context as above; `409 CONFLICT` when the orchestrator is busy, at a dialog or has a draft |
| `GET /schedule?project=` | session | Scheduled tasks: `{tasks: [{name, title, path, origin: app\|dir\|user, editable, every, after, schedule, as, target: orchestrator\|lead\|job, project_id, project, dir, role, engine, body_md, shadowed, enabled, last_run_at, next_due_at, last_outcome, job_id, job_state, run_now, runs: [{at, outcome, job_id}]}], problems: [{path, name, error}], dirs}`; `project=<id>` keeps one project's tasks (`0`: the orchestrator's and unfiled) |
| `POST /schedule` | session | A new app schedule `{name, every, after, as: poke\|job, project_id, body_md, dir, role, engine}` → `201` and the list. `400` invalid, `409` the name is taken (by a file or app schedule) |
| `POST /schedule/{name}/run` | session | Run now: a poke task goes with the next poke the orchestrator can take, a job task starts its job → the list. `404` for an unknown name, `409` for a task file waiting for review |
| `POST /schedule/{name}/approve` | session | `{hash}` as the list showed it: approve a task file that starts a job or pokes a lead → the list. `409` when the file changed since, or it needs no review |
| `PUT /schedule/{name}` | session | `{enabled}`: switch a task off or on (kept by crowsnest; a file is untouched); and/or every field of `POST /schedule` (but `name`) to replace an app schedule → the list. `409` "read-only: edit the file" for a task file's edit |
| `DELETE /schedule/{name}` | session | Delete an app schedule (its history stays) → the list. `409` for a task file, `404` unknown |
| `GET /spend?from=&to=&group=` | session | Spend for the local days `from`..`to` (YYYY-MM-DD, inclusive; default this week, Monday to today, at most a year) grouped by `job` (the orchestrator is its own row), `model` or `day`: `{from, to, group, rows: [{key, label, …summary}], total, prices_checked, prices_source, prices_file, note}`. A summary is `{measured, reason, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, total_tokens, turns, tool_calls, wall_seconds, cost_usd (null when unknown), cost_partial, unpriced_tokens, cache_hit_share, models, first_at, last_at}` |
| `GET /spend/summary` | session | `{today, week, orchestrator_today, orchestrator_total, today_date, week_start, prices_checked, note}`: the Jobs header's totals and the Chat menu's line |
| `GET /journal?kind=&before=&limit=` | session | The journal, newest first: `{entries: [{id, at, kind, summary, detail}], total, next_before}`. `kind` filters (comma-separated: `poke`, `skip`, `send`, `answer`, `restart`, `alert`, `backstop`, `task`, `upkeep`); `before` is the previous page's `next_before`; `limit` defaults to 50, at most 200 |
| `GET /questions?status=` | session | Questions with a status (`open`, `answered`, `expired`, `cancelled`, a comma-separated list, or `all`), open first; file contents left out (`file_count` says how many) |
| `GET /questions/{id}` | session | One question with its `files`, the agent's `attachments` and the `answer_attachments` sent with the answer |
| `POST /questions/{id}/answer` | session | `{answer_value, answer_note, attachments}` (`attachments`: upload ids, copied into the asker's inbox and named in the answer line): a key, an array of keys for a multi-select, nothing for ack/notice; a `manual` answer is the note. `409 CONFLICT` once settled |
| `DELETE /questions/{id}` | session | Cancel an open question; the asker is told |
| `GET /inbox/summary` | session | `{open, blocking}`: open questions (the badge is the attention count) |
| `GET /attention`; `POST /attention/snooze`, `/unsnooze`, `/dismiss` `{id}` | session | The one queue of what needs you, with its counts; snooze an item for ten minutes on every device; dismiss a failed job |
| `GET /now`, `GET /now/since?browser=`, `POST /now/beat`, `POST /now/seen` | session | The Now view's rows; what happened since this browser left; this browser is in use; mark all seen |
| `GET /approvals?status=` | session | Tool-call approvals (`pending`, the default; `all`; or one status), newest first: `{id, job_id, pane_id, agent_name, cwd, tool_name, tool_input, suggestions, risk, risk_reasons, status, decided_via, note, status_reason, rule_ids, remember, created_at, settled_at, expires_at, summary}` (`decided_via` is `browser`, `notification` or `policy`) |
| `GET /approvals/{id}` | session | One approval |
| `POST /approvals/{id}/decide` | session | `{decision: "allow"\|"deny", note?, confirm?, remember?: {pattern}}` → the approval. `409 CONFLICT` when it is no longer pending, or a remember is refused (a deny, a dangerous call, no job, the job ended); `400` for a bad pattern; `409 CONFIRM_REQUIRED` for a dangerous allow without `confirm: true`. The note is the deny message the agent gets. `remember` also stores a rule for the job and allows its other pending calls the rule matches |
| `POST /approvals/batch-allow` | session | `{ids}` → the approvals, allowed once each, when all are pending, low risk and from the same job; else `409` with the reason and nothing allowed |
| `GET /projects/{ref}/approval-rules` | session | The project's live approval rules, each `{id, scope, kind, pattern, max_risk, note, created_at, created_via, sentence, recent}` (`recent`: what it matched in the project's last week) |
| `POST /projects/{ref}/approval-rules` | session | `{kind: "bash"\|"edit"\|"tool", pattern, max_risk?: "low"\|"elevated", note?}` → `201` the rule; `400` with a sentence when it is not valid |
| `POST /projects/{ref}/approval-rules/preview` | session | The same body → `{considered, allowed, above_max_risk, dangerous}`: the project's recent calls (last 7 days, at most 50) the pattern matches; dangerous ones are never auto-allowed |
| `DELETE /approval-rules/{id}`, `GET /jobs/{id}/approval-rules` | session | Remove a project or job rule; a job's remembered rules. None of the rule routes exists on the agent socket |
| `POST /push/action` | action token | A notification's button: `{token, action}` with the single-use, item-scoped, 15-minute token the push carried (never the session cookie) → `{item, message}`; `403` for a used, expired, forged or out-of-scope token, and for any elevated or dangerous approval. Action `snooze` hides the item from the queue for ten minutes and decides nothing |
| `POST /attachments` | session | One file: the raw body with its name in `X-Filename` (URL-encoded), or multipart `file` → `201` the pending attachment `{id, name, type, size, sha256, image, thumb, inline, …}`. The type is sniffed from the bytes. `413 TOO_LARGE` past `--attachment-max-mb`, `507 QUOTA_FULL` when the quota cannot be freed |
| `GET /attachments/{id}` | session | The file: inline only for PNG, JPEG, GIF, WebP, BMP, PDF and plain text; everything else (SVG, HTML, …) as a download; always `nosniff`, and a sandboxing CSP on all but an inline PDF. `?download=1` forces a download. `/thumb` is a JPEG thumbnail of a picture, `/meta` the record |
| `GET /attachments?owner=&role=`, `?answer=` | session | An owner's files (`job:j_3`, `question:q_7`, `orchestrator:`), or the files sent with an answer (`answer=q_7`) |
| `DELETE /attachments/{id}` | session | Remove an upload that was never sent (`409` once sent) |
| `GET /jobs?state=` | session | The board: every job, newest first (`state` filters, comma-separated), each with its `spend` summary; archived jobs are left out. `?q=text` searches title, brief and result (newest 100), `?archived=1` lists archived jobs only |
| `POST /jobs` | session | `{brief, dir?, role?, done_when?, engine?, model?, isolate?, attachments?}` (upload ids, listed in the brief) → the queued job (`201`); the agent starts in the background. No `dir`: the orchestrator takes it |
| `GET /jobs/{id}` | session | The job with its `timeline` (state changes, progress, nudges, hooks, questions, restarts, retries, messages), `questions`, `spend` and `spend_turns` (its latest model calls, oldest first: `{at, model, tokens, output, cost_usd}`) |
| `POST /jobs/{id}/message` | session | `{text, force_over_draft, attachments}`: typed into the job's agent as `[crowsnest message j_<n>] …`; errors like a Chat send |
| `POST /jobs/{id}/cancel`, `/nudge`, `/resume` | session | Cancel (Esc to the agent, then `cancelled`; the pane stays), nudge by hand, or resume a stuck job (retry now after a limit, restart a gone agent, else nudge) |
| `GET /projects[?archived=1]`, `POST /projects`, `GET /projects/{ref}`, `PUT /projects/{ref}` | session | Projects with their lead and job counts; create `{name, dir, …}`; one by id or slug (`?open=1` when opened in the app); edit or `{archived}` |
| `GET /leads`, `POST /projects/{ref}/lead/start\|stop\|restart\|new\|poke` | session | Running leads; a project's lead controls |
| `POST /jobs/{id}/restart-agent` | session | Restart the job's agent on the same conversation with fresh instructions |
| `GET /voice/agents`, `PUT /voice/agents/{agent}` | session | Each standing agent's voice (`{voice_id}`, empty for the instance's) |
| `GET /roles`, `GET /dirs` | session | Role names with a one-line summary; remembered job directories, then the cwds of the session's panes |
| `GET /browse?path=&files=1&hidden=1` | session only (no read token; not on the agent socket) | The folder picker: one directory's folders (and, asked, files: names only), repositories marked, links resolved, under the roots alone; 500 entries at most, 3 s at most (`504` on a slow mount) |
| `GET /browse/facts?path=` | session only | A folder's facts: git repo, branch, base branch guess, remote host (credentials removed), check command, AGENTS.md / CLAUDE.md, Claude's trust, the project holding it or inside it |
| `GET /browse/places` | session only | Home, favourites and roots for the picker |
| `GET /instructions`, `GET /instructions/{scope}/{key}` | session | Instruction layers: every layer with its source (`none`, `builtin`, `db`, `file`) and text; one layer with its revisions. The global layer's key is `-` |
| `PUT /instructions/{scope}/{key}`, `DELETE /instructions/role/{name}`, `POST /instructions/{scope}/{key}/restore` | session | `{body_md}` (at most 32 KB): a new revision; reset a role to its built-in; `{revision}`: that text again. `409` for a role that comes from a file |
| `POST /instructions/role/{name}/file-seen` | session | `{hash}`: mark a role file's text read, which clears its "changed, review" badge → the layer. `409` when the file changed since |
| `POST /instructions/{scope}/{key}/apply-restored` | session | `{hash}`: let a layer a restore brought back reach agents (R7-S4) → the layer. `409` when its text changed since |
| `GET /agents-instructions/{subject}` | session | `j_<n>`, `orchestrator` or `lead:<slug>`: the layers it was started with, `changed` since, and the instruction files Claude loaded (paths only) |
| `POST /voice/stt` | session | Talk mode. The body is one recording as MediaRecorder made it (`audio/webm`, `audio/mp4`, `audio/ogg`, …; up to five minutes: at most 8.2 MB, and it must arrive within 2 minutes) → `{text}`. 12 a minute per session (`429` with `Retry-After`); ElevenLabs failures are `502`/`503`/`422` with code `VOICE_UPSTREAM` and never its own error text |
| `GET /voice/tts?text_id=` | session | The speech of a server-held text (a `say`, a SAY line, a read-aloud), streamed (`audio/mpeg`). `404` for an unknown or expired id (texts live an hour). 30 a minute per session; a replay comes from a cache |
| `POST /voice/read` | session | `{question_id}` → `{text_id}`: the question's title and options, to play through `/voice/tts` |
| `POST /voice/reply` | session | `{target, message_id}` → `{text_id, text}`: that reply's SAY line, read from the transcript on the server, to play through `/voice/tts` (Chat's play button). `404` when it has none |
| `GET /voice/settings` | session | `{voice_id, voice_source, default_voice_id, speed, speed_min, speed_max, playback_rate, model, models}`: how replies are spoken. `voice_source` is `settings` (chosen in the app) or where the default came from (`--elevenlabs-voice`, `CROWSNEST_ELEVENLABS_VOICE`, the config file, `default`); `playback_rate` is the browser's share of the speed |
| `PUT /voice/settings` | session | `{voice_id, speed, model}` (`voice_id` empty: the default; speed 0.8 to 1.5; model `fast` or `quality`) → as GET. Stored in the database; applies from the next text spoken |
| `GET /voice/voices` | session | The ElevenLabs account's voices `[{voice_id, name, category, description, labels}]`, cached for five minutes |
| `POST /voice/preview` | session | `{voice_id, speed, model}` (unsaved choices) → `{text_id, playback_rate}`: a fixed sample sentence to play through `/voice/tts` |
| `GET /push/key` | session | `{public_key}`: the VAPID key for `pushManager.subscribe` |
| `GET /push/settings`, `PUT /push/settings` | session | `{kinds, order, devices, max_devices}`; PUT `{kinds: {approval, question, dialog, job, done, orchestrator: bool}}`. A device is `{id, user_agent, service, created_at, last_success_at, last_failure_at, failures, endpoint_hash}`, never its endpoint |
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
| `mise run test:claude` | Real-Claude smoke (a few cents): a real orchestrator on haiku in a throwaway session answers one prompt through the HTTP API, then Restart and New conversation; a manual poke with one synthetic reason is delivered through `POST /orchestrator/poke` and answered; and it calls the `ask` MCP tool, the question is answered through the browser API, and the answer arrives in its transcript; and a PNG with a word drawn in it, uploaded and sent with a prompt under the `default` permission mode, is read (no permission prompt) and the word named. Then **the stop test**: three haiku job agents in a scratch git repo, where an undeclared stop is refused by the Stop hook, `done` stops cleanly, and a non-blocking `ask` answered through the browser API reaches the agent, which finishes. And **the approvals test**: three haiku job agents under the `default` permission mode: a Bash call becomes an approval, is allowed through the API and runs with no terminal answer; another is denied with a note the agent quotes back; and `ssh` to a protected host is refused by the guard hook with no approval asked |
| `mise run fonts:terminal` | Regenerate the terminal's fallback webfonts (`web/src/fonts/terminal/`, committed) with `web/scripts/terminal-fonts.py`: downloads the pinned Nerd Fonts symbols release and Unicode emoji data (checked by SHA-256), reads DejaVu and Noto from the pinned `@fontsource` devDependencies, and writes the unicode-range chunks and their CSS. Run after changing a pin |
| `mise run lint` | `go vet`, golangci-lint, gofmt check, `tsc`, eslint. Read-only |
| `mise run fmt` | gofmt and eslint `--fix` |
| `mise run check` | lint + test: the gate before calling anything done |
| `mise run test:clone` | Clone HEAD into a temporary directory and run `setup` and `build` there: proves a clean checkout builds. Slow (a full `npm ci`) |
| `mise run e2e` | Builds, then the hermetic browser suite (`web/e2e/hermetic`): Playwright against `./bin/crowsnest --demo`, a fresh instance per test (the auth specs with the login on), desktop Chromium 1440×900 plus 375×812 Chromium and WebKit. User journeys for auth, Jobs, Chat, Inbox, Fleet, terminals and talk mode, and every view's 375 px, theme, offline and axe checks. Also in CI. See [docs/TESTING.md](docs/TESTING.md) |
| `mise run e2e:real` | Builds, then Playwright against the real binary (`web/e2e/real`): the Fleet view on `--demo` with no fixtures; on a throwaway `crowsnest-test-*` herdr session the terminals, a tab made through herdr appearing live, a scripted dialog in a pane answered from the Inbox, and the login-on WebSocket refusals. Each spec starts and stops its own instance |
| `mise run e2e:claude` | Opt-in (running the task is the opt-in: it sets `CROWSNEST_E2E_CLAUDE=1`, without which the specs skip), about $1.50 (haiku): real Claude through the browser on a throwaway session: chat, a job from the Jobs box run to done, a job's question answered in the Inbox, a nudge on a timeline, `crowsnest ask --wait` from the orchestrator's pane, a real typed draft, and a true first run (setup link → password → checklist → Start → the folder-trust card → Chat greeting, in a fresh /tmp folder, which Claude then remembers as trusted in `~/.claude.json`). Uses your real HOME for Claude's login; scratch files stay under `web/e2e/claude/` |
| `mise run screenshots` | Playwright with fixtures: Fleet screenshots at 375×812 and 1440×900, light and dark, into `web/screenshots/`, plus the 375 px checks (no sideways scroll, 44 px targets) |
| `mise run screenshots:inbox` | (The three `screenshots:*` tasks run `web/e2e/screenshots` in the `screenshots` Playwright project, a fresh demo instance per test.) Builds, then Playwright against `./bin/crowsnest --demo`: the Inbox list and a question (mermaid and a diff) at 375×812 and 1440×900, light and dark, a multi-select, the 375 px checks, and an answer round trip |
| `mise run screenshots:jobs` | Builds, then Playwright against `./bin/crowsnest --demo`: the Jobs board (light and dark), the new-job form, a running, a stuck and a done job at 375×812 and 1440×900, the 375 px checks, and giving a job |
| `mise run screenshots:palette` | Builds, then Playwright against `./bin/crowsnest --demo`: the command palette open, with a query (highlights, Give job), and a job's actions at 375×812 and 1440×900 in light and dark, plus the keyboard shortcuts sheet |
| `mise run screenshots:manual` | Builds, then Playwright against `./bin/crowsnest --demo`: the built-in manual's pictures (`docs/manual/img`, light theme, 1x, JPEG): Now at 1440 and 390, the board, a project, a decision packet and rescue on a phone, the Land tab, the Schedule sheet and Chat. Run it after a UI change the manual's pictures show |
| `mise run screenshots:themes` | Builds, then Playwright against `./bin/crowsnest --demo`: Now in every theme, light and dark, at 1440 and 390, as one contact sheet per theme (`web/screenshots/themes-<id>.png`) and one of them all (`themes-all.png`), plus the Settings → Appearance gallery |
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
| `~/.local/state/crowsnest/<session>/jobs/j_<n>/` | A job agent's `prompt.md` (role prompt and job protocol), `brief.md`, `settings.json` (hooks with `--job j_<n>`, the allow rule) `mcp.json` (with the agent's token), `agent-token` (the token, for the hooks) and `guard.json` (the guard hook's rules: the session, protected hosts and branches); each written fresh and read-only (0400). The orchestrator's `guard.json` is beside its `claude-settings.json` |
| `~/.config/crowsnest/risk.yaml` | Optional: overrides for the approval risk rules (`disable: [ids]`, `rules: [{id, level, tools, field, pattern, reason}]`); an invalid file stops the start (DESIGN.md 8.7) |
| `~/.local/share/crowsnest/backups/` | Automatic backups (`auto-<session>-<time>.tar.gz`) and the copies taken before an upgrade (`pre-migration-…db`, newest three) |
| `~/.local/state/crowsnest/<session>/terminal-client/` | The Terminal view's herdr client state (`XDG_STATE_HOME`), so it sees none of your saved SSH machines (`--terminal-machines` turns this off) |
| `~/.config/crowsnest/roles/*.md` | Optional role prompts that add to or replace the built-in ones |
| `~/.config/crowsnest/prices.json` | Optional price overrides for the spend estimate (see Spend) |
| `~/.local/state/crowsnest/<session>/mcp.json` | The orchestrator's `--mcp-config`: the `crowsnest mcp` stdio server, its `CROWSNEST_SOCKET` and its agent token (`0600`); `agent-token` beside it holds the same start's token for the hooks |
| `~/.local/state/crowsnest/<session>/agent.sock` | The agent API socket (when `XDG_RUNTIME_DIR` is unset) |
| `$TMPDIR/crowsnest-demo-*/` | `--demo` only: the demo's database, state (the demo agents' transcripts, its agent socket) and orchestrator directory, removed on exit |
| `~/.local/state/crowsnest/mise-install.log` | Devbox image only: the entrypoint's last `mise install` |

## License

MIT. (C) 2026 Jeff Clement. See [LICENSE](LICENSE).
