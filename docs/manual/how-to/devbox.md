# Run the devbox container

`ghcr.io/jclement/crowsnest` is a ready-made devbox: crowsnest, herdr and mise in one Linux
container (amd64 and arm64), with Claude Code and node installed by mise into a volume on first
start. Use it to give agents a machine of their own, away from your real home directory.

## Start it

With compose (the repository's `deploy/` folder):

```sh
cp deploy/crowsnest.env.sample deploy/crowsnest.env
docker compose -f deploy/compose.yaml up -d
docker compose -f deploy/compose.yaml logs -f crowsnest   # first start: mise install, then the setup link
```

Or plainly:

```sh
docker run -d --name crowsnest -p 127.0.0.1:2769:2769 -v crowsnest-home:/home/crow ghcr.io/jclement/crowsnest
docker logs crowsnest 2>&1 | grep -o 'http://[^"]*/setup#token=[^"]*'
```

1. Open the setup link from the log and choose a username and password. The login is kept in the
   volume.
2. **Sign Claude in**: nothing personal is in the image. Open **Terminal** in the app
   ([Open the Terminal view](app:/terminal)), run `claude`, and complete its login (it prints a URL
   to open on any device). The login is kept in the volume.
3. Clone your projects into the volume (from the terminal), then [create a
   project](projects.md) for each.

## What is in it

| | |
|---|---|
| Base | `debian:trixie-slim`, `tini` as PID 1, user `crow` (uid 1000), `HOME=/home/crow` |
| Baked in | crowsnest, herdr, mise, git, gh, openssh-client, curl, build-essential, ripgrep, jq |
| Volume `/home/crow` | Claude's login, mise's tools, herdr's sessions, crowsnest's database, your projects. Keep it across upgrades. |
| Port | 2769, published on 127.0.0.1 only by `deploy/compose.yaml` |

Every setting is a `CROWSNEST_*` variable in `crowsnest.env` ([Settings and
flags](../reference/settings.md)). Change the port with `CROWSNEST_PORT`, not `--port`, so the
health check probes the same port. For an unattended login set `CROWSNEST_USERNAME` and mount the
password as a file (`deploy/crowsnest_password`, mode 0600, uid 1000).

## Reach it

Do not publish the port to the internet. `deploy/compose.yaml` has commented sidecars for
Tailscale and cloudflared: put one in front. Behind either, crowsnest sees every client as
loopback, so set `CROWSNEST_TRUSTED_PROXY=loopback` (and for cloudflared only,
`CROWSNEST_TRUSTED_PROXY_HEADER=cf-connecting-ip`).

## Upgrade

Tools: `mise upgrade` in the web terminal (Claude Code's own auto-updater is off in the image).
crowsnest, herdr or mise: pull a new image and recreate the container; the volume carries over.

## For crowsnest development

`mise run dev:container` builds the image for this machine and runs it in the foreground on
`127.0.0.1:2770`, printing the setup link; `mise run dev:container:reset` removes it and its volume.

## What it does and does not protect

Whoever signs in has a shell as `crow`, and so does every agent in the container. They reach what
is mounted and what the network reaches, nothing else on the host. Mount only what you would hand
an agent: never the Docker socket or your real home. Agents run as the same user as crowsnest, so
they can read any environment in the container: prefer files for secrets (the password file), and
know that `ELEVENLABS_API_KEY` and the notify URL set in `crowsnest.env` are readable by agents.
More in [Safety](../safety.md).

## What can go wrong

- **The setup link is not in the log yet**: the first start runs `mise install` first (up to five
  minutes); keep following the log.
- **Claude is not signed in**: jobs fail with "Claude needs a login or billing fix". Sign in from
  the Terminal view.
- **"Host not allowed" behind a proxy**: add its name with `CROWSNEST_ALLOWED_HOSTS`.
