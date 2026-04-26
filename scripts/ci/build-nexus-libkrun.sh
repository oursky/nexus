#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_DIR="$REPO_ROOT/packages/nexus"
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"

pushd "$NEXUS_DIR" >/dev/null
go generate ./cmd/nexus/

EMBED_DIR="$NEXUS_DIR/cmd/nexus"
if [[ -f "$EMBED_DIR/nexus-libkrun-vm" ]]; then
  SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
  SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
  LIBS_TMP="/tmp/smolvm-libs-${SMOLVM_VERSION}"

  if [[ ! -d "$LIBS_TMP" ]]; then
    curl -fsSL --retry 3 -o "/tmp/${SMOLVM_TARBALL}" "$SMOLVM_URL"
    mkdir -p "$LIBS_TMP"
    tar -xzf "/tmp/${SMOLVM_TARBALL}" --strip-components=2 -C "$LIBS_TMP" \
      "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
  fi

  cp "$LIBS_TMP/libkrun.so.1" "$EMBED_DIR/libkrun-embed.so"
  LIBKRUNFW_REAL=$(find "$LIBS_TMP" -maxdepth 1 -name 'libkrunfw.so.*.*' | sort | tail -1)
  cp "$LIBKRUNFW_REAL" "$EMBED_DIR/libkrunfw-embed.so"
  BUILD_TAGS="-tags libkrun"
  echo "Building nexus with libkrun embed..."
else
  BUILD_TAGS=""
  echo "nexus-libkrun-vm not found — building without libkrun embed"
fi

# shellcheck disable=SC2086
CGO_ENABLED=0 go build $BUILD_TAGS -o /tmp/nexus-bin ./cmd/nexus/
rm -f "$EMBED_DIR/libkrun-embed.so" "$EMBED_DIR/libkrunfw-embed.so"

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "NEXUS_E2E_BINARY=/tmp/nexus-bin" >> "$GITHUB_ENV"
fi
popd >/dev/null
