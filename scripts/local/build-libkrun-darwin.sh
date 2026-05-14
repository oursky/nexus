#!/usr/bin/env bash
set -euo pipefail
# Downloads the smolvm darwin-arm64 release tarball and extracts
# libkrun.dylib + libkrunfw.dylib into packages/nexus/cmd/nexus/
# as embed artifacts for the macOS nexus binary.
#
# Version must match internal/domain/bundle/runtime_payload.go smolvmRuntimeVersion.

SMOLVM_VERSION="v0.5.20"
SMOLVM_TARBALL_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/smolvm-0.5.20-darwin-arm64.tar.gz"
SMOLVM_SHA256="92d687486852f78ea5ddf12be88c879ae9b8d8fc2bd7159de6586df0cb71d3e1"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DEST_DIR="$REPO_ROOT/packages/nexus/cmd/nexus"

mkdir -p "$DEST_DIR"

TARBALL="$DEST_DIR/libkrun-darwin-arm64.tar.gz"

echo "Downloading smolvm ${SMOLVM_VERSION} (${SMOLVM_TARBALL_URL})..."
curl -fsSL "${SMOLVM_TARBALL_URL}" -o "$TARBALL"

echo "${SMOLVM_SHA256}  ${TARBALL}" | shasum -a 256 -c -

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

tar -xzf "$TARBALL" -C "$tmp"

LIB_SRC=""
while IFS= read -r -d '' f; do
  LIB_SRC="$(dirname "$f")"
  break
done < <(find "$tmp" -name libkrun.dylib -print0 || true)

if [[ -z "$LIB_SRC" ]]; then
  echo "libkrun.dylib not found inside tarball — unexpected layout." >&2
  exit 1
fi

if [[ ! -f "$LIB_SRC/libkrunfw.dylib" ]]; then
  echo "libkrunfw.dylib missing next to libkrun in $LIB_SRC" >&2
  exit 1
fi

DEST_LIB="$DEST_DIR/libkrun-darwin-arm64.dylib"
DEST_FW="$DEST_DIR/libkrunfw-darwin-arm64.dylib"

cp -f "$LIB_SRC/libkrun.dylib" "$DEST_LIB"
cp -f "$LIB_SRC/libkrunfw.dylib" "$DEST_FW"

summarize() {
  local label="$1" path="$2"
  local sz
  sz="$(wc -c <"$path" | tr -d ' ')"
  printf '%s → %s  (%s bytes)\n' "$label" "$path" "$sz"
}

echo "Staged embedded dylibs:"
summarize "libkrun" "$DEST_LIB"
summarize "libkrunfw" "$DEST_FW"
