#!/usr/bin/env bash
# Build the Linux/arm64 guest ext4 for darwin libkrun VMs by:
#   1) assembling a minimal ubuntu 22.04 arm64 ext4 (guest agent only, no toolchain bake)
#   2) running `nexus vm bake` locally on the macOS/arm64 GitHub runner (Hypervisor.framework + gvproxy)
#   3) compressing the baked image to dist/rootfs-darwin-arm64.ext4.gz (+ .sha256)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_PKG="$REPO_ROOT/packages/nexus"
NEXUS_CMD="$NEXUS_PKG/cmd/nexus"
DIST="${GITHUB_WORKSPACE:-$REPO_ROOT}/dist"
mkdir -p "$DIST"

MKE2FS="$(command -v mke2fs || true)"
if [[ -z "$MKE2FS" ]]; then
  MKE2FS="$(ls /opt/homebrew/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || ls /usr/local/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || true)"
fi
if [[ -z "$MKE2FS" || ! -x "$MKE2FS" ]]; then
  echo "ERROR: mke2fs not found (brew install e2fsprogs)" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Darwin" ]] || [[ "$(uname -m)" != "arm64" ]]; then
  echo "ERROR: this script expects macOS/arm64 (Hypervisor.framework bake)" >&2
  exit 1
fi

if ! command -v brew >/dev/null 2>&1; then
  echo "ERROR: Homebrew not available" >&2
  exit 1
fi

brew install e2fsprogs >/dev/null

echo "==> Stage libkrun dylibs for nexus embed"
bash "$REPO_ROOT/scripts/local/build-libkrun-darwin.sh"

echo "==> Build embedded Linux guest-agent"
pushd "$NEXUS_CMD" >/dev/null
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o agent-linux-arm64 ../nexus-guest-agent/
popd >/dev/null

echo "==> Assemble minimal (un-baked) ext4 base"
curl -fsSL --retry 3 -o /tmp/ubuntu-arm64-root.tar.xz \
  "https://cloud-images.ubuntu.com/releases/jammy/release/ubuntu-22.04-server-cloudimg-arm64-root.tar.xz"

STAGING="/tmp/nexus-rootfs-staging-minimal.$$"
sudo rm -rf "$STAGING"
sudo mkdir -p "$STAGING"
sudo tar --no-same-owner -xJf /tmp/ubuntu-arm64-root.tar.xz -C "$STAGING"
sudo rm -rf "$STAGING/var/lib/snapd/void"

sudo mkdir -p "$STAGING/usr/local/bin"
sudo cp "$NEXUS_CMD/agent-linux-arm64" "$STAGING/usr/local/bin/nexus-guest-agent"
sudo chmod 0755 "$STAGING/usr/local/bin/nexus-guest-agent"
sudo mkdir -p "$STAGING/workspace"
if ! sudo grep -q '/dev/vda' "$STAGING/etc/fstab" 2>/dev/null; then
  echo "/dev/vda / ext4 rw,relatime 0 1" | sudo tee -a "$STAGING/etc/fstab" >/dev/null
fi
sudo chown -R "$(id -u):$(id -g)" "$STAGING"

BASE_EXT4="/tmp/nexus-rootfs-darwin-ci-base.ext4"
rm -f "$BASE_EXT4"
"$MKE2FS" -F -t ext4 -L nexus-root -d "$STAGING" "$BASE_EXT4" 10G
rm -rf "$STAGING"

echo "==> Prepare isolated XDG dirs for nexus vm bake"
BAKE_HOME="/tmp/nexus-ci-bake-home.$$"
rm -rf "$BAKE_HOME"
mkdir -p "$BAKE_HOME"
export HOME="$BAKE_HOME"
export XDG_CACHE_HOME="$BAKE_HOME/.cache"
export XDG_DATA_HOME="$BAKE_HOME/.local/share"
export XDG_STATE_HOME="$BAKE_HOME/.local/state"
mkdir -p "$XDG_CACHE_HOME/nexus/vm"
mkdir -p "$XDG_DATA_HOME/nexus/lib"
mkdir -p "$XDG_STATE_HOME/nexus"
cp -f "$BASE_EXT4" "$XDG_CACHE_HOME/nexus/vm/rootfs.ext4"
rm -f "$XDG_STATE_HOME/nexus"/rootfs-baked-* 2>/dev/null || true

echo "==> Build nexus (darwin/arm64) with embedded guest agent + libkrun"
pushd "$NEXUS_PKG" >/dev/null
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o "$REPO_ROOT/dist/nexus-darwin-arm64-bake" ./cmd/nexus
popd >/dev/null

echo "==> Run nexus vm bake (may take ~30–60m)"
export NEXUS_LIBKRUN_BAKE_TIMEOUT="${NEXUS_LIBKRUN_BAKE_TIMEOUT:-55m}"
"$REPO_ROOT/dist/nexus-darwin-arm64-bake" vm bake --timeout 120m

mkdir -p "$DIST"
gzip -9 -c "$XDG_CACHE_HOME/nexus/vm/rootfs.ext4" > "$DIST/rootfs-darwin-arm64.ext4.gz"
ls -lh "$DIST/rootfs-darwin-arm64.ext4.gz"
(
  cd "$DIST"
  shasum -a 256 rootfs-darwin-arm64.ext4.gz | tee rootfs-darwin-arm64.ext4.gz.sha256
)

rm -rf "$BAKE_HOME" "$BASE_EXT4" || true
rm -f "$REPO_ROOT/dist/nexus-darwin-arm64-bake" || true
