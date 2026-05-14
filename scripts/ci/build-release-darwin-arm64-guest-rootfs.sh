#!/usr/bin/env bash
# Build compressed Linux/arm64 guest rootfs for macOS libkrun VMs (Apple Silicon).
# Run on Linux CI (ubuntu-latest): native mke2fs reliably populates ext4 from the Ubuntu cloud tarball.
# macOS runners hit e2fsprogs populate_fs permission errors on the same tarball.
# Writes dist/rootfs-darwin-arm64.ext4.gz and dist/rootfs-darwin-arm64.ext4.gz.sha256
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_DIR="$REPO_ROOT/packages/nexus"

MKE2FS="$(command -v mke2fs || true)"
if [[ -z "$MKE2FS" ]]; then
  MKE2FS="$(ls /opt/homebrew/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || ls /usr/local/opt/e2fsprogs/sbin/mke2fs 2>/dev/null || true)"
fi
if [[ -z "$MKE2FS" || ! -x "$MKE2FS" ]]; then
  echo "ERROR: mke2fs not found (apt install e2fsprogs or brew install e2fsprogs)" >&2
  exit 1
fi

pushd "$NEXUS_DIR" >/dev/null
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o /tmp/nexus-guest-agent ./cmd/nexus-guest-agent/
popd >/dev/null

curl -fsSL --retry 3 -o /tmp/ubuntu-arm64-root.tar.xz \
  "https://cloud-images.ubuntu.com/releases/jammy/release/ubuntu-22.04-server-cloudimg-arm64-root.tar.xz"

STAGING="/tmp/rootfs-staging-darwin-rel"
rm -rf "$STAGING"
mkdir -p "$STAGING"
sudo tar --no-same-owner -xJf /tmp/ubuntu-arm64-root.tar.xz -C "$STAGING"
sudo chown -R "$(id -u):$(id -g)" "$STAGING"
mkdir -p "$STAGING/usr/local/bin"
cp /tmp/nexus-guest-agent "$STAGING/usr/local/bin/nexus-guest-agent"
chmod 0755 "$STAGING/usr/local/bin/nexus-guest-agent"
mkdir -p "$STAGING/workspace"
if ! grep -q '/dev/vda' "$STAGING/etc/fstab" 2>/dev/null; then
  echo "/dev/vda / ext4 rw,relatime 0 1" | tee -a "$STAGING/etc/fstab"
fi

"$MKE2FS" -F -t ext4 -L nexus-root -d "$STAGING" /tmp/rootfs-darwin-arm64.ext4 10G

OUT="${GITHUB_WORKSPACE:-$REPO_ROOT}/dist"
mkdir -p "$OUT"
gzip -9 -c /tmp/rootfs-darwin-arm64.ext4 > "$OUT/rootfs-darwin-arm64.ext4.gz"
ls -lh "$OUT/rootfs-darwin-arm64.ext4.gz"
(
  cd "$OUT"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum rootfs-darwin-arm64.ext4.gz | tee rootfs-darwin-arm64.ext4.gz.sha256
  else
    shasum -a 256 rootfs-darwin-arm64.ext4.gz | tee rootfs-darwin-arm64.ext4.gz.sha256
  fi
)
