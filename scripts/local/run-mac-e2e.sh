#!/usr/bin/env bash
# Run macOS VM e2e tests locally.
#
# Prerequisites (run once):
#   brew install e2fsprogs
#   bash scripts/local/build-libkrun-darwin.sh
#   bash scripts/ci/ensure-macos-e2e-rootfs.sh
#
# Usage:
#   bash scripts/local/run-mac-e2e.sh                        # run all vm e2e suites
#   bash scripts/local/run-mac-e2e.sh -run TestLifecycle_StartAndStop
#   bash scripts/local/run-mac-e2e.sh -run TestSpotlight     # specific test
#   NEXUS_LIBKRUN_LOG=1 bash scripts/local/run-mac-e2e.sh    # verbose libkrun logs
#
# Environment overrides:
#   NEXUS_E2E_SUITES  space-separated list of test package dirs (relative to packages/nexus)
#                     default: all vm e2e suites
#   NEXUS_LIBKRUN_LOG set to any value to enable verbose libkrun debug output
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_PKG="$REPO_ROOT/packages/nexus"

# ── Prerequisites check ────────────────────────────────────────────────────────
MKE2FS=""
for p in mke2fs /opt/homebrew/opt/e2fsprogs/sbin/mke2fs /usr/local/opt/e2fsprogs/sbin/mke2fs; do
  if command -v "$p" &>/dev/null || [ -x "$p" ]; then
    MKE2FS="$p"
    break
  fi
done

LIBKRUN_DYLIB="$NEXUS_PKG/cmd/nexus/libkrun-darwin-arm64.dylib"
if [ ! -f "$LIBKRUN_DYLIB" ]; then
  echo "ERROR: libkrun dylibs not staged. Run: bash scripts/local/build-libkrun-darwin.sh" >&2
  exit 1
fi

ROOTFS="${NEXUS_VM_ROOTFS:-$HOME/.cache/nexus/vm/rootfs.ext4}"
if [ ! -f "$ROOTFS" ]; then
  echo "ERROR: rootfs not found at $ROOTFS." >&2
  echo "Run: bash scripts/ci/ensure-macos-e2e-rootfs.sh" >&2
  exit 1
fi

# ── Build nexus binary if stale ───────────────────────────────────────────────
NEXUS_BIN="${NEXUS_E2E_BINARY:-/tmp/nexus-bin}"
echo "==> Building nexus binary → $NEXUS_BIN"
cd "$NEXUS_PKG"
CGO_ENABLED=0 go build -o "$NEXUS_BIN" ./cmd/nexus/

echo "==> Building pty-host → /tmp/pty-host"
CGO_ENABLED=0 go build -o /tmp/pty-host ./cmd/pty-host/

echo "==> Codesigning $NEXUS_BIN (Hypervisor.framework entitlement)"
_ENT="$(mktemp /tmp/nexus-entitlements.XXXXXX.plist)"
cat > "$_ENT" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>com.apple.security.hypervisor</key>
    <true/>
    <key>com.apple.security.virtualization</key>
    <true/>
</dict>
</plist>
PLIST
codesign --entitlements "$_ENT" --force --sign - "$NEXUS_BIN"
rm -f "$_ENT"

# ── Default suites ────────────────────────────────────────────────────────────
DEFAULT_SUITES=(
  ./test/e2e/harness/...
  ./test/e2e/daemon/...
  ./test/e2e/auth/...
  ./test/e2e/fs/...
  ./test/e2e/project/...
  ./test/e2e/workspace/...
  ./test/e2e/pty/...
  ./test/e2e/spotlight/...
)

if [ -n "${NEXUS_E2E_SUITES:-}" ]; then
  IFS=' ' read -ra SUITES <<< "$NEXUS_E2E_SUITES"
else
  SUITES=("${DEFAULT_SUITES[@]}")
fi

# ── Run tests ─────────────────────────────────────────────────────────────────
echo ""
echo "==> Running macOS VM e2e tests"
echo "    rootfs:  $ROOTFS"
echo "    binary:  $NEXUS_BIN"
echo "    suites:  ${SUITES[*]}"
echo ""

export NEXUS_E2E_DRIVER=vm
export NEXUS_E2E_BINARY="$NEXUS_BIN"
export NEXUS_VM_ROOTFS="$ROOTFS"
export NEXUS_LIBKRUN_SKIP_BAKE=1
export NEXUS_LIBKRUN_MEM_MIB="${NEXUS_LIBKRUN_MEM_MIB:-512}"
export NEXUS_WORKSPACE_IMAGE_MIN_MIB="${NEXUS_WORKSPACE_IMAGE_MIN_MIB:-512}"
export NEXUS_RUNNER_ROOTFS_MIN_MIB="${NEXUS_RUNNER_ROOTFS_MIN_MIB:-512}"
export XDG_CACHE_HOME="${XDG_CACHE_HOME:-$HOME/.cache}"
# Add /tmp to PATH so daemon can find pty-host
export PATH="/tmp:$PATH"

cd "$NEXUS_PKG"
# shellcheck disable=SC2068
exec go test -tags e2e -count=1 -timeout=45m -v -p=1 -parallel=1 \
  "$@" \
  "${SUITES[@]}"
