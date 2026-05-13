#!/usr/bin/env bash
# scripts/ci/mac-vm-smoke.sh
# Smoke-test macOS VM workspaces (libkrun): build rootfs, start daemon,
# create/start workspace, wait for running, PTY exec, stop + destroy.
#
# Prerequisites (macOS Apple Silicon host):
#   - Hypervisor.framework access (entitlement applied via codesign)
#   - brew install e2fsprogs  (installs mke2fs for rootfs creation)
#   - curl                    (download Ubuntu minimal cloud image)
#   - Go toolchain            (build nexus + guest-agent + pty-host)
#
# Environment:
#   NEXUS_REPO_ROOT     — git repo root (default: inferred from script location)
#   NEXUS_DAEMON_TOKEN  — auth token (default: test-mac-vm-smoke)
#   NEXUS_DAEMON_PORT   — WebSocket listener port (default: 7799)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="${NEXUS_REPO_ROOT:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
cd "$REPO_ROOT/packages/nexus"

TOKEN="${NEXUS_DAEMON_TOKEN:-test-mac-vm-smoke}"
PORT="${NEXUS_DAEMON_PORT:-7799}"
STATEDIR="${NEXUS_MAC_VM_STATE:-${TMPDIR:-/tmp}/nexus-mac-vm-smoke-state}"

export XDG_STATE_HOME="$STATEDIR/state"
export XDG_DATA_HOME="$STATEDIR/data"
export XDG_CACHE_HOME="$STATEDIR/cache"
export XDG_CONFIG_HOME="$STATEDIR/config"
mkdir -p "$XDG_STATE_HOME/nexus" "$XDG_DATA_HOME" "$XDG_CACHE_HOME/nexus/vm" "$XDG_CONFIG_HOME"

BINDIR="$(mktemp -d "${TMPDIR:-/tmp}/nexus-mac-vm-bin.XXXXXX")"
ROOTFSDIR="$(mktemp -d "${TMPDIR:-/tmp}/nexus-mac-vm-rootfs.XXXXXX")"

cleanup() {
  local nexus_bin="$BINDIR/nexus"
  if [[ -x "$nexus_bin" ]]; then
    NEXUS_DAEMON_TOKEN="$TOKEN" NEXUS_DAEMON_PORT="$PORT" \
      "$nexus_bin" daemon stop 2>/dev/null || true
  fi
  rm -rf "$BINDIR" "$ROOTFSDIR"
}
trap cleanup EXIT

SOCK="$XDG_STATE_HOME/nexus/nexusd.sock"
ROOTFS_CACHE="$XDG_CACHE_HOME/nexus/vm/rootfs.ext4"

# ---------------------------------------------------------------------------
# Step 1: Build binaries
# ---------------------------------------------------------------------------
echo "==> Building nexus + pty-host → $BINDIR"
go generate ./cmd/nexus/ 2>/dev/null || true
go build -o "$BINDIR/nexus" ./cmd/nexus
go build -o "$BINDIR/pty-host" ./cmd/pty-host

echo "==> Building nexus-guest-agent (linux/arm64)"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$BINDIR/nexus-guest-agent-linux-arm64" ./cmd/nexus-guest-agent

export PATH="$BINDIR:$PATH"
NEXUS_BIN="$BINDIR/nexus"

# ---------------------------------------------------------------------------
# Step 2: Sign nexus with Hypervisor.framework entitlement (ad-hoc)
# ---------------------------------------------------------------------------
echo "==> Signing nexus binary (Hypervisor.framework entitlement)"
ENTITLEMENTS_PLIST="$(mktemp /tmp/nexus-entitlements.XXXXXX.plist)"
cat > "$ENTITLEMENTS_PLIST" << 'PLIST'
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
codesign --entitlements "$ENTITLEMENTS_PLIST" --force --sign - "$NEXUS_BIN"
rm -f "$ENTITLEMENTS_PLIST"

# ---------------------------------------------------------------------------
# Step 3: Build macOS VM rootfs (Ubuntu 22.04 ARM64 minimal + guest-agent)
# ---------------------------------------------------------------------------
if [[ ! -f "$ROOTFS_CACHE" ]]; then
  echo "==> Building macOS VM rootfs"

  # Locate mke2fs from e2fsprogs (brew installs it to a non-standard path)
  MKE2FS="$(command -v mke2fs 2>/dev/null || \
    ls /opt/homebrew/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || \
    ls /usr/local/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || true)"
  if [[ -z "$MKE2FS" ]]; then
    echo "ERROR: mke2fs not found — install e2fsprogs: brew install e2fsprogs" >&2
    exit 1
  fi

  # Download Ubuntu 22.04 ARM64 minimal root filesystem
  UBUNTU_TAR="$ROOTFSDIR/ubuntu-arm64-root.tar.xz"
  UBUNTU_ROOT_URL="https://cloud-images.ubuntu.com/minimal/releases/jammy/release/ubuntu-22.04-minimal-cloudimg-arm64-root.tar.xz"
  echo "  Downloading Ubuntu 22.04 ARM64 minimal root..."
  curl -fsSL --retry 3 "$UBUNTU_ROOT_URL" -o "$UBUNTU_TAR"

  # Extract Ubuntu rootfs to a staging directory
  UBUNTU_STAGING="$ROOTFSDIR/ubuntu-staging"
  mkdir -p "$UBUNTU_STAGING"
  echo "  Extracting Ubuntu rootfs..."
  tar -xf "$UBUNTU_TAR" -C "$UBUNTU_STAGING" 2>/dev/null || \
    tar -xJf "$UBUNTU_TAR" -C "$UBUNTU_STAGING"

  # Install the nexus-guest-agent into the rootfs
  mkdir -p "$UBUNTU_STAGING/usr/local/bin"
  cp "$BINDIR/nexus-guest-agent-linux-arm64" "$UBUNTU_STAGING/usr/local/bin/nexus-guest-agent"
  chmod 0755 "$UBUNTU_STAGING/usr/local/bin/nexus-guest-agent"

  # Ensure workspace mount point exists
  mkdir -p "$UBUNTU_STAGING/workspace"

  # Create ext4 rootfs using mke2fs -d (no mounting required)
  echo "  Creating ext4 image (10G sparse) via mke2fs -d..."
  "$MKE2FS" -F -t ext4 -L nexus-root -d "$UBUNTU_STAGING" "$ROOTFS_CACHE" 10G
  echo "  Rootfs: $(ls -lah "$ROOTFS_CACHE" | awk '{print $5}')"
else
  echo "==> Rootfs cache hit: $ROOTFS_CACHE"
fi

# ---------------------------------------------------------------------------
# Step 4: Start daemon
# ---------------------------------------------------------------------------
echo "==> Stopping any prior daemon on port $PORT"
"$NEXUS_BIN" daemon stop 2>/dev/null || true
sleep 1

echo "==> Starting daemon (VM driver, WebSocket on 127.0.0.1:$PORT)"
NEXUS_DAEMON_TOKEN="$TOKEN" \
  "$NEXUS_BIN" daemon start \
    --port "$PORT" \
    --network=true \
    --foreground=false \
    --driver vm \
    --token "$TOKEN" \
    --socket "$SOCK"

echo "==> Waiting for WebSocket listener on port $PORT"
for i in $(seq 1 60); do
  if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then
    echo "  listener up after ${i}s"
    break
  fi
  sleep 1
done
if ! nc -z 127.0.0.1 "$PORT" 2>/dev/null; then
  echo "ERROR: daemon did not open port $PORT" >&2
  exit 1
fi

export NEXUS_DAEMON_TOKEN="$TOKEN"
export NEXUS_DAEMON_PORT="$PORT"

# ---------------------------------------------------------------------------
# Step 5: Create and start workspace
# ---------------------------------------------------------------------------
echo "==> workspace create"
CREATE_OUT="$("$NEXUS_BIN" workspace create --name mac-vm-ci --repo "$REPO_ROOT" 2>&1)"
echo "  $CREATE_OUT"
WS_ID="$(echo "$CREATE_OUT" | sed -n 's/.*(id: \([^)]*\)).*/\1/p')"
if [[ -z "$WS_ID" ]]; then
  echo "ERROR: could not parse workspace id from: $CREATE_OUT" >&2
  exit 1
fi

echo "==> workspace start $WS_ID"
"$NEXUS_BIN" workspace start "$WS_ID"

# ---------------------------------------------------------------------------
# Step 6: Wait for workspace to reach running state
# ---------------------------------------------------------------------------
echo "==> Waiting for workspace ready (up to 4 minutes)"
READY_OK=false
for i in $(seq 1 120); do
  STATE="$("$NEXUS_BIN" workspace info "$WS_ID" 2>/dev/null | awk '/^state:/{print $2}')"
  if [[ "$STATE" == "running" ]]; then
    echo "  workspace running after ~$((i * 2))s"
    READY_OK=true
    break
  elif [[ "$STATE" == "failed" ]]; then
    echo "ERROR: workspace entered failed state" >&2
    "$NEXUS_BIN" workspace info "$WS_ID" || true
    exit 1
  fi
  sleep 2
done

if [[ "$READY_OK" != true ]]; then
  echo "ERROR: workspace did not reach running state in time" >&2
  "$NEXUS_BIN" workspace info "$WS_ID" || true
  exit 1
fi

"$NEXUS_BIN" workspace info "$WS_ID"

# ---------------------------------------------------------------------------
# Step 7: PTY exec test
# ---------------------------------------------------------------------------
echo "==> PTY exec test"
RESULT="$("$NEXUS_BIN" workspace exec "$WS_ID" -- echo hello-ci-mac-vm 2>&1)"
echo "  result: $RESULT"
if [[ "$RESULT" != "hello-ci-mac-vm" ]]; then
  echo "ERROR: unexpected exec result: $RESULT" >&2
  exit 1
fi
echo "  PTY exec OK"

# ---------------------------------------------------------------------------
# Step 8: Cleanup
# ---------------------------------------------------------------------------
echo "==> stop + destroy"
"$NEXUS_BIN" workspace stop "$WS_ID" 2>/dev/null || true
"$NEXUS_BIN" workspace remove "$WS_ID" 2>/dev/null || true

echo ""
echo "OK mac-vm-smoke  workspace=$WS_ID"
