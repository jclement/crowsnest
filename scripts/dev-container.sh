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
#     container only as a 0600 file owned by crow, mounted read-only at
#     /run/secrets/crowsnest_password and named by CROWSNEST_PASSWORD_FILE: never in argv, the
#     environment or the image.
#
# The secret is a throwaway named volume (`crowsnest-dev-secret`, removed on exit), filled
# through stdin, rather than a bind-mounted host file: Colima's and Docker Desktop's file
# sharing do not carry a host file's mode or owner into the container (a 0600 file shows up as
# 0755 there, and crowsnest refuses a password file others can read).
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

secret_volume=crowsnest-dev-secret
cleanup() { docker volume rm -f "$secret_volume" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

# 18 random bytes as 24 base64 characters (no padding), with + and / swapped for letters so the
# password is easy to copy. A finite read: `tr </dev/urandom | head` dies of SIGPIPE under pipefail.
password="$(head -c 18 /dev/urandom | base64 | tr '+/' 'xy')"

# Write it into the secret volume from stdin, as root in a one-off container of the same image,
# then hand the directory (0700) and file (0600) to crow (uid 1000).
docker volume create "$secret_volume" >/dev/null
printf '%s\n' "$password" | docker run --rm -i --user 0:0 --entrypoint /bin/sh \
  -v "$secret_volume:/secret" crowsnest:dev \
  -c 'umask 077 && cat >/secret/crowsnest_password && chown -R 1000:1000 /secret && chmod 700 /secret'

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
  -v "$secret_volume:/run/secrets:ro" \
  -e CROWSNEST_USERNAME="$username" \
  -e CROWSNEST_PASSWORD_FILE=/run/secrets/crowsnest_password \
  crowsnest:dev
