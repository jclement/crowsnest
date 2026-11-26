#!/usr/bin/env bash
# `mise run dev`: the real crowsnest, built from this checkout and run locally against a
# dedicated testing herdr session, with a real Claude orchestrator. `mise run dev:demo` is the
# old fake-herdr mode; `mise run dev:reset` tears all of this down.
#
# Isolation from a real instance, and from the herdr session this may be started in:
#
#   - session `crowsnest-dev` (CROWSNEST_DEV_SESSION overrides it; `default` is refused), so the
#     state directory, the agent socket and the herdr session are this instance's alone;
#   - the database and the orchestrator's directory live under .dev/ in the repo (gitignored),
#     and an empty .dev/config.yaml stands in for ~/.config/crowsnest/config.yaml, so nothing from
#     a real install's config, database or credentials is read;
#   - every HERDR_* variable is removed before crowsnest starts, so running this inside a herdr
#     pane cannot address that pane's session (crowsnest also scrubs them from its own children);
#   - no login, localhost only.
#
# The orchestrator's directory is new and untrusted, so the first start stops at Claude's
# folder-trust prompt: answer it once in the Terminal view (or `herdr --session crowsnest-dev`).
#
# The UI: Vite on :5173 with hot reload, proxying /api to the Go server on CROWSNEST_PORT
# (default 2769). Open http://localhost:5173.
set -euo pipefail

cd "$(dirname "$0")/.."
root="$PWD"
session="${CROWSNEST_DEV_SESSION:-crowsnest-dev}"
port="${CROWSNEST_PORT:-2769}"

if [[ ! "$session" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || [[ "$session" == "default" ]]; then
  echo "CROWSNEST_DEV_SESSION=$session is not a usable dev session name (letters, digits, . _ -; never default)" >&2
  exit 2
fi

dev="$root/.dev"
mkdir -p "$dev/data" "$dev/orchestrator"
chmod 700 "$dev"
[[ -f "$dev/config.yaml" ]] || : >"$dev/config.yaml"

# Drop every HERDR_* variable (HERDR_ENV, HERDR_SOCKET_PATH, HERDR_PANE_ID, ...), and anything
# that would turn the login back on or point at another instance's files.
while IFS= read -r name; do
  unset "$name"
done < <(env | sed -n 's/^\(HERDR_[A-Za-z0-9_]*\)=.*/\1/p')
unset CROWSNEST_USERNAME CROWSNEST_PASSWORD CROWSNEST_PASSWORD_FILE CROWSNEST_DEMO \
  CROWSNEST_BIND SUPPRESS_AUTH

export CROWSNEST_CONFIG="$dev/config.yaml"
export CROWSNEST_DATA_DIR="$dev/data"
export CROWSNEST_DIR="$dev/orchestrator"
export CROWSNEST_SESSION="$session"
export CROWSNEST_PORT="$port"
export CROWSNEST_NO_AUTH=true

echo "crowsnest dev: herdr session $session, data in .dev/, API on http://127.0.0.1:$port, UI on http://localhost:5173"
echo "first start: answer Claude's folder-trust prompt for .dev/orchestrator in the Terminal view"

# Ctrl+C (or either process exiting) stops both: the whole process group goes.
trap 'kill 0' EXIT INT TERM
go run ./cmd/crowsnest serve --web-dev --bind localhost &
server=$!

# Vite must not come up before the API does: `go run` compiles first (tens of seconds after a
# pull), and a browser tab left open on :5173 would meanwhile fill the log with proxy
# ECONNREFUSED errors that look like a broken server.
echo "building and starting the Go server (the first build after a pull takes a while)..."
until curl -fsS -o /dev/null "http://127.0.0.1:$port/api/v1/health" 2>/dev/null; do
  if ! kill -0 "$server" 2>/dev/null; then
    echo "the Go server exited before it started listening; its error is above" >&2
    exit 1
  fi
  sleep 0.5
done
echo "API is up; starting Vite"
npm --prefix web run dev -- --clearScreen false
