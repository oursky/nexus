#!/usr/bin/env bash
# scripts/remote/deploy.sh
# Cross-compile nexus for linux/amd64 and deploy to a remote host.
# Also stages the linux binary to packages/nexus-swift/Resources/nexus-linux-amd64
# so the Mac app's provision endpoint always uploads the same binary that was
# just built — never a stale embedded copy.
#
# When cmd/nexus/nexus-libkrun-vm exists (committed pre-built CGO helper),
# this script automatically downloads the libkrun shared libraries from smolvm
# releases and builds a full libkrun-enabled nexus binary (no SSH to Linux
# needed for CGO).  Only rebuild nexus-libkrun-vm itself (rarely) via:
#   scripts/remote/deploy-libkrun.sh
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN='$HOME/.local/bin/nexus-dev'] scripts/remote/deploy.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus-dev}"
# v0.5.20 libkrun is built without virtio-net symbols.
# v0.5.19 exports krun_add_net_unixstream and krun_set_passt_fd.
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
SWIFT_RESOURCES="$SCRIPT_DIR/../../packages/nexus-swift/Resources"
EMBED_DIR="$NEXUS_PKG/cmd/nexus"

# Build timestamp and git commit for build.Info() traceability.
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "$SCRIPT_DIR/../.." rev-parse --short HEAD 2>/dev/null || echo dev)"
LDFLAGS="-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Time=${BUILD_TIME} \
         -X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Commit=${GIT_COMMIT}"

# Build the guest agent for linux/amd64 and stage it for embedding.
# The //go:embed directive in embed_agent_linux_amd64.go reads agent-linux-amd64
# at compile time; build it first so the embedded binary is always current.
echo "Building nexus-guest-agent for linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -C "$NEXUS_PKG" \
  -ldflags "$LDFLAGS" \
  -o ./tmp/nexus-guest-agent-linux \
  ./cmd/nexus-guest-agent

cp "$NEXUS_PKG/tmp/nexus-guest-agent-linux" "$EMBED_DIR/agent-linux-amd64"
chmod +x "$EMBED_DIR/agent-linux-amd64"

# ── libkrun-enabled build (preferred) ────────────────────────────────────────
# When nexus-libkrun-vm is committed in cmd/nexus/, stage the libkrun shared
# libraries from a cached smolvm release and build with -tags libkrun.
# This produces a fully self-contained binary that embeds the CGO helper and
# the shared libraries — no CGO compilation on the build machine required.
BUILD_TAGS=""
if [[ -f "$EMBED_DIR/nexus-libkrun-vm" ]]; then
  SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
  SMOLVM_CACHE="$NEXUS_PKG/tmp/${SMOLVM_TARBALL}"

  if [[ ! -f "$SMOLVM_CACHE" ]]; then
    SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
    echo "Downloading libkrun libs from smolvm ${SMOLVM_VERSION}..."
    mkdir -p "$NEXUS_PKG/tmp"
    curl -fsSL --progress-bar -o "$SMOLVM_CACHE" "$SMOLVM_URL"
  else
    echo "Using cached libkrun libs (smolvm ${SMOLVM_VERSION})..."
  fi

  # Extract real .so files (not symlinks) into the embed staging locations.
  LIBS_TMP="$NEXUS_PKG/tmp/smolvm-libs-${SMOLVM_VERSION}"
  if [[ ! -d "$LIBS_TMP" ]]; then
    mkdir -p "$LIBS_TMP"
    tar -xzf "$SMOLVM_CACHE" \
      --strip-components=2 \
      -C "$LIBS_TMP" \
      "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
  fi

  # Stage embed files (real files, not symlinks).
  cp "$LIBS_TMP/libkrun.so.1" "$EMBED_DIR/libkrun-embed.so"
  LIBKRUNFW_REAL=$(find "$LIBS_TMP" -maxdepth 1 -name 'libkrunfw.so.*.*' | sort | tail -1)
  cp "$LIBKRUNFW_REAL" "$EMBED_DIR/libkrunfw-embed.so"

  # Stage passt binary for embedding (we are building for linux/amd64, so download the x86_64 static build).
  PASST_EMBED="$EMBED_DIR/passt-embed"
  if [[ ! -f "$NEXUS_PKG/tmp/passt-embed" ]]; then
    echo "  Downloading passt for embed (x86_64 static build)..."
    curl -fsSL --retry 3 -o "$NEXUS_PKG/tmp/passt-embed" "https://passt.top/builds/latest/x86_64/passt"
    chmod +x "$NEXUS_PKG/tmp/passt-embed"
    echo "  → downloaded passt for embed ($(du -sh "$NEXUS_PKG/tmp/passt-embed" | cut -f1))"
  else
    echo "  Using cached passt for embed ($(du -sh "$NEXUS_PKG/tmp/passt-embed" | cut -f1))"
  fi
  cp "$NEXUS_PKG/tmp/passt-embed" "$PASST_EMBED"

  BUILD_TAGS="-tags libkrun,dev"
  echo "Building nexus for linux/amd64 with libkrun (commit=${GIT_COMMIT} built=${BUILD_TIME})..."
else
  BUILD_TAGS="-tags dev"
  echo "Building nexus for linux/amd64 (commit=${GIT_COMMIT} built=${BUILD_TIME})..."
  echo "  (nexus-libkrun-vm not found — building without libkrun embed)"
else
  echo "Building nexus for linux/amd64 (commit=${GIT_COMMIT} built=${BUILD_TIME})..."
  echo "  (nexus-libkrun-vm not found — building without libkrun embed)"
fi

# shellcheck disable=SC2086
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -C "$NEXUS_PKG" \
  $BUILD_TAGS \
  -ldflags "$LDFLAGS" \
  -o ./tmp/nexus-linux \
  ./cmd/nexus

# Clean up staged embed files so they don't clutter the working tree.
rm -f "$EMBED_DIR/libkrun-embed.so" "$EMBED_DIR/libkrunfw-embed.so" "$EMBED_DIR/passt-embed" "$EMBED_DIR/agent-linux-amd64"

# Keep the Mac app's embedded linux binary in sync so provision never
# re-uploads a stale version over a freshly deployed remote daemon.
if [ -d "$SWIFT_RESOURCES" ]; then
  cp "$NEXUS_PKG/tmp/nexus-linux" "$SWIFT_RESOURCES/nexus-linux-amd64"
  chmod +x "$SWIFT_RESOURCES/nexus-linux-amd64"
  echo "Staged  → packages/nexus-swift/Resources/nexus-linux-amd64 (kept in sync)"
fi

# The guest agent binary is embedded in nexus and extracted automatically by
# the daemon on first start (resolveGuestAgentBinary) — no separate deploy needed.
echo "Deploying to ${REMOTE_HOST}:${REMOTE_BIN} (dev env)..."
ssh "$REMOTE_HOST" "mkdir -p \$(dirname ${REMOTE_BIN}) && rm -f ${REMOTE_BIN}"
scp "$NEXUS_PKG/tmp/nexus-linux" "${REMOTE_HOST}:${REMOTE_BIN}"
ssh "$REMOTE_HOST" "chmod +x ${REMOTE_BIN}"

echo "Deployed successfully (commit=${GIT_COMMIT} built=${BUILD_TIME})."
