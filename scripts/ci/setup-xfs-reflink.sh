#!/usr/bin/env bash
set -euo pipefail

MOUNT_POINT="/data/nexus"
BACKING_FILE="/var/lib/nexus-xfs-backing.img"
SIZE_GB="${NEXUS_XFS_SIZE_GB:-20}"
LOCK_FILE="/tmp/nexus-xfs-setup.lock"

# Multiple matrix shards can run on the same runner host. Serialize setup
# so one shard doesn't unmount/remove while another is preparing the mount.
exec 9>"$LOCK_FILE"
flock 9

# Check if already correctly set up.
if mountpoint -q "$MOUNT_POINT" 2>/dev/null; then
  fs_type=$(findmnt -n -o FSTYPE "$MOUNT_POINT" 2>/dev/null || true)
  if [ "$fs_type" = "xfs" ]; then
    if xfs_info "$MOUNT_POINT" 2>/dev/null | grep -q "reflink=1"; then
      echo "XFS with reflink=1 already mounted at $MOUNT_POINT"
      exit 0
    fi
  fi
  echo "Unmounting existing filesystem at $MOUNT_POINT"
  sudo umount "$MOUNT_POINT" || true
fi

# Clean slate for mount point.
sudo rm -rf "$MOUNT_POINT" 2>/dev/null || true
sudo mkdir -p "$MOUNT_POINT"

# Helper: check whether a directory supports reflink.
reflink_supported() {
  local dir="$1"
  local tmp_src="$dir/.nexus-reflink-test-src-$$"
  local tmp_dst="$dir/.nexus-reflink-test-dst-$$"
  touch "$tmp_src" && cp --reflink=always "$tmp_src" "$tmp_dst" 2>/dev/null
  local rc=$?
  rm -f "$tmp_src" "$tmp_dst"
  return $rc
}

# If the host filesystem already supports reflink, we don't need a loopback file.
if reflink_supported "$MOUNT_POINT"; then
  sudo chmod 777 "$MOUNT_POINT"
  echo "Host filesystem supports reflink — using $MOUNT_POINT directly"
  exit 0
fi

# Host filesystem doesn't support reflink; we need an XFS loopback image.
echo "Host filesystem does not support reflink; creating XFS loopback image..."

# Create or reuse backing sparse file.
if [ -f "$BACKING_FILE" ]; then
  echo "Reusing existing XFS backing file $BACKING_FILE"
else
  echo "Creating sparse XFS backing file (${SIZE_GB} GB)..."
  sudo mkdir -p "$(dirname "$BACKING_FILE")"
  sudo truncate -s "${SIZE_GB}G" "$BACKING_FILE"
  sudo mkfs.xfs -f -m reflink=1 "$BACKING_FILE"
fi

# Mount loopback.
if sudo mount -o loop "$BACKING_FILE" "$MOUNT_POINT" 2>/dev/null; then
  sudo chmod 777 "$MOUNT_POINT"
  echo "XFS with reflink=1 mounted at $MOUNT_POINT"
  xfs_info "$MOUNT_POINT" | grep reflink
  exit 0
fi

# Loop mount failed and host filesystem doesn't support reflink.
echo "ERROR: Cannot set up reflink-supporting filesystem for libkrun workspaces." >&2
echo "Tried:" >&2
echo "  1. Host filesystem at $MOUNT_POINT — reflink not supported" >&2
echo "  2. XFS loopback mount from $BACKING_FILE — loop device unavailable" >&2
echo "" >&2
echo "Self-hosted runner must satisfy ONE of the following:" >&2
echo "  - Host filesystem is XFS or btrfs with reflink=1" >&2
echo "  - /dev/loop* devices are available and the runner has CAP_SYS_ADMIN" >&2
echo "  - $MOUNT_POINT is pre-mounted as an XFS/btrfs volume with reflink" >&2
exit 1
