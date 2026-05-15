#!/usr/bin/env bash
# Build compressed Linux/arm64 guest rootfs for macOS libkrun VMs (Apple Silicon).
# Run on Linux CI (ubuntu-latest): native mke2fs reliably populates ext4 from the Ubuntu cloud tarball.
# macOS runners hit e2fsprogs populate_fs permission errors on the same tarball.
# Writes dist/rootfs-darwin-arm64.ext4.gz and dist/rootfs-darwin-arm64.ext4.gz.sha256
#
# Pre-bakes the same toolchain as packages/nexus/cmd/nexus-guest-agent/sysconfig.go
# (ensureGuestBasePackages + ensureGuestCLITools): apt packages, mise + node@20, npx wrappers,
# and /var/lib/nexus-tools-base-v15. Docker image pre-pull from bake targets ephemeral
# /workspace and is not reproduced here.
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
sudo rm -rf "$STAGING"
sudo mkdir -p "$STAGING"
sudo tar --no-same-owner -xJf /tmp/ubuntu-arm64-root.tar.xz -C "$STAGING"
sudo rm -rf "$STAGING/var/lib/snapd/void"

# --- Pre-bake guest toolchain (same as guest-agent bake path, via chroot + qemu-user-static) ---
if [[ "$(uname -s)" != Linux ]]; then
  echo "ERROR: chroot bake requires Linux (CI ubuntu-latest)" >&2
  exit 1
fi

sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq qemu-user-static

QEMU_GUEST="/usr/bin/qemu-aarch64-static"
if [[ ! -x "$QEMU_GUEST" ]]; then
  echo "ERROR: $QEMU_GUEST not found after installing qemu-user-static" >&2
  exit 1
fi
sudo cp "$QEMU_GUEST" "$STAGING/usr/bin/qemu-aarch64-static"

mount_staging_for_chroot() {
  sudo mount --bind /dev "$STAGING/dev"
  sudo mount --bind /proc "$STAGING/proc"
  sudo mount --bind /sys "$STAGING/sys"
}

unmount_staging_for_chroot() {
  sudo umount "$STAGING/sys" 2>/dev/null || true
  sudo umount "$STAGING/proc" 2>/dev/null || true
  sudo umount "$STAGING/dev" 2>/dev/null || true
}

mount_staging_for_chroot
trap unmount_staging_for_chroot EXIT

sudo cp /etc/resolv.conf "$STAGING/etc/resolv.conf"

# Keep package list in sync with ensureGuestBasePackages in sysconfig.go (guest agent).
sudo chroot "$STAGING" /bin/bash -ec "$(cat <<'INNER'
set -euo pipefail
export HOME=/root
export DEBIAN_FRONTEND=noninteractive
# Match ensurePathInEnv defaultPath (exec.go) for mise resolution.
export PATH=/root/.local/share/mise/shims:/root/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

for dir in /var/log/apt /var/cache/apt/archives/partial; do
  mkdir -p "$dir"
done
if [[ ! -f /var/log/dpkg.log ]]; then
  : > /var/log/dpkg.log
fi

APT_BASE=( -o Acquire::Retries=5 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 )

apt-get "${APT_BASE[@]}" -o APT::Update::Error-Mode=any update

PKGS=(
  make
  git
  curl
  wget
  docker.io
  containerd
  build-essential
  docker-compose-v2
  busybox-static
)

apt-get "${APT_BASE[@]}" install -y --no-install-recommends "${PKGS[@]}"

if ! command -v curl >/dev/null; then
  echo "ERROR: curl missing after apt install" >&2
  exit 1
fi

curl -fsSL https://mise.run | sh

MISE="/root/.local/bin/mise"
if [[ ! -x "$MISE" ]]; then
  echo "ERROR: mise not found after install" >&2
  exit 1
fi

"$MISE" x node@20 -- node --version
"$MISE" use -g node@20

write_wrapper() {
  local path="$1"
  local body="$2"
  printf '%s\n' "$body" > "$path"
  chmod 0755 "$path"
}

write_wrapper /usr/local/bin/npx "#!/usr/bin/env sh
exec $MISE x node@20 -- npx \"\$@\""
cp /usr/local/bin/npx /usr/bin/npx

write_wrapper /usr/local/bin/opencode "#!/usr/bin/env sh
exec $MISE x node@20 -- npx -y opencode-ai@latest \"\$@\""
cp /usr/local/bin/opencode /usr/bin/opencode

write_wrapper /usr/local/bin/codex "#!/usr/bin/env sh
exec $MISE x node@20 -- npx -y @openai/codex@latest \"\$@\""
cp /usr/local/bin/codex /usr/bin/codex

write_wrapper /usr/local/bin/claude "#!/usr/bin/env sh
exec $MISE x node@20 -- npx -y @anthropic-ai/claude-code@latest \"\$@\""
cp /usr/local/bin/claude /usr/bin/claude

for bin in docker dockerd opencode codex claude npx; do
  command -v "$bin" >/dev/null || { echo "ERROR: missing $bin in PATH" >&2; exit 1; }
done

mkdir -p /var/lib
echo ok > /var/lib/nexus-tools-base-v15

apt-get clean
rm -rf /var/lib/apt/lists/*
rm -rf /var/cache/apt/archives/*
INNER
)"

unmount_staging_for_chroot
trap - EXIT

sudo rm -f "$STAGING/usr/bin/qemu-aarch64-static"

sudo mkdir -p "$STAGING/usr/local/bin"
sudo cp /tmp/nexus-guest-agent "$STAGING/usr/local/bin/nexus-guest-agent"
sudo chmod 0755 "$STAGING/usr/local/bin/nexus-guest-agent"
sudo mkdir -p "$STAGING/workspace"
if ! sudo grep -q '/dev/vda' "$STAGING/etc/fstab" 2>/dev/null; then
  echo "/dev/vda / ext4 rw,relatime 0 1" | sudo tee -a "$STAGING/etc/fstab" >/dev/null
fi

sudo chown -R "$(id -u):$(id -g)" "$STAGING"

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
