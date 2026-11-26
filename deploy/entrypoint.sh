#!/bin/sh
# Entrypoint of the crowsnest devbox image (deploy/Dockerfile, DESIGN.md section 16).
#
# Runs under tini as user crow, every start:
#   1. refuses to start without credentials (or an explicit, doubly confirmed open mode);
#   2. on first start only, seeds a default mise config (node LTS, Claude Code) and a herdr
#      config that skips herdr's first-run screen; an existing file is never touched;
#   3. runs `mise install` so tools land in the /home/crow volume, waiting for it at most
#      CROWSNEST_MISE_WAIT seconds; a slow or failed install is reported loudly and never stops
#      crowsnest from starting (a slow one carries on in the background);
#   4. execs `crowsnest serve --bind 0.0.0.0` with any extra arguments passed through.
#
# Secrets (R1b N1): every agent runs as crow, the same uid as this script, tini and crowsnest, so
# whatever sits in an environment here is readable by an agent through /proc/<pid>/environ.
#   - Sidecar tokens (TUNNEL_TOKEN, TS_AUTHKEY) belong to other containers. They are dropped at
#     once, so a .env shared with a sidecar by mistake still never reaches crowsnest or a pane.
#   - crowsnest's own secrets are kept out of every child this script starts (mise install), and
#     crowsnest re-executes itself without them (see cmd/crowsnest/secretenv.go), so neither its
#     own environment nor any pane's holds them.
#   - What no script can change: tini, PID 1, keeps the environment Docker started it with. A
#     password given as CROWSNEST_PASSWORD is therefore readable in /proc/1/environ. Give it as a
#     file (CROWSNEST_PASSWORD_FILE, a compose secret) to keep it out of there too; the start-up
#     log says so when it was not.
#
# POSIX sh on purpose (shellcheck-clean): it must run before mise has installed anything.
set -eu

state_dir="$HOME/.local/state/crowsnest"
mise_config="${MISE_CONFIG_DIR:-$HOME/.config/mise}/config.toml"
herdr_config="$HOME/.config/herdr/config.toml"
install_log="$state_dir/mise-install.log"
install_status="$state_dir/mise-install.status"
mise_wait="${CROWSNEST_MISE_WAIT:-300}"

log() { printf 'crowsnest-entrypoint: %s\n' "$*" >&2; }
die() { log "$*"; exit 64; }

# ---- 1. Credentials -------------------------------------------------------------------------
# crowsnest refuses an unauthenticated start too, but this says it in container terms (which
# env vars, which file) before anything is written to the volume.
check_credentials() {
  if [ "${CROWSNEST_NO_AUTH:-}" = "true" ]; then
    [ "${CROWSNEST_I_KNOW_THIS_IS_OPEN:-}" = "true" ] && {
      log "WARNING: no login. Anyone who reaches this port has a shell as crow in this container."
      return 0
    }
    die "CROWSNEST_NO_AUTH=true needs CROWSNEST_I_KNOW_THIS_IS_OPEN=true as well: without a login, anyone who reaches the port gets a shell. Set a username and password instead (CROWSNEST_USERNAME plus CROWSNEST_PASSWORD or CROWSNEST_PASSWORD_FILE)."
  fi
  [ -n "${CROWSNEST_USERNAME:-}" ] ||
    die "no credentials: set CROWSNEST_USERNAME plus CROWSNEST_PASSWORD or CROWSNEST_PASSWORD_FILE (see deploy/crowsnest.env.sample). A web terminal is a shell; it does not start open."
  if [ -n "${CROWSNEST_PASSWORD_FILE:-}" ]; then
    [ -r "$CROWSNEST_PASSWORD_FILE" ] ||
      die "CROWSNEST_PASSWORD_FILE=$CROWSNEST_PASSWORD_FILE is not a readable file (mount it, e.g. as a Docker secret)."
    return 0
  fi
  [ -n "${CROWSNEST_PASSWORD:-}" ] ||
    die "CROWSNEST_USERNAME is set but there is no password: set CROWSNEST_PASSWORD or CROWSNEST_PASSWORD_FILE."
}

# The variables holding crowsnest's own secrets: kept from every child but crowsnest itself.
secret_vars="CROWSNEST_PASSWORD ELEVENLABS_API_KEY CROWSNEST_NOTIFY_URL"

# Sidecar tokens: never crowsnest's to hold.
drop_sidecar_tokens() {
  for name in TUNNEL_TOKEN TS_AUTHKEY; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then
      log "WARNING: $name is in crowsnest's environment; it belongs to a sidecar's own env file. Dropped, so no agent can read it here (but PID 1 still holds it: move it out of crowsnest.env)."
    fi
    unset "$name"
  done
}

warn_env_secrets() {
  [ -n "${CROWSNEST_PASSWORD:-}" ] &&
    log "NOTE: the password came as CROWSNEST_PASSWORD. Agents run as the same user and can read it in /proc/1/environ; mount it as a file and set CROWSNEST_PASSWORD_FILE instead (see deploy/compose.yaml)."
  return 0
}

# ---- 2. First-start seeding -----------------------------------------------------------------
check_home() {
  [ -w "$HOME" ] ||
    die "$HOME is not writable by uid $(id -u). A bind-mounted directory must belong to uid 1000 (chown 1000:1000 it), or use a named volume."
}

# The default tool set. Claude Code comes from mise's registry entry `claude`
# (aqua:anthropics/claude-code, the native binary with checksums), so `mise upgrade` updates it
# in the volume without rebuilding the image. Edit the file to add tools; it is never rewritten.
seed_mise_config() {
  [ -e "$mise_config" ] && return 0
  mkdir -p "$(dirname "$mise_config")"
  cat >"$mise_config" <<'EOF'
# Seeded by the crowsnest devbox on first start; yours to edit (it is never overwritten).
# Tools install into the /home/crow volume. Upgrade with `mise upgrade`, add with `mise use -g`.
[tools]
node = "lts"
claude = "latest"   # Claude Code (aqua:anthropics/claude-code)

[settings]
# No prompts: nobody is at a terminal when the entrypoint runs this.
yes = true
EOF
  log "seeded $mise_config (node LTS, Claude Code)"
}

# herdr shows a first-run screen in its TUI; in a fresh container that would be the first thing
# the browser's Terminal view shows. Skip it, once, the way the real-herdr E2E does.
seed_herdr_config() {
  [ -e "$herdr_config" ] && return 0
  mkdir -p "$(dirname "$herdr_config")"
  printf '# Seeded by the crowsnest devbox on first start; yours to edit.\nonboarding = false\n' >"$herdr_config"
  log "seeded $herdr_config (skips herdr's first-run screen)"
}

# ---- 3. mise install ------------------------------------------------------------------------
# Run detached (the subshell exits at once, so tini adopts and reaps the install) and wait for
# it up to $mise_wait seconds. Waiting means Claude Code is usually on PATH before crowsnest
# starts its orchestrator; the cap means a slow network never keeps the UI down.
run_mise_install() {
  rm -f "$install_status"
  log "mise install: starting (log: $install_log; waiting up to ${mise_wait}s)"
  (
    (
      # shellcheck disable=SC2086 # the names are a fixed, space-separated list
      unset $secret_vars
      started=$(date +%s)
      if mise install >"$install_log" 2>&1; then code=0; else code=$?; fi
      echo "$code $(($(date +%s) - started))" >"$install_status"
    ) &
  )
  waited=0
  while [ ! -s "$install_status" ] && [ "$waited" -lt "$mise_wait" ]; do
    sleep 1
    waited=$((waited + 1))
  done
  report_mise_install
}

report_mise_install() {
  if [ ! -s "$install_status" ]; then
    log "WARNING: mise install is still running after ${mise_wait}s; starting crowsnest anyway."
    log "WARNING: tools (Claude Code) appear when it finishes; follow it with: tail -f $install_log"
    return 0
  fi
  read -r code seconds <"$install_status"
  if [ "$code" -eq 0 ]; then
    log "mise install: done in ${seconds}s"
    return 0
  fi
  log "WARNING: mise install FAILED (exit $code after ${seconds}s); starting crowsnest anyway."
  log "WARNING: tools it should have installed (Claude Code) may be missing. Last lines of $install_log:"
  tail -n 20 "$install_log" >&2 || true
  log "WARNING: fix it from the web terminal with: mise install"
}

# ---- main -----------------------------------------------------------------------------------
check_credentials
drop_sidecar_tokens
warn_env_secrets
check_home
mkdir -p "$state_dir"
seed_mise_config
seed_herdr_config
run_mise_install

log "starting crowsnest on 0.0.0.0:${CROWSNEST_PORT:-2769}"
exec crowsnest serve --bind 0.0.0.0 "$@"
