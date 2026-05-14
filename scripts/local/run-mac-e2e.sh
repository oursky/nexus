#!/usr/bin/env bash
# Run macOS VM e2e tests locally with the same env as CI (e2e-macos-vm) and
# always clean up VM processes + workspace caches on EXIT.
#
# Prerequisites (run once):
#   brew install e2fsprogs
#   bash scripts/local/build-libkrun-darwin.sh
#   bash scripts/ci/ensure-macos-e2e-rootfs.sh
#
# Usage:
#   bash scripts/local/run-mac-e2e.sh
#   bash scripts/local/run-mac-e2e.sh -run TestSpotlight
#
# Environment overrides (optional):
#   NEXUS_E2E_GO_TIMEOUT   — default 45m (longer than CI for slow laptops)
#   NEXUS_E2E_GO_P         — default 2 (same as CI)
#   NEXUS_E2E_GO_PARALLEL  — default 2
#   NEXUS_E2E_MACOS_PACKAGES — space-separated packages (default: full CI list)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_PKG="$REPO_ROOT/packages/nexus"

disk_report() {
  local phase="$1"
  echo ""
  echo "=== disk ($phase) ==="
  df -h / 2>/dev/null || true
  df -h "${HOME}" 2>/dev/null || true
  echo ""
}

cleanup() {
  echo ""
  echo "==> run-mac-e2e cleanup (EXIT trap)"
  pkill -TERM -f '[m]acvm-runner' 2>/dev/null || true
  pkill -TERM -f '[g]vproxy' 2>/dev/null || true
  sleep 1
  pkill -KILL -f '[m]acvm-runner' 2>/dev/null || true
  pkill -KILL -f '[g]vproxy' 2>/dev/null || true

  shopt -s nullglob
  rm -rf /tmp/nexus-* 2>/dev/null || true
  shopt -u nullglob

  local ws="${XDG_CACHE_HOME:-$HOME/.cache}/nexus/macvm-workspaces"
  if [[ -d "$ws" ]]; then
    rm -rf "${ws:?}"/* 2>/dev/null || true
  fi

  # Best-effort: temp dirs under macOS/var (short run; cap results).
  find /var/folders -type d -name 'nexus-*' 2>/dev/null | head -200 | while read -r d; do
    rm -rf "$d" 2>/dev/null || true
  done

  disk_report "after cleanup"
}

trap cleanup EXIT

disk_report "before"

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
go generate ./cmd/nexus/
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

# gvproxy: same release as internal/vm/net/gvproxy.go (FindGVProxy checks PATH first).
GVPROXY_VERSION="${GVPROXY_VERSION:-0.8.8}"
GVPROXY_URL="https://github.com/containers/gvisor-tap-vsock/releases/download/v${GVPROXY_VERSION}/gvproxy-darwin"
echo "==> Installing gvproxy → /tmp/gvproxy"
if [[ ! -x /tmp/gvproxy ]]; then
  curl -fsSL --retry 3 -L "$GVPROXY_URL" -o /tmp/gvproxy
  chmod +x /tmp/gvproxy
fi
/tmp/gvproxy --help >/dev/null

# ── Packages (match CI e2e-macos-vm when unset) ───────────────────────────────
if [[ -n "${NEXUS_E2E_MACOS_PACKAGES:-}" ]]; then
  read -ra SUITES <<< "$NEXUS_E2E_MACOS_PACKAGES"
else
  SUITES=(
    ./test/e2e/harness/...
    ./test/e2e/daemon/...
    ./test/e2e/auth/...
    ./test/e2e/fs/...
    ./test/e2e/project/...
    ./test/e2e/vmproof/...
    ./test/e2e/workspace/...
    ./test/e2e/cli/...
    ./test/e2e/pty/...
    ./test/e2e/spotlight/...
  )
fi

# ── Env (mirror .github/workflows/ci.yml e2e-macos-vm) ───────────────────────
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
export CI="${CI:-true}"
export NEXUS_E2E_DRIVER=vm
export NEXUS_E2E_BINARY="$NEXUS_BIN"
export NEXUS_VM_ROOTFS="$ROOTFS"
export NEXUS_LIBKRUN_SKIP_BAKE="${NEXUS_LIBKRUN_SKIP_BAKE:-1}"
export NEXUS_LIBKRUN_MEM_MIB="${NEXUS_LIBKRUN_MEM_MIB:-512}"
export NEXUS_WORKSPACE_IMAGE_MIN_MIB="${NEXUS_WORKSPACE_IMAGE_MIN_MIB:-512}"
export NEXUS_RUNNER_ROOTFS_MIN_MIB="${NEXUS_RUNNER_ROOTFS_MIN_MIB:-512}"
export NEXUS_READINESS_TIMEOUT_SECONDS="${NEXUS_READINESS_TIMEOUT_SECONDS:-90}"
export XDG_CACHE_HOME="${XDG_CACHE_HOME:-$HOME/.cache}"
export PATH="/tmp:$PATH"

echo ""
echo "==> Running macOS VM e2e"
echo "    rootfs:  $ROOTFS"
echo "    binary:  $NEXUS_BIN"
echo "    suites:  ${SUITES[*]}"
echo ""

cd "$NEXUS_PKG"
go test -tags e2e -count=1 \
  -timeout="${NEXUS_E2E_GO_TIMEOUT:-45m}" -v \
  -p "${NEXUS_E2E_GO_P:-2}" -parallel "${NEXUS_E2E_GO_PARALLEL:-2}" \
  "$@" \
  "${SUITES[@]}"
