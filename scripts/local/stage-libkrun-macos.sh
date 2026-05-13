#!/usr/bin/env bash
set -euo pipefail
# stage-libkrun-macos.sh: copies libkrun dylibs and kernel into Swift Resources
# so the Mac app can embed them for the local VM daemon.

find_libkrun_dir() {
  if command -v brew >/dev/null 2>&1; then
    local pfx
    pfx="$(brew --prefix libkrun 2>/dev/null || true)"
    if [[ -n "$pfx" ]] && [[ -f "$pfx/lib/libkrun.dylib" ]]; then
      echo "$pfx/lib"
      return 0
    fi
  fi
  for base in /opt/homebrew /usr/local; do
    if [[ -f "$base/opt/libkrun/lib/libkrun.dylib" ]]; then
      echo "$base/opt/libkrun/lib"
      return 0
    fi
  done
  local f
  f="$(find /opt/homebrew /usr/local -name libkrun.dylib 2>/dev/null | head -n 1)"
  if [[ -n "$f" ]] && [[ -f "$f" ]]; then
    echo "$(dirname "$f")"
    return 0
  fi

  echo "" >&2
  echo "libkrun.dylib not found. Install with: brew install libkrun" >&2
  return 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SRC_LIB="$(find_libkrun_dir)"
DEST_DIR="$REPO_ROOT/packages/nexus-swift/Resources"

for d in "$SRC_LIB/libkrun.dylib" "$SRC_LIB/libkrunfw.dylib"; do
  if [[ ! -f "$d" ]]; then
    echo "missing: $d" >&2
    exit 1
  fi
done

mkdir -p "$DEST_DIR"

cp -f "$SRC_LIB/libkrun.dylib" "$DEST_DIR/libkrun.dylib"
cp -f "$SRC_LIB/libkrunfw.dylib" "$DEST_DIR/libkrunfw.dylib"

KERNEL_SRC="$REPO_ROOT/packages/nexus/cmd/nexus/assets/Image"
if [[ -f "$KERNEL_SRC" ]]; then
  cp -f "$KERNEL_SRC" "$DEST_DIR/nexus-vm-kernel"
else
  echo "Optional kernel skipped (not present at $KERNEL_SRC)"
fi

summarize() {
  local label="$1" path="$2"
  if [[ -f "$path" ]]; then
    local sz
    sz="$(wc -c <"$path" | tr -d ' ')"
    printf '  %s → %s  (%s bytes)\n' "$label" "$path" "$sz"
  fi
}

echo "Staging complete → $DEST_DIR"
summarize "libkrun" "$DEST_DIR/libkrun.dylib"
summarize "libkrunfw" "$DEST_DIR/libkrunfw.dylib"
if [[ -f "$DEST_DIR/nexus-vm-kernel" ]]; then
  summarize "kernel" "$DEST_DIR/nexus-vm-kernel"
fi
echo "Success: libkrun assets staged for Nexus Swift."
