# Doctor checks and their fixes

`crowsnest doctor` checks everything crowsnest depends on, read-only, in a few seconds, and prints
one line per check with a fix for anything that is not fine. The **System** page
([Open System](app:/system)) shows the same report from the running server, with **Re-check** and
**Copy diagnostics** (the report as text, with no secrets and your home directory as `~`, for an
issue). The first-run checklist is the same report too, so the three never disagree.

```sh
crowsnest doctor          # coloured on a terminal, plain when piped
crowsnest doctor --json   # for scripts
crowsnest doctor --deep   # also one tiny real call per agent ("reply OK", a few tokens) and the voice key
```

Each check is **ok**, **info**, **warn**, **fail** or **skip** (a shape as well as a colour).
Exit 0 when nothing failed (warnings included), 1 when something did, 2 for a usage error.

## herdr

| Check | What it looks at | Typical fix |
|---|---|---|
| **herdr installed** | On `PATH`, at least 0.9.5 (socket protocol 22). | `brew install herdr`, or `curl -fsSL https://herdr.dev/install.sh \| sh`, or `mise use -g herdr`. |
| **herdr session "crowsnest"** | Whether the session is reachable. A session that is not running is fine: "crowsnest will start it". | Nothing, or check `herdr session list --json` runs. |
| **herdr first-run screen** | herdr's `onboarding = false` in its `config.toml`, so a started session does not stop at herdr's welcome. | Run herdr once and finish its first-run screen. |

## Agents

| Check | What it looks at | Typical fix |
|---|---|---|
| **Claude Code** | Path, version, signed in (from `claude auth status`, never a model call; the account's email is never kept). | Install Claude Code; `claude`, then `/login`. |
| **Codex** | Path, version, signed in (`codex login status`). Optional. | Install and sign in, or ignore. |
| **Orchestrator and jobs run on** | What `--engine` resolves to. | Settings → Agents → **Default engine**. |

## Claude Code

| Check | What it looks at | Typical fix |
|---|---|---|
| **Claude Code flags crowsnest uses** | `--settings`, `--mcp-config`, `--append-system-prompt-file`, `--session-id`, `--remote-control`, `--permission-mode` are accepted. | Upgrade Claude Code. |
| **Claude Code hooks** | A version whose hooks crowsnest relies on (2.1 or newer). | Upgrade Claude Code. |
| **Orchestrator folder trusted** | Whether Claude already trusts the orchestrator's folder (or a parent). | Nothing: answer the trust question once when it appears. |
| **Claude transcripts readable** | The transcript directory crowsnest reads conversations and spend from. | Check `CLAUDE_CONFIG_DIR` and permissions. |

## The machine

| Check | What it looks at | Typical fix |
|---|---|---|
| **Data directory**, **State directory**, **Config directory** | Writable and private (0700). | `chmod 700` the directory, or fix its owner. |
| **Database** | Opens (read-only for the check); its schema against this binary's. | A newer schema: run the newer crowsnest, or restore a backup. |
| **Agent socket path** | Short enough for a unix socket (103 bytes on macOS, 107 on Linux). | A shorter `--data-dir` or `XDG_RUNTIME_DIR`. |
| **Port 2769** | Free, or already a running crowsnest (asked through its health endpoint). | `crowsnest --port N`, or use the one already running. |
| **Listens on** | What the bind resolves to, and a tailnet address. | See [Reach it from your phone](../how-to/phone.md). |
| **Secure context (voice, push)** | Whether browsers will get HTTPS or localhost. | `tailscale serve`, or `--tls-cert` and `--tls-key`. |
| **Disk space** | Warn under 2 GiB free, fail under 200 MiB. | Free space; **System → Storage → Clean up now…**. |
| **git** | Installed. | Install git. |
| **Clock** | Not wildly off. | Fix the system clock (sign-in and push depend on it). |

## Tailscale

| Check | What it looks at | Typical fix |
|---|---|---|
| **Tailscale** | Installed and connected. Optional. | Install Tailscale, or ignore. |
| **Tailnet name allowed** | The MagicDNS name is on the host allowlist (detected at start). | Restart without `--no-tailscale-detect`, or `--allowed-host`. |
| **Tailscale CLI** | Which `tailscale` crowsnest would run (on `PATH`, or the macOS app's in `/Applications/Tailscale.app`) and whether it is 1.52 or newer, which publishing needs. | Update Tailscale. |
| **Tailscale HTTPS certificates** | Enabled for the tailnet. | Tailscale admin console → DNS → HTTPS Certificates. |
| **Published on the tailnet over HTTPS** | Something (crowsnest itself, or a `tailscale serve` you ran) already proxies to the port. | Publish from System ([Publish to your tailnet](../how-to/phone.md)), or `tailscale serve --bg 2769` by hand. |

## Optional features

| Check | What it looks at |
|---|---|
| **Talk mode (ElevenLabs)** | A key is configured (`--deep` also checks it works). |
| **Web Push** | The server's push keys exist. |
| **Notify URL** | Set or not (its value is never shown). |
| **Updates** | Whether a newer release exists. |

## Configuration

| Check | What it looks at | Typical fix |
|---|---|---|
| **Configuration loads** | The config file, flags and environment parse, and where every non-default setting came from. | The message names the bad key or value. |
| **Startup rules** | The refusals (`--no-auth` on an open bind, `--session default`, an ambiguous login) would not stop a start. | Do what the message says. |
| **Configuration warning** | Things that work but should not be so (a password in a readable config file). | `chmod 600`, or move the password to a file. |
| **CROWSNEST_\* variables** | Variables no option reads, with the nearest real name ("did you mean…"). | Fix the typo. |

## The running instance

Against a running crowsnest, doctor also asks it (over its agent socket): **Setup** (waiting for
first-run setup), **Orchestrator** (its state), **Uptime**, **Hooks seen firing**, and its last
warnings and errors.

## What can go wrong

- **Everything is green but jobs still fail**: run `crowsnest doctor --deep`; it makes a real call
  per agent, which catches a login that `auth status` still reports as signed in.
- **doctor and System disagree**: they read the same report; System is the running server's view
  (its environment, its config), doctor is your shell's. A variable set in your shell but not the
  service's shows the difference.
