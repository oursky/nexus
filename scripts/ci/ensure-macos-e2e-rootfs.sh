#!/usr/bin/env bash
# Populate ~/.cache/nexus/vm/rootfs.ext4 (or NEXUS_VM_ROOTFS) for macOS E2E VM tests.
# Same layout as scripts/ci/mac-vm-smoke.sh rootfs build (Ubuntu minimal arm64 + guest-agent).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT/packages/nexus"

CACHE="${NEXUS_VM_ROOTFS:-${NEXUS_E2E_ROOTFS:-$HOME/.cache/nexus/vm/rootfs.ext4}}"
mkdir -p "$(dirname "$CACHE")"

if [[ -f "$CACHE" ]]; then
  echo "rootfs cache hit: $CACHE"
  exit 0
fi

echo "==> Building macOS E2E guest rootfs → $CACHE"

MKE2FS="$(command -v mke2fs 2>/dev/null || \
  ls /opt/homebrew/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || \
  ls /usr/local/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || true)"
if [[ -z "$MKE2FS" ]]; then
  echo "ERROR: mke2fs not found — brew install e2fsprogs" >&2
  exit 1
fi

ROOTFSDIR="$(mktemp -d "${TMPDIR:-/tmp}/nexus-mac-e2e-rootfs.XXXXXX")"
trap 'rm -rf "$ROOTFSDIR"' EXIT

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ROOTFSDIR/nexus-guest-agent" ./cmd/nexus-guest-agent

UBUNTU_TAR="$ROOTFSDIR/ubuntu-arm64-root.tar.xz"
# Jammy minimal no longer ships arm64 *-root.tar.xz on the release tree; Noble does.
UBUNTU_ROOT_URL="https://cloud-images.ubuntu.com/minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-arm64-root.tar.xz"
curl -fsSL --retry 3 "$UBUNTU_ROOT_URL" -o "$UBUNTU_TAR"

UBUNTU_STAGING="$ROOTFSDIR/ubuntu-staging"
mkdir -p "$UBUNTU_STAGING"
# macOS tar cannot create Linux device nodes under dev/ (hits "Can't create" and exits 1).
# Guest kernels use devtmpfs for /dev; an empty dev directory is enough for mke2fs -d staging.
# -o: do not restore root ownership so CI can rm -rf the temp dir.
tar -xJf "$UBUNTU_TAR" -C "$UBUNTU_STAGING" -o --exclude='dev/*'

mkdir -p "$UBUNTU_STAGING/usr/local/bin"
cp "$ROOTFSDIR/nexus-guest-agent" "$UBUNTU_STAGING/usr/local/bin/nexus-guest-agent"
chmod 0755 "$UBUNTU_STAGING/usr/local/bin/nexus-guest-agent"
mkdir -p "$UBUNTU_STAGING/workspace"

"$MKE2FS" -F -t ext4 -L nexus-root -d "$UBUNTU_STAGING" "$CACHE" 10G
echo "rootfs: $(ls -lah "$CACHE")"
