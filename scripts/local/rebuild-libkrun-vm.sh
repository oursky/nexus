#!/usr/bin/env bash
set -euo pipefail
#
# rebuild-libkrun-vm.sh — Rebuild nexus-libkrun-vm on a Linux host when local
# source has changed. This is for macOS contributors who cannot build the CGO
# Linux binary locally.
#
# Usage:
#   REMOTE_HOST=user@linux-host ./scripts/local/rebuild-libkrun-vm.sh
#
# The script compares mtimes of cmd/nexus-libkrun-vm/*.go against the committed
# cmd/nexus/nexus-libkrun-vm binary. If source is newer, it SSHes into the
# remote Linux host, builds there, and pulls the binary back.
#

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_PKG="$REPO_ROOT/packages/nexus"
SRC_DIR="$NEXUS_PKG/cmd/nexus-libkrun-vm"
BINARY="$NEXUS_PKG/cmd/nexus/nexus-libkrun-vm"

if [[ -z "${REMOTE_HOST:-}" ]]; then
  echo "ERROR: REMOTE_HOST is not set. Pass REMOTE_HOST=user@host" >&2
  exit 1
fi

if [[ ! -d "$SRC_DIR" ]]; then
  echo "ERROR: source dir not found: $SRC_DIR" >&2
  exit 1
fi

# Find newest source file mtime.
NEWEST_SRC_MTIME=$(find "$SRC_DIR" -name '*.go' -printf '%T@\n' 2>/dev/null | sort -n | tail -1 || true)
if [[ -z "$NEWEST_SRC_MTIME" ]]; then
  # macOS fallback (no -printf)
  NEWEST_SRC_MTIME=$(find "$SRC_DIR" -name '*.go' -exec stat -f '%m' {} + 2>/dev/null | sort -n | tail -1 || true)
fi

BINARY_MTIME=""
if [[ -f "$BINARY" ]]; then
  BINARY_MTIME=$(stat -f '%m' "$BINARY" 2>/dev/null || stat -c '%Y' "$BINARY" 2>/dev/null || true)
fi

if [[ -n "$BINARY_MTIME" && -n "$NEWEST_SRC_MTIME" ]]; then
  # Normalize: stat -f '%m' returns integer seconds; printf returns float.
  NEWEST_INT="${NEWEST_SRC_MTIME%%.*}"
  if [[ "$BINARY_MTIME" -ge "$NEWEST_INT" ]]; then
    echo "nexus-libkrun-vm is up to date (binary newer than source)"
    exit 0
  fi
fi

echo "nexus-libkrun-vm source is newer than binary — rebuilding on $REMOTE_HOST ..."

# Build on remote Linux host.
ssh "$REMOTE_HOST" bash -s <<'REMOTE'
  set -euo pipefail
  WORKDIR="$HOME/magic/nexus"
  if [[ ! -d "$WORKDIR" ]]; then
    echo "ERROR: $WORKDIR does not exist on remote host" >&2
    exit 1
  fi
  cd "$WORKDIR/packages/nexus"
  SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"
  SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
  SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
  LIBS_TMP="/tmp/smolvm-libs-${SMOLVM_VERSION}"

  if [[ ! -d "$LIBS_TMP" ]]; then
    curl -fsSL --retry 3 -o "/tmp/${SMOLVM_TARBALL}" "$SMOLVM_URL"
    mkdir -p "$LIBS_TMP"
    tar -xzf "/tmp/${SMOLVM_TARBALL}" --strip-components=2 -C "$LIBS_TMP" \
      "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
  fi

  # Download header from libkrun source repo (not shipped in smolvm tarball).
  LIBKRUN_INC="/tmp/libkrun-include-${SMOLVM_VERSION}"
  mkdir -p "$LIBKRUN_INC"
  if [[ ! -f "$LIBKRUN_INC/libkrun.h" ]]; then
    curl -fsSL --retry 3 -o "$LIBKRUN_INC/libkrun.h" \
      "https://raw.githubusercontent.com/containers/libkrun/main/include/libkrun.h"
  fi

  CGO_ENABLED=1 \
    CGO_CFLAGS="-I$LIBKRUN_INC" \
    CGO_LDFLAGS="-L$LIBS_TMP -lkrun -Wl,-rpath,\$ORIGIN/../lib" \
    go build -tags libkrunvm \
      -o cmd/nexus/nexus-libkrun-vm \
      ./cmd/nexus-libkrun-vm
  echo "  → rebuilt on remote"
REMOTE

# Pull the binary back.
scp "$REMOTE_HOST:~/magic/nexus/packages/nexus/cmd/nexus/nexus-libkrun-vm" "$BINARY"
echo "  → pulled to $BINARY ($(du -sh "$BINARY" | cut -f1))"
echo "  → stage with: git add packages/nexus/cmd/nexus/nexus-libkrun-vm"
