#!/usr/bin/env bash
# Build the devbox image for linux/amd64 and linux/arm64 and push it to GHCR as
# ghcr.io/jclement/crowsnest:<version> and :latest (DESIGN.md section 16). `mise run publish`.
#
# The release workflow does the same on every v* tag; this is for pushing by hand. The build is
# entirely inside deploy/Dockerfile (web and Go stages), so it needs Docker with buildx and no
# local toolchain. Shape follows timecop's packaging/publish.sh.
#
#   mise run publish            # version from `git describe`
#   mise run publish -- v1.2.3  # explicit version
#
# Environment:
#   GHCR_TOKEN          a token with write:packages; else `gh auth token` is used
#   GHCR_USER           the account it belongs to; else `gh api user`
#   CROWSNEST_IMAGE     the repository (default ghcr.io/jclement/crowsnest)
#   CROWSNEST_PLATFORMS default linux/amd64,linux/arm64
#   CROWSNEST_ALLOW_DIRTY=1 to push from a dirty tree
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${CROWSNEST_IMAGE:-ghcr.io/jclement/crowsnest}"
PLATFORMS="${CROWSNEST_PLATFORMS:-linux/amd64,linux/arm64}"
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
REGISTRY="${IMAGE%%/*}"

# An image whose tag does not match its source wastes an afternoon during an incident.
if [ -z "${CROWSNEST_ALLOW_DIRTY:-}" ] && [ -n "$(git status --porcelain)" ]; then
  echo >&2 "Working tree is dirty; ${IMAGE}:${VERSION} would not match its own source."
  echo >&2 "Commit first, or set CROWSNEST_ALLOW_DIRTY=1 if you know why you want this."
  exit 1
fi

# ---- Credentials ----------------------------------------------------------------------------
# GHCR_TOKEN first (CI, a one-off export), then the gh CLI's own token, which needs the
# write:packages scope (`gh auth refresh -s write:packages`). The token only ever goes to
# `docker login` on stdin, never into argv.
TOKEN="${GHCR_TOKEN:-}"
if [ -z "$TOKEN" ] && command -v gh >/dev/null 2>&1; then
  TOKEN="$(gh auth token 2>/dev/null || true)"
fi
if [ -z "$TOKEN" ]; then
  echo >&2 "No GHCR token. Set GHCR_TOKEN (write:packages) or sign in with gh:"
  echo >&2 "  gh auth login && gh auth refresh -s write:packages"
  exit 1
fi
USER_NAME="${GHCR_USER:-$(gh api user -q .login 2>/dev/null || true)}"
if [ -z "$USER_NAME" ]; then
  echo >&2 "Could not tell which GitHub account the token belongs to; set GHCR_USER."
  exit 1
fi

# ---- A throwaway Docker config --------------------------------------------------------------
# So the credential never lands in ~/.docker/config.json. Two things the redirect would lose,
# both learned on Colima: the current context (resolve DOCKER_HOST from the real config first,
# or the build dies on the default socket) and the CLI plugins (symlink them, or buildx vanishes
# as "unknown flag: --platform").
if [ -z "${DOCKER_HOST:-}" ]; then
  DOCKER_HOST="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)"
  [ -n "$DOCKER_HOST" ] && export DOCKER_HOST
fi
REAL_DOCKER_CONFIG="${DOCKER_CONFIG:-$HOME/.docker}"
DOCKER_CONFIG="$(mktemp -d)"
export DOCKER_CONFIG
trap 'docker buildx rm crowsnest-publish >/dev/null 2>&1 || true; rm -rf "$DOCKER_CONFIG"' EXIT
[ -d "$REAL_DOCKER_CONFIG/cli-plugins" ] && ln -s "$REAL_DOCKER_CONFIG/cli-plugins" "$DOCKER_CONFIG/cli-plugins"
for plugin_dir in /opt/homebrew/lib/docker/cli-plugins /usr/local/lib/docker/cli-plugins; do
  [ -e "$DOCKER_CONFIG/cli-plugins" ] || { [ -d "$plugin_dir" ] && ln -s "$plugin_dir" "$DOCKER_CONFIG/cli-plugins"; }
done

echo "Logging in to ${REGISTRY} as ${USER_NAME}…"
printf '%s' "$TOKEN" | docker login "$REGISTRY" -u "$USER_NAME" --password-stdin

# A docker-container builder: the default docker driver cannot build a multi-platform image.
# arm64-on-amd64 (or the reverse) runs under QEMU; Colima and Docker Desktop ship binfmt for it.
docker buildx create --name crowsnest-publish --driver docker-container --use >/dev/null

echo "Building and pushing ${IMAGE}:${VERSION} and :latest (${PLATFORMS})…"
docker buildx build \
  --platform "$PLATFORMS" \
  -f deploy/Dockerfile \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$COMMIT" \
  --build-arg BUILD_DATE="$BUILD_DATE" \
  --label org.opencontainers.image.source=https://github.com/jclement/crowsnest \
  --label org.opencontainers.image.version="$VERSION" \
  --label org.opencontainers.image.revision="$(git rev-parse HEAD)" \
  --label org.opencontainers.image.created="$BUILD_DATE" \
  -t "${IMAGE}:${VERSION}" -t "${IMAGE}:latest" \
  --push .

echo
echo "Pushed ${IMAGE}:${VERSION}"
echo "   and ${IMAGE}:latest"
