# Back up, restore and upgrade

## What is worth keeping

crowsnest's state is one SQLite database (`~/.local/share/crowsnest/<session>.db`), the
attachments beside it, and a few files that steer agents (`~/.config/crowsnest/roles/`,
`tasks/`, `risk.yaml`, and the orchestrator's `tasks/`). herdr and your repositories are separate:
a backup does not include Claude's transcripts, herdr's sessions and scrollback, repositories or
worktrees, or the config file (it may hold the password).

## Back up

From the app: **Download backup**, on **System**'s **Storage** card ([Open System](app:/system))
or in the Settings sheet. From the shell:

```sh
crowsnest backup              # crowsnest-<session>-<time>.tar.gz in the current directory
crowsnest backup --out FILE   # somewhere else
```

It is safe beside a running crowsnest (the database copy is consistent). The archive never holds
a password in clear or any token: login sessions, agent tokens, notification tokens, the Web Push
key and subscriptions, and the notify URL are removed from the copy.

**Automatic backups**: Settings → **Backups and storage** → **Automatic backup every** (for example
`24h`; off by default) and **Automatic backups kept** (7). They go to
`~/.local/share/crowsnest/backups/` and none is made while paused.

## Restore

```sh
crowsnest restore --verify FILE   # check it, write nothing
crowsnest restore FILE            # into this machine's data directory
crowsnest restore --force FILE    # over an existing database (the old one is kept beside it)
```

1. Stop crowsnest (restore refuses while it is running, unless `--force`).
2. `crowsnest restore --verify FILE`, then `crowsnest restore FILE`.
3. Start crowsnest. **The restored instance starts paused**: it first compares the restored jobs
   with the panes herdr really has (the Journal says which still have one), and nothing starts,
   restarts, retries or is poked until you **Resume** (the banner, or the account menu's **Pause automation** switch).
4. Sign in again on every browser, and turn notifications on again on every device.

A restore is all or nothing: every file is checked against the manifest and prepared beside its
destination first; a failure part-way puts everything back. Scheduled task files that start jobs
must be approved again, the backup's **approval rules arrive revoked** (they no longer apply;
`crowsnest restore` says how many; add back the ones you want on each project page), its role files are marked unread, and Remote Control links are forgotten (found again
when the agents start). The replaced database is kept beside the new one,
scrubbed of sessions and tokens like a backup. Treat a backup from somewhere else as you would a script: it carries its
own roles, tasks and risk rules.

## Upgrade

- **Homebrew**: `brew upgrade crowsnest`, then restart it.
- **A downloaded binary**: `crowsnest update --check` reports; `crowsnest update` downloads the
  newest release, verifies its signature and checksum, and replaces the binary. Restart it.
- **The container**: pull the new image and recreate the container; keep the volume.

The footer links to a newer release when one exists. herdr and every agent keep running while
crowsnest restarts.

Before a start that changes the database's schema, or the first start of a different version,
crowsnest copies the database to `backups/pre-migration-…db` (the newest three are kept).
Migrations only go forward: a database written by a newer crowsnest is refused, with what to do.
After an update **System** shows "Updated from vA to vB" with the release notes until dismissed.

## Housekeeping

Settled jobs leave the boards after **Archive settled jobs after (days)** (14) and are deleted,
with their history, after 180 days. A finished worktree job that was never landed is never deleted
by the nightly clean-up. **System → Storage → Clean up now…** shows exactly what it would remove
first.

## What can go wrong

- **"schema newer than this binary"**: you ran an older crowsnest on a newer database. Run the
  newer one, or restore a backup.
- **Restore refused**: crowsnest is running, or a database already exists. Stop it; use `--force`
  if you mean to replace it.
- **Agents missing after a restore**: herdr's panes are not in the backup. The Journal lists the
  restored jobs that have no pane; cancel or restart those.
