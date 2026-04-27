#!/usr/bin/env bash
set -euo pipefail

# Seed rootfs-dir for the test runner user so parallel e2e tests don't block on
# debugfs rdump / (which takes 20–30s and serializes via flock).
RUNNER_USER="${SUDO_USER:-${USER:-runner}}"
if [ "$RUNNER_USER" = "root" ]; then
  RUNNER_USER="runner"
fi

runner_home="$(eval echo "~$RUNNER_USER")"
rootfs_dir="$runner_home/.local/share/nexus/vm/rootfs-dir"

if [ -d "$rootfs_dir" ] && [ -f "$rootfs_dir/usr/bin/docker" ]; then
  echo "rootfs-dir already seeded for $RUNNER_USER"
  exit 0
fi

if ! command -v debugfs >/dev/null 2>&1; then
  echo "debugfs not available; skipping rootfs-dir seed"
  exit 0
fi

rootfs_image="${NEXUS_VM_ROOTFS:-/var/lib/nexus/rootfs.ext4}"
if [ ! -f "$rootfs_image" ]; then
  echo "rootfs image not found at $rootfs_image; skipping seed"
  exit 0
fi

echo "Seeding rootfs-dir for $RUNNER_USER..."
mkdir -p "$(dirname "$rootfs_dir")"
tmp_dir="$(mktemp -d "$(dirname "$rootfs_dir")/rootfs-dir-seed-XXXXXX")"
debugfs -R "rdump / $tmp_dir" "$rootfs_image" >/dev/null 2>&1 || true

# Ensure required files were extracted.
if [ ! -f "$tmp_dir/usr/bin/docker" ]; then
  echo "debugfs rdump did not produce expected files; skipping seed"
  rm -rf "$tmp_dir"
  exit 0
fi

rm -rf "$rootfs_dir.bak"
if [ -d "$rootfs_dir" ]; then
  mv "$rootfs_dir" "$rootfs_dir.bak"
fi
mv "$tmp_dir" "$rootfs_dir"
rm -rf "$rootfs_dir.bak"
chown -R "$RUNNER_USER:$RUNNER_USER" "$runner_home/.local/share/nexus" 2>/dev/null || true

echo "rootfs-dir seeded for $RUNNER_USER"
