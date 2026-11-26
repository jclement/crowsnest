#!/usr/bin/env bash
# `mise run dev:container`: the devbox image built from this checkout (`mise run image` builds
# crowsnest:dev for this machine's arch first), run in the foreground with throwaway-safe
# defaults. Ctrl+C stops it; `mise run dev:container:reset` removes the container and volume.
#
#   - container `crowsnest-dev`, `--rm`, logs in the terminal;
#   - port 127.0.0.1:2770 (CROWSNEST_DEV_CONTAINER_PORT), so it never clashes with `mise run dev`
#     on 2769 and is never reachable from the network;
#   - home in the named volume `crowsnest-dev-home` (first start runs mise install there);
#   - user `crow` and a fresh random password, printed once here. The password reaches the
#     container only as a 0600 file mounted read-only at /run/secrets/crowsnest_password and
#     named by CROWSNEST_PASSWORD_FILE: never in argv, the environment or the image.
set -euo pipefail

cd "$(dirname "$0")/.."

name=crowsnest-dev
volume=crowsnest-dev-home
port="${CROWSNEST_DEV_CONTAINER_PORT:-2770}"
username=crow

if ! [[ "$port" =~ ^[0-9]+$ ]]; then
  echo "CROWSNEST_DEV_CONTAINER_PORT=$port is not a port number" >&2
  exit 2
fi
if [[ -n "$(docker ps -aq --filter "name=^${name}$")" ]]; then
  echo "a container named $name already exists; stop it or run: mise run dev:container:reset" >&2
  exit 1
fi

# The password file lives in a private temporary directory and is removed on exit. The
# container user (uid 1000) must read it, and on Docker Desktop/Colima a bind-mounted file
# keeps the host owner, so it is 0600 on the host and Docker's file sharing does the rest.
secret_dir="$(mktemp -d "${TMPDIR:-/tmp}/crowsnest-dev-secret.XXXXXX")"
trap 'rm -rf "$secret_dir"' EXIT
password="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)"
(umask 077 && printf '%s\n' "$password" >"$secret_dir/crowsnest_password")

cat <<EOF

  crowsnest dev container: http://127.0.0.1:$port
  username: $username
  password: $password        (this run only; not stored anywhere else)

  Ctrl+C stops it. mise run dev:container:reset removes the container and the volume.

EOF
unset password

docker run --rm --name "$name" \
  -p "127.0.0.1:$port:2769" \
  -v "$volume:/home/crow" \
  -v "$secret_dir/crowsnest_password:/run/secrets/crowsnest_password:ro" \
  -e CROWSNEST_USERNAME="$username" \
  -e CROWSNEST_PASSWORD_FILE=/run/secrets/crowsnest_password \
  crowsnest:dev
