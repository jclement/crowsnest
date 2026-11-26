#!/usr/bin/env bash
# `mise run dev:reset`: back to a clean checkout. Removes build output, the web dist, Vite's
# cache and .dev/ (the dev database and orchestrator directory), and stops and deletes the dev
# herdr session `crowsnest-dev` (or CROWSNEST_DEV_SESSION) with its crowsnest state directory.
#
# Only that one session, by its exact name: never `default`, never another session, never the
# herdr server of the pane this runs in (HERDR_* is removed first, and the session is named).
set -euo pipefail

cd "$(dirname "$0")/.."
session="${CROWSNEST_DEV_SESSION:-crowsnest-dev}"
if [[ ! "$session" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || [[ "$session" == "default" ]]; then
  echo "CROWSNEST_DEV_SESSION=$session is not a usable dev session name (letters, digits, . _ -; never default)" >&2
  exit 2
fi

while IFS= read -r name; do
  unset "$name"
done < <(env | sed -n 's/^\(HERDR_[A-Za-z0-9_]*\)=.*/\1/p')

# The session's status by exact name match on the first column; empty when herdr does not know it.
session_status() {
  herdr session list 2>/dev/null | awk -v want="$1" 'NR > 1 && $1 == want { print $2 }'
}

if command -v herdr >/dev/null 2>&1; then
  status="$(session_status "$session")"
  if [[ "$status" == "running" ]]; then
    herdr session stop "$session" >/dev/null
    echo "stopped herdr session $session"
  fi
  if [[ -n "$(session_status "$session")" ]]; then
    herdr session delete "$session" >/dev/null
    echo "deleted herdr session $session"
  fi
fi

# crowsnest's per-session state (logs, agent socket, job files); same derivation as the config
# loader: $XDG_STATE_HOME/crowsnest/<session>.
rm -rf "${XDG_STATE_HOME:-$HOME/.local/state}/crowsnest/$session"

rm -rf bin dist .dev web/node_modules/.vite
find web/dist -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true
echo "local state cleared (web/node_modules kept; run mise run setup to reinstall)"
