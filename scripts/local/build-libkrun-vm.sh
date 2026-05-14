#!/usr/bin/env bash
set -euo pipefail
#
# build-libkrun-vm.sh — Build nexus-libkrun-vm on a local Linux host.
#
# Builds the CGO binary from source, places it in the embed location
# (packages/nexus/cmd/nexus/nexus-libkrun-vm) and, when LOCAL_SHARE is set,
# also copies it directly into the running daemon's bin directory so the next
# workspace launch picks it up without a full nexus-dev rebuild.
#
# Usage (called by Taskfile):
#   LOCAL_SHARE=~/.local/share-dev/nexus ./scripts/local/build-libkrun-vm.sh
#
# After this script, run `task dev:local` to re-embed the binary into
# nexus-dev and restart the daemon.
#

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_DIR="$REPO_ROOT/packages/nexus"
EMBED_DIR="$NEXUS_DIR/cmd/nexus"
SRC_DIR="$NEXUS_DIR/cmd/nexus-libkrun-vm"
BINARY="$EMBED_DIR/nexus-libkrun-vm"

SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"
SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
LIBS_TMP="/tmp/smolvm-libs-${SMOLVM_VERSION}"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "ERROR: nexus-libkrun-vm must be built on Linux (CGO targeting Linux libkrun.so)" >&2
  echo "       On macOS, use scripts/local/rebuild-libkrun-vm.sh with REMOTE_HOST set." >&2
  exit 1
fi

if [[ ! -d "$SRC_DIR" ]]; then
  echo "ERROR: source directory not found: $SRC_DIR" >&2
  exit 1
fi

echo "==> Ensuring smolvm libs (${SMOLVM_VERSION})..."
if [[ ! -d "$LIBS_TMP" ]]; then
  curl -fsSL --retry 3 -o "/tmp/${SMOLVM_TARBALL}" "$SMOLVM_URL"
  mkdir -p "$LIBS_TMP"
  tar -xzf "/tmp/${SMOLVM_TARBALL}" --strip-components=2 -C "$LIBS_TMP" \
    "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
  echo "  → unpacked to $LIBS_TMP"
else
  echo "  → cached at $LIBS_TMP"
fi

echo "==> Building nexus-libkrun-vm..."
cd "$NEXUS_DIR"
cp -f "$LIBS_TMP/libkrun.so.1" "$EMBED_DIR/libkrun-embed.so"
CGO_ENABLED=1 \
  go build \
    -o "$BINARY" \
    ./cmd/nexus-libkrun-vm
echo "  → built: $BINARY ($(du -sh "$BINARY" | cut -f1))"

# If LOCAL_SHARE is set, also copy directly into the daemon's runtime bin dir.
# This lets the current daemon use the new binary for the next workspace start
# without waiting for a full nexus-dev rebuild cycle.
LOCAL_SHARE="${LOCAL_SHARE:-}"
if [[ -n "$LOCAL_SHARE" ]]; then
  RUNTIME_BIN="$(eval echo "$LOCAL_SHARE")/bin"
  if [[ -d "$RUNTIME_BIN" ]]; then
    # Atomic replace: copy to a temp file then rename so we don't hit
    # ETXTBSY if the daemon is currently holding the old binary open.
    TMP_BIN="$RUNTIME_BIN/nexus-libkrun-vm.tmp.$$"
    cp "$BINARY" "$TMP_BIN"
    mv -f "$TMP_BIN" "$RUNTIME_BIN/nexus-libkrun-vm"
    echo "  → deployed to $RUNTIME_BIN/nexus-libkrun-vm"
  else
    echo "  (skipped runtime deploy: $RUNTIME_BIN not found)"
  fi
fi

echo ""
echo "nexus-libkrun-vm built. Next steps:"
echo "  task dev:local   — re-embed into nexus-dev, install, restart daemon"
echo "  git add packages/nexus/cmd/nexus/nexus-libkrun-vm"
