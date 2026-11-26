# syntax=docker/dockerfile:1
#
# The crowsnest devbox (DESIGN.md section 16): crowsnest, herdr and mise in one non-root
# container, so a browser gets a herdr session with Claude Code in it and nothing else on the
# host is reachable except what is mounted and the network.
#
# Three stages:
#   web      builds web/dist (the SPA the binary embeds)
#   build    builds the static crowsnest binary with the ldflags `mise run build` stamps
#   runtime  Debian slim with the tools agents expect, herdr and mise pinned and checksummed
#
# Build it with `mise run image` (local, host arch) or `mise run publish` (multi-arch push);
# the release workflow builds the same file on a v* tag. Everything mutable (Claude auth, mise
# tools, herdr sessions, crowsnest state, projects) lives in the one volume at /home/crow, and
# deploy/entrypoint.sh fills it on first start. Nothing personal and no credential is baked in.

# ---- Versions ------------------------------------------------------------------------------
# Keep NODE_VERSION and GO_VERSION in step with mise.toml so the image builds what CI tests.
ARG NODE_VERSION=26.13.0
ARG GO_VERSION=1.27.3
ARG DEBIAN_RELEASE=trixie

# ---- web: the SPA ---------------------------------------------------------------------------
# $BUILDPLATFORM: the output is platform-independent JS, so build it natively once instead of
# under QEMU for every target architecture.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-${DEBIAN_RELEASE}-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# The login page and footer import the Go binary's taglines file raw (DESIGN.md decision 16).
COPY internal/taglines/taglines.txt /src/internal/taglines/taglines.txt
RUN npm run build

# ---- build: the binary ----------------------------------------------------------------------
# Also $BUILDPLATFORM, and not only for speed: Go under QEMU on Apple silicon has been seen to
# die in the GC mid-download. CGO is off, so cross-compiling with GOOS/GOARCH needs nothing else.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-${DEBIAN_RELEASE} AS build
ARG TARGETOS
ARG TARGETARCH
# The same three values `mise run build` and GoReleaser stamp; the publish task and the release
# workflow pass them, a bare `docker build` gets these placeholders.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/*.go web/
COPY --from=web /src/web/dist/ web/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
      -o /out/crowsnest ./cmd/crowsnest

# ---- runtime --------------------------------------------------------------------------------
FROM debian:${DEBIAN_RELEASE}-slim AS runtime
ARG TARGETARCH

# herdr and mise are pinned with their SHA-256 per architecture. To bump one, take the new
# checksums from the release (herdr also publishes them in https://herdr.dev/latest.json; mise
# in SHASUMS256.txt) and check the GitHub release asset digest agrees.
ARG HERDR_VERSION=0.9.5
ARG HERDR_SHA256_AMD64=14d9e8bc8b4a9e0118d77c32266f76d461f09b40342eb23752960bd89f59929d
ARG HERDR_SHA256_ARM64=17d7536f2da50859523680ef279dffcac8e4a235f4e46fe8da6c2ec44141897c
ARG MISE_VERSION=2026.11.14
ARG MISE_SHA256_AMD64=6fa596291e9758106f761fea4f8e7fac6c3e7ff4714d5c0b9a76e16068db14f6
ARG MISE_SHA256_ARM64=7bd3b2d5ffff077a7662ba186c09600e5b40c1eee9df0c264ed67642ce0a8698

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# The toolbox an agent expects on a dev machine. build-essential is for native npm modules and
# anything else mise compiles; less for git's pager; locales so tools that insist on a named
# UTF-8 locale get one.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      bash build-essential ca-certificates curl git gnupg jq less locales openssh-client \
      procps ripgrep tini unzip xz-utils \
 && sed -i 's/^# *\(en_US.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
 && locale-gen \
 && rm -rf /var/lib/apt/lists/*

# gh from GitHub's own apt repository (Debian's package trails by many releases), installed as
# GitHub documents it: their keyring, a signed-by source.
RUN install -d -m 0755 /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && chmod 0644 /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*

# herdr: the release binary its installer would fetch, but for a pinned version rather than
# whatever latest.json says today, and checked against the pinned digest.
RUN case "$TARGETARCH" in \
      amd64) asset=herdr-linux-x86_64;  sum="$HERDR_SHA256_AMD64" ;; \
      arm64) asset=herdr-linux-aarch64; sum="$HERDR_SHA256_ARM64" ;; \
      *) echo "no herdr build for $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && curl -fsSL --retry 3 -o /usr/local/bin/herdr \
      "https://github.com/herdrdev/herdr/releases/download/v${HERDR_VERSION}/${asset}" \
 && echo "${sum}  /usr/local/bin/herdr" | sha256sum -c - \
 && chmod 0755 /usr/local/bin/herdr

# mise: the standalone release binary (what https://mise.run installs), pinned and checksummed.
# Tools it installs go to the volume (MISE_DATA_DIR below), so they upgrade without a rebuild.
RUN case "$TARGETARCH" in \
      amd64) asset="mise-v${MISE_VERSION}-linux-x64";   sum="$MISE_SHA256_AMD64" ;; \
      arm64) asset="mise-v${MISE_VERSION}-linux-arm64"; sum="$MISE_SHA256_ARM64" ;; \
      *) echo "no mise build for $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && curl -fsSL --retry 3 -o /usr/local/bin/mise \
      "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/${asset}" \
 && echo "${sum}  /usr/local/bin/mise" | sha256sum -c - \
 && chmod 0755 /usr/local/bin/mise

# The one non-root user. uid 1000 so a bind-mounted host directory usually lines up.
RUN groupadd --gid 1000 crow \
 && useradd --uid 1000 --gid 1000 --create-home --home-dir /home/crow --shell /bin/bash crow

# Login shells (herdr panes may start one) reset PATH from /etc/profile; put mise's shims and
# ~/.local/bin back in front so `claude` and the other mise tools resolve in every pane.
COPY deploy/profile.sh /etc/profile.d/crowsnest-devbox.sh

COPY --from=build /out/crowsnest /usr/local/bin/crowsnest
COPY deploy/entrypoint.sh /usr/local/bin/crowsnest-entrypoint
RUN chmod 0755 /usr/local/bin/crowsnest-entrypoint /usr/local/bin/crowsnest

# DISABLE_AUTOUPDATER: Claude Code is upgraded by `mise upgrade`; its own updater would install a
# second copy under ~/.local that the shims shadow anyway.
ENV HOME=/home/crow \
    LANG=en_US.UTF-8 \
    LC_ALL=en_US.UTF-8 \
    SHELL=/bin/bash \
    TERM=xterm-256color \
    MISE_DATA_DIR=/home/crow/.local/share/mise \
    MISE_CONFIG_DIR=/home/crow/.config/mise \
    MISE_CACHE_DIR=/home/crow/.cache/mise \
    PATH=/home/crow/.local/share/mise/shims:/home/crow/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    DISABLE_AUTOUPDATER=1 \
    CROWSNEST_PORT=2769

USER crow
WORKDIR /home/crow
VOLUME /home/crow
EXPOSE 2769

# `crowsnest health` reads the same config as serve; with no --bind it probes 127.0.0.1, which
# a 0.0.0.0 listener answers. start-period covers a first-start `mise install`, which runs
# before serve (see entrypoint.sh) but is capped there.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5m --retries=3 \
  CMD ["crowsnest", "health"]

# tini is PID 1 so the PTY children crowsnest and herdr spawn are reaped and signals forwarded.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/crowsnest-entrypoint"]
CMD []
