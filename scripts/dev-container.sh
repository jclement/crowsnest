#!/usr/bin/env bash
# `mise run dev:container`: the devbox image built from this checkout (`mise run image` builds
# crowsnest:dev for this machine's arch first), run in the foreground with throwaway-safe
# defaults. Ctrl+C stops it; `mise run dev:container:reset` removes the container and volume.
#
#   - container `crowsnest-dev`, `--rm`, logs in the terminal;
#   - port 127.0.0.1:2770 (CROWSNEST_DEV_CONTAINER_PORT), so it never clashes with `mise run dev`
#     on 2769 and is never reachable from the network. crowsnest listens on the same port inside
#     the container, so the setup link it prints opens as is;
#   - home in the named volume `crowsnest-dev-home` (first start runs mise install there);
#   - no credentials: on the first run crowsnest starts locked in first-run setup mode and
#     prints a setup link with a one-time token (DESIGN.md 4.14); this script prints it again,
#     plainly, once it appears. Open it and choose a username and password. They are stored in
#     the volume, so later runs ask you to sign in; `mise run dev:container:reset` starts over.
set -euo pipefail

cd "$(dirname "$0")/.."

name=crowsnest-dev
volume=crowsnest-dev-home
port="${CROWSNEST_DEV_CONTAINER_PORT:-2770}"

if ! [[ "$port" =~ ^[0-9]+$ ]]; then
  echo "CROWSNEST_DEV_CONTAINER_PORT=$port is not a port number" >&2
  exit 2
fi
if [[ -n "$(docker ps -aq --filter "name=^${name}$")" ]]; then
  echo "a container named $name already exists; stop it or run: mise run dev:container:reset" >&2
  exit 1
fi

printf '\n  crowsnest dev container: http://127.0.0.1:%s\n' "$port"
printf '  First run: open the setup link it prints (repeated below) and choose a login.\n'
printf '  Later runs: sign in with that login. mise run dev:container:reset starts over.\n'
printf '  Ctrl+C stops it.\n\n'

# Repeat the setup link plainly once crowsnest has printed it (its log in the container is
# JSON). A background watcher on `docker logs`, stopped when this script exits.
watch_for_link() {
  local link
  for _ in $(seq 1 600); do
    link="$(docker logs "$name" 2>&1 | grep -o 'http://[^"]*/setup#token=[A-Za-z0-9_-]*' | tail -n 1 || true)"
    if [[ -n "$link" ]]; then
      printf '\n  >>> setup link: %s\n\n' "$link" >&2
      return 0
    fi
    if docker logs "$name" 2>&1 | grep -q 'login: the owner stored at setup'; then
      return 0
    fi
    sleep 1
  done
}
watch_for_link &
watcher=$!
trap 'kill "$watcher" 2>/dev/null || true' EXIT

docker run --rm --name "$name" \
  -p "127.0.0.1:$port:$port" \
  -e CROWSNEST_PORT="$port" \
  -v "$volume:/home/crow" \
  crowsnest:dev
