# Settings and flags

<!-- Generated from internal/config (the option table and the settings the app may change). Do not edit by hand: run `UPDATE_MANUAL=1 go test ./internal/config -run Manual`. -->

Every setting crowsnest has, and where you can change it. A value is taken from the first of these that sets it: a command-line flag, an environment variable, the app's **Settings** sheet (only the settings marked below), the config file (`~/.config/crowsnest/config.yaml`, keys are the flag names), then the default. When a flag or variable sets something, the Settings sheet shows it locked, with where to change it.

Durations need a unit (`15m`, `2h`); a bare number is refused. Lists repeat as flags, are comma-separated in the environment, and are a YAML list in the file.

Open the Settings sheet: [Open Settings](app:sheet/settings). The [Settings concept](../concepts/budgets-limits-pause.md) and [how-to pages](../README.md) explain what the important ones are for.

## Changeable in the app

These appear in the **Settings** sheet (account menu → Settings), grouped as the sheet groups them.

### Agents

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Orchestrator model | `--model` | when the orchestrator restarts (crowsnest restarts it) | sonnet, opus, haiku or a full model id; empty means Claude's own default |
| Job model | `--job-model` | at once | for Claude job agents whose job names none; empty means Claude's own default |
| Default engine | `--engine` | when the orchestrator restarts (crowsnest restarts it) | the orchestrator's agent, and the engine of jobs that name none. auto: Claude when signed in, else Codex |
| Permission mode | `--permission-mode` | when the orchestrator restarts (crowsnest restarts it) | the orchestrator's Claude Code permission mode |
| Remote Control (on is `--no-remote-control` off) | `--no-remote-control` | when the orchestrator restarts (crowsnest restarts it) | start the orchestrator with Claude Code's Remote Control, so the Claude app can reach it too |
| Remote Control for jobs | `--job-remote-control` | when crowsnest restarts | start Claude job agents with Remote Control too |

### Orchestrator upkeep

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Restart for new instructions (on is `--no-instruction-restart` off) | `--no-instruction-restart` | at once | when the orchestrator's or a lead's instructions changed since it started, restart it on the same conversation at its next idle moment |
| Poke interval | `--poke-interval` | at once | how often the timer considers poking the orchestrator; 0 turns timer pokes off |
| Compact past (tokens) | `--compact-at` | at once | compact the orchestrator once its context passes this size; 0 off |
| Fresh conversation at | `--fresh-at` | at once | HH:MM local time: a daily hand-over and new conversation; empty off |

### Attachments

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Largest attachment (MB) | `--attachment-max-mb` | when crowsnest restarts | pasted, dropped or attached by an agent |
| Attachment quota (MB) | `--attachment-quota-mb` | when crowsnest restarts | disk all attachments may take; past it the oldest unneeded files are deleted |

### Notices

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Notify URL | `--notify-url` | when crowsnest restarts | ntfy-style URL that gets a one-line POST when something needs you. Write-only: it is never shown again |

### Throttling and review

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Running jobs at once | `--max-running-jobs` | at once | job agents running at once; more wait queued and start in order. 0: a quarter of the cores, at least 2 |
| Hold starts above load per core | `--load-hold` | at once | new job starts wait while the load average per core stays above this for a minute; 0 off |
| Heavy command slots | `--heavy-slots` | at once | builds and test suites that may run at once through crowsnest heavy |
| Review gate | `--review` | at once | for builder jobs in a worktree: auto is the other engine when installed, else the same one |
| Land merges with | `--merge` | at once | merge: a merge commit; ff: fast-forward only (refused when the base moved on) |
| Daily digest at | `--digest-at` | at once | HH:MM local time: the day's digest in the Inbox; empty off |

### Guard

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Protected hosts | `--protected-host` | when crowsnest restarts | production hosts no agent may reach unattended (added to the protected-hosts file's) |
| Protected branches | `--protected-branch` | when crowsnest restarts | branches agents may not commit or push onto, nor force-push |

### Appearance

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Default theme | `--theme` | at once | the theme a browser gets until it chooses its own (Appearance sets it); empty: the house theme |

### Budgets and acceptance

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Budget per job ($) | `--job-budget-usd` | at once | estimated dollars a job and its delegated jobs may use before it stops at a safe point and asks; 0 none |
| Budget per job (minutes) | `--job-budget-minutes` | at once | minutes since a job's agent started; bounds agents whose spend is not measured too; 0 none |
| Budget per job (turns) | `--job-budget-turns` | at once | ended turns, delegated jobs included; 0 none |
| Budget per job (tokens) | `--job-budget-tokens` | at once | tokens, delegated jobs included; 0 none |
| Fleet ceiling per day ($) | `--daily-ceiling-usd` | at once | bounds jobs with no budget of their own and holds new starts once today's spend reaches it; 0 none |
| Orchestrator ceiling per day ($) | `--orchestrator-ceiling-usd` | at once | past it the orchestrator's automatic pokes wait until tomorrow; 0 none |
| Lead ceiling per day ($) | `--lead-ceiling-usd` | at once | each project lead's, where its spend is measured; 0 none |
| Check timeout | `--check-timeout` | at once | longest crowsnest's check of a finished job may run (such as 30m) |
| Check rounds | `--check-rounds` | at once | failed checks sent back to the agent before the job needs you |
| Require all files viewed before Land | `--require-viewed-before-land` | at once | merge and push refuse a job whose diff has files not marked viewed; off, Land only warns |

### Backups and storage

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Automatic backup every | `--backup-every` | at once | such as 24h; 0 turns automatic backups off. Each copies the database and the attachments |
| Automatic backups kept | `--backup-keep` | at once | the oldest automatic backup is deleted past this many |
| Archive settled jobs after (days) | `--archive-after-days` | at once | hidden from the boards, still searchable; 0 never |
| Delete settled history after (days) | `--delete-after-days` | at once | jobs and their timelines, settled questions and approvals, journal and spend detail; 0 never. Unlanded work is never deleted without asking |

### Agents

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Trust the folders of projects you create | `--trust-project-folders` | at once | creating a project records Claude Code's and Codex's "trust this folder" for it (only folders you chose in the app, never one an agent chose); off, every new project's first agent asks in its terminal and the question comes to you as a card |

### Tailnet

| Setting | Flag | Applies | What it does |
|---|---|---|---|
| Publish to your tailnet | `--tailscale-serve` | at once | run tailscale serve while crowsnest runs, so https://<machine>.<tailnet>.ts.net reaches it from your devices (the sign-in page first). Needs a login; never public |

## Every flag

All of them, in `crowsnest --help` order. The config-file key is the flag's name without the dashes.

| Flag | Environment | Default | In the app | What it does |
|---|---|---|---|---|
| `--username` | `CROWSNEST_USERNAME` | none | no | login name |
| `--password` | `CROWSNEST_PASSWORD` | none | no | login password (visible in ps: prefer CROWSNEST_PASSWORD or --password-file) |
| `--password-file` | `CROWSNEST_PASSWORD_FILE` | none | no | file holding the password (first line) |
| `--no-auth` | `CROWSNEST_NO_AUTH`, `SUPPRESS_AUTH` | `false` | no | serve without a login (trusted tailnet or localhost only) |
| `--bind` | `CROWSNEST_BIND` | `localhost` | no | localhost, tailnet, an interface name, an IP, or ip:port (repeatable) |
| `--port` | `CROWSNEST_PORT` | `2769` | no | port for binds that do not name one |
| `--session` | `CROWSNEST_SESSION` | `crowsnest` | no | herdr session to create or connect to |
| `--dir` | `CROWSNEST_DIR` | none | no | orchestrator working directory (default <data-dir>/orchestrator) |
| `--model` | `CROWSNEST_MODEL` | `sonnet` | Agents → Orchestrator model | orchestrator model; an empty value means Claude's own default |
| `--job-model` | `CROWSNEST_JOB_MODEL` | none | Agents → Job model | job agents' model when a job names none (default: Claude's own) |
| `--permission-mode` | `CROWSNEST_PERMISSION_MODE` | `auto` | Agents → Permission mode | orchestrator permission mode |
| `--poke-interval` | `CROWSNEST_POKE_INTERVAL` | `15m` | Orchestrator upkeep → Poke interval | timer poke interval; 0 disables timer pokes |
| `--no-orchestrator` | `CROWSNEST_NO_ORCHESTRATOR` | `false` | no | serve the UI over the session without starting Claude |
| `--allowed-host` | `CROWSNEST_ALLOWED_HOSTS` | none | no | extra Host/Origin name, e.g. a reverse proxy or MagicDNS name (repeatable) |
| `--trusted-proxy` | `CROWSNEST_TRUSTED_PROXY` | none | no | loopback, or an IP or CIDR: a reverse proxy whose client-IP header (--trusted-proxy-header) is believed (repeatable) |
| `--trusted-proxy-header` | `CROWSNEST_TRUSTED_PROXY_HEADER` | `x-forwarded-for` | no | the header a trusted proxy names its client in: x-forwarded-for (its last hop) or cf-connecting-ip (cloudflared only) |
| `--tls-cert` | `CROWSNEST_TLS_CERT` | none | no | PEM certificate: serve HTTPS directly |
| `--tls-key` | `CROWSNEST_TLS_KEY` | none | no | PEM key for --tls-cert |
| `--elevenlabs-api-key` | `ELEVENLABS_API_KEY` | none | no | ElevenLabs API key: enables talk mode (prefer the env var or --elevenlabs-api-key-file) |
| `--elevenlabs-api-key-file` | `ELEVENLABS_API_KEY_FILE` | none | no | file holding the ElevenLabs API key (first line) |
| `--elevenlabs-voice` | `CROWSNEST_ELEVENLABS_VOICE` | `21m00Tcm4TlvDq8ikWAM` | no | ElevenLabs voice id for spoken replies |
| `--elevenlabs-url` | `CROWSNEST_ELEVENLABS_URL` | `https://api.elevenlabs.io` | no | ElevenLabs API base URL (https, or http only on loopback) |
| `--notify-url` | `CROWSNEST_NOTIFY_URL` | none | Notices → Notify URL | POST a one-line notice here when something needs you (ntfy-style) |
| `--notify-url-file` | `CROWSNEST_NOTIFY_URL_FILE` | none | no | file holding the notify URL (first line) |
| `--demo` | `CROWSNEST_DEMO` | `false` | no | in-memory fake herdr and scripted orchestrator |
| `--i-know-this-is-open` | `CROWSNEST_I_KNOW_THIS_IS_OPEN` | `false` | no | allow --no-auth on a bind that is not loopback or tailnet |
| `--allow-default-session` | `CROWSNEST_ALLOW_DEFAULT_SESSION` | `false` | no | allow --session default (your main herdr session) |
| `--data-dir` | `CROWSNEST_DATA_DIR` | none | no | data directory for the database (default ~/.local/share/crowsnest) |
| `--config` | `CROWSNEST_CONFIG` | none | no | config file (default ~/.config/crowsnest/config.yaml) |
| `--check-timeout` | `CROWSNEST_CHECK_TIMEOUT` | `30m` | Budgets and acceptance → Check timeout | longest crowsnest's check of a finished job may run before it counts as failed |
| `--check-rounds` | `CROWSNEST_CHECK_ROUNDS` | `3` | Budgets and acceptance → Check rounds | failed checks sent back to the agent before the job needs you |
| `--job-budget-usd` | `CROWSNEST_JOB_BUDGET_USD` | `0` | Budgets and acceptance → Budget per job ($) | default budget per job in estimated dollars, its delegated jobs included (0: none) |
| `--job-budget-minutes` | `CROWSNEST_JOB_BUDGET_MINUTES` | `0` | Budgets and acceptance → Budget per job (minutes) | default budget per job in minutes since its agent started (0: none) |
| `--job-budget-turns` | `CROWSNEST_JOB_BUDGET_TURNS` | `0` | Budgets and acceptance → Budget per job (turns) | default budget per job in ended turns, its delegated jobs included (0: none) |
| `--job-budget-tokens` | `CROWSNEST_JOB_BUDGET_TOKENS` | `0` | Budgets and acceptance → Budget per job (tokens) | default budget per job in tokens, its delegated jobs included (0: none) |
| `--daily-ceiling-usd` | `CROWSNEST_DAILY_CEILING_USD` | `0` | Budgets and acceptance → Fleet ceiling per day ($) | the fleet's spend ceiling per day in estimated dollars: bounds jobs with no budget of their own and holds new starts once reached (0: none) |
| `--orchestrator-ceiling-usd` | `CROWSNEST_ORCHESTRATOR_CEILING_USD` | `0` | Budgets and acceptance → Orchestrator ceiling per day ($) | the orchestrator's spend ceiling per day; past it automatic pokes wait (0: none) |
| `--lead-ceiling-usd` | `CROWSNEST_LEAD_CEILING_USD` | `0` | Budgets and acceptance → Lead ceiling per day ($) | each project lead's spend ceiling per day where its spend is measured (0: none) |
| `--theme` | `CROWSNEST_THEME` | none | Appearance → Default theme | the theme a browser gets until it chooses its own, such as phosphor (default: the house theme, crowsnest) |
| `--attachment-max-mb` | `CROWSNEST_ATTACHMENT_MAX_MB` | `25` | Attachments → Largest attachment (MB) | largest attachment (pasted, dropped or attached by an agent) in MB |
| `--attachment-quota-mb` | `CROWSNEST_ATTACHMENT_QUOTA_MB` | `1024` | Attachments → Attachment quota (MB) | disk all attachments may take, in MB; past it the oldest files nobody needs are deleted |
| `--browse-root` | `CROWSNEST_BROWSE_ROOTS` | none | no | a directory the folder picker may browse besides your home, projects' and remembered directories, e.g. /srv or /Volumes; `/` only when named here (repeatable) |
| `--require-viewed-before-land` | `CROWSNEST_REQUIRE_VIEWED_BEFORE_LAND` | `false` | Budgets and acceptance → Require all files viewed before Land | refuse to merge or push a job until every file of its diff is marked viewed (off: Land only warns) |
| `--protected-host` | `CROWSNEST_PROTECTED_HOSTS` | none | Guard → Protected hosts | a production host no agent may reach unattended: the guard refuses commands that would, and approvals naming it are dangerous (repeatable) |
| `--protected-hosts-file` | `CROWSNEST_PROTECTED_HOSTS_FILE` | `~/Developer/claude-agent/config/protected-hosts` | no | file of protected hosts, one per line, # comments; read when it exists ('' for none) |
| `--protected-branch` | `CROWSNEST_PROTECTED_BRANCHES` | none | Guard → Protected branches | a branch agents may not commit or push onto, nor force-push (repeatable; default none) |
| `--max-running-jobs` | `CROWSNEST_MAX_RUNNING_JOBS` | `0` | Throttling and review → Running jobs at once | job agents running at once; more wait queued and start in order (0: a quarter of the cores, at least 2) |
| `--load-hold` | `CROWSNEST_LOAD_HOLD` | `2` | Throttling and review → Hold starts above load per core | hold new job starts while the load average per core stays above this for a minute (0: off) |
| `--heavy-slots` | `CROWSNEST_HEAVY_SLOTS` | `2` | Throttling and review → Heavy command slots | heavy commands (builds, test suites) that may run at once through `crowsnest heavy` |
| `--review` | `CROWSNEST_REVIEW` | `auto` | Throttling and review → Review gate | review gate for builder jobs in a worktree: auto (the other engine if installed), same, claude, codex or off |
| `--merge` | `CROWSNEST_MERGE` | `merge` | Throttling and review → Land merges with | how Land merges a job's branch: merge (a merge commit) or ff (fast-forward only) |
| `--digest-at` | `CROWSNEST_DIGEST_AT` | none | Throttling and review → Daily digest at | HH:MM: write the daily digest into the Inbox after this local time (default off) |
| `--terminal-machines` | `CROWSNEST_TERMINAL_MACHINES` | `false` | no | let the Terminal view's herdr client show your saved SSH machines (off: it gets a state directory of crowsnest's own and sees only this session) |
| `--tailscale-auth` | `CROWSNEST_TAILSCALE_AUTH` | `false` | no | sign in tailnet users the owner allowed, from the identity headers of `tailscale serve` on this machine (password login stays) |
| `--backup-every` | `CROWSNEST_BACKUP_EVERY` | `0` | Backups and storage → Automatic backup every | make an automatic backup this often, such as 24h; 0 off |
| `--backup-keep` | `CROWSNEST_BACKUP_KEEP` | `7` | Backups and storage → Automatic backups kept | automatic backups kept; older ones are deleted |
| `--archive-after-days` | `CROWSNEST_ARCHIVE_AFTER_DAYS` | `14` | Backups and storage → Archive settled jobs after (days) | settled jobs leave the boards after this many days (still searchable); 0 never |
| `--delete-after-days` | `CROWSNEST_DELETE_AFTER_DAYS` | `180` | Backups and storage → Delete settled history after (days) | settled jobs, questions, approvals, journal and spend detail are deleted after this many days; 0 never |
| `--engine` | `CROWSNEST_ENGINE` | `auto` | Agents → Default engine | agent engine for the orchestrator and for jobs that name none: auto, claude or codex |
| `--open` | `CROWSNEST_OPEN` | `auto` | no | open a browser at start (the setup link, else the app): auto (only from a desktop terminal), always or never |
| `--no-tailscale-detect` | `CROWSNEST_NO_TAILSCALE_DETECT` | `false` | no | do not add this machine's Tailscale MagicDNS name to the allowed hosts at start |
| `--trust-project-folders` | `CROWSNEST_TRUST_PROJECT_FOLDERS` | `true` | Agents → Trust the folders of projects you create | when you create a project in the app (and leave its box ticked), record Claude Code's and Codex's folder-trust answer for its folder, so its agents start without the trust prompt (false: never write either agent's state) |
| `--tailscale-serve` | `CROWSNEST_TAILSCALE_SERVE` | `false` | Tailnet → Publish to your tailnet | publish crowsnest to your tailnet while it runs, with a foreground `tailscale serve` (needs a login; never Funnel) |
| `--tailscale-serve-port` | `CROWSNEST_TAILSCALE_SERVE_PORT` | `0` | no | the tailnet HTTPS port --tailscale-serve publishes on; 0 picks the first free of 443, 8443 and 10000 |
| `--name` | `CROWSNEST_NAME` | none | no | this instance's name, shown in the header, the page title, notices and Remote Control (default: the short hostname) |
| `--no-remote-control` | `CROWSNEST_NO_REMOTE_CONTROL` | `false` | Agents → Remote Control | start the orchestrator without Claude Code's Remote Control |
| `--job-remote-control` | `CROWSNEST_JOB_REMOTE_CONTROL` | `false` | Agents → Remote Control for jobs | start job agents with Remote Control too, named "<name> <job id> <title>" |
| `--compact-at` | `CROWSNEST_COMPACT_AT` | `150000` | Orchestrator upkeep → Compact past (tokens) | compact the orchestrator once its context passes this many tokens; 0 disables |
| `--fresh-at` | `CROWSNEST_FRESH_AT` | none | Orchestrator upkeep → Fresh conversation at | HH:MM: start the orchestrator on a new conversation once a day after this local time, with a hand-over (default off) |
| `--no-instruction-restart` | `CROWSNEST_NO_INSTRUCTION_RESTART` | `false` | Orchestrator upkeep → Restart for new instructions | do not restart the orchestrator or a lead by itself when its instructions changed since it started (it is shown, and Restart takes them) |

Never changeable from a browser, whatever the database holds: the login, the bind, the port, TLS, allowed hosts and trusted proxies, the herdr session, the data directory and the orchestrator's directory.
