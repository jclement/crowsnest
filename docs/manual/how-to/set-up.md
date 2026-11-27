# Set up on a new machine

What you need: macOS or Linux, [herdr](https://herdr.dev) 0.9.5 or newer, and Claude Code (2.1 or
newer) or Codex, installed and signed in. crowsnest starts without them and tells you what is
missing.

## Install

```sh
brew install jclement/tap/crowsnest   # macOS and Linux; brings herdr
```

Or download a release archive from GitHub and verify it (the README's **Install** section has the
cosign commands), or build from source (`mise run setup`, then `mise run build`, which writes
`./bin/crowsnest`).

## First run

1. Run `crowsnest` with no flags. It binds `localhost:2769`, prints a one-time **setup link** and
   opens it in your browser (when it can: it does not try over SSH, in a container, or without a
   terminal). The first lines say what it found, for example `herdr 0.9.5 ✓  claude 2.1.332 ✓
   signed in  codex not found`.
2. On the setup page choose a **username**, a **password** (at least 12 characters; a few
   unrelated words make a good one) and the instance's **name** (the short hostname by default).
   The token in the link works once and is new at every start; anyone who can read crowsnest's
   log can complete setup, which is the point: that is you.
3. A **checklist** follows: herdr and its session, Claude Code (installed, signed in), Codex, and
   the orchestrator's folder. Anything missing shows the exact command for your OS, and the list
   re-checks itself every few seconds.
   - Claude's sign-in needs a terminal: **Open a terminal to sign in** opens one in the page with
     the command typed.
   - With both Claude Code and Codex, it asks which runs the orchestrator.
   - Optional steps are folded away: the name, a voice key, notifications on this device, and the
     `tailscale serve` command for reaching it from a phone.
4. Press **Start**. The orchestrator starts. The first time, Claude asks whether it trusts the
   orchestrator's folder: the question appears right there as a card with its buttons. Answer it.
5. You land in **Chat**, where the orchestrator says hello. Done.

Nothing else is needed: no config file, no flags. [Open System](app:/system) any time for the same
checks live.

## Check it: `crowsnest doctor`

```sh
crowsnest doctor          # one line per check, with the fix for anything not fine
crowsnest doctor --deep   # also one tiny real call per agent ("reply OK", a few tokens)
crowsnest doctor --json   # for scripts
```

It only reads: it never creates a directory, starts herdr or calls a model (unless `--deep`).
Exit 0 when nothing failed (warnings included), 1 when something did. Every check and its fix:
[Doctor checks](../troubleshooting/doctor.md).

## Keep it running

crowsnest is one process. herdr and every agent keep running when it stops, and it adopts them
again when it starts. To run it at login, use whatever you use for services (a launchd agent with
`KeepAlive` on macOS, a systemd user unit with `Restart=always` on Linux). An update installed
from the app's API exits afterwards and expects to be started again ([Back up, restore and
upgrade](backup-restore-upgrade.md)).

## Set it up unattended

Give the login in the environment instead of the setup page, and nothing is asked:

```sh
CROWSNEST_PASSWORD='something long' crowsnest --username jeff
# or --password-file ~/.config/crowsnest/password (chmod 600)
```

Every setting has a flag, an environment variable and a config-file key: [Settings and
flags](../reference/settings.md). The container image is set up this way: [Run the devbox
container](devbox.md).

## What can go wrong

- **Port in use**: another crowsnest, or something else, is on 2769. `crowsnest doctor` tells them
  apart; start with `--port N`.
- **"do you trust this folder?" every time**: Claude asks once per directory; answer it in the
  card or the terminal. Projects' directories get the same question the first time a lead or job
  starts there.
- **Claude not signed in**: `claude` in a terminal, then `/login`. The checklist notices.
- **The setup link expired**: it is new at every start. Restart crowsnest and use the new link,
  or `crowsnest reset-setup` to start over (asks first).
- **Forgot the password**: `crowsnest reset-password` on the machine.

More: [Common problems](../troubleshooting/common-problems.md).
