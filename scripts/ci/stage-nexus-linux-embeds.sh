#!/usr/bin/env bash
# Stage libkrun + passt blobs for go:embed on linux/amd64 nexus builds.
# Safe to run multiple times (reuses /tmp smolvm cache). Skips on non-amd64 hosts.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_DIR="$REPO_ROOT/packages/nexus"
EMBED_DIR="$NEXUS_DIR/cmd/nexus"
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"
ARCH="${GOHOSTARCH:-$(go env GOARCH)}"

if [[ "$ARCH" != "amd64" ]]; then
  echo "stage-nexus-linux-embeds: host arch=$ARCH — no smolvm amd64 tarball; skipping"
  exit 0
fi

if [[ ! -d "$EMBED_DIR" ]]; then
  echo "stage-nexus-linux-embeds: missing $EMBED_DIR" >&2
  exit 1
fi

SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
LIBS_TMP="/tmp/smolvm-libs-${SMOLVM_VERSION}-embed"

if [[ ! -d "$LIBS_TMP" ]]; then
  curl -fsSL --retry 3 -o "/tmp/${SMOLVM_TARBALL}" "$SMOLVM_URL"
  mkdir -p "$LIBS_TMP"
  tar -xzf "/tmp/${SMOLVM_TARBALL}" --strip-components=2 -C "$LIBS_TMP" \
    "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
fi

cp "$LIBS_TMP/libkrun.so.1" "$EMBED_DIR/libkrun-embed.so"
LIBKRUNFW_REAL=$(find "$LIBS_TMP" -maxdepth 1 -name 'libkrunfw.so.*.*' | sort | tail -1)
cp "$LIBKRUNFW_REAL" "$EMBED_DIR/libkrunfw-embed.so"

PASST_EMBED="$EMBED_DIR/passt-embed"
if [[ -x "$(command -v passt)" ]]; then
  cp "$(command -v passt)" "$PASST_EMBED"
else
  curl -fsSL --retry 3 -o "$PASST_EMBED" "https://passt.top/builds/latest/x86_64/passt"
  chmod +x "$PASST_EMBED"
fi

echo "stage-nexus-linux-embeds: staged libkrun + passt under $EMBED_DIR"
