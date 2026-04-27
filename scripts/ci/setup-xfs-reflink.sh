#!/usr/bin/env bash
set -euo pipefail

MOUNT_POINT="/data/nexus"
BACKING_FILE="/var/lib/nexus-xfs-backing.img"
SIZE_GB="${NEXUS_XFS_SIZE_GB:-20}"

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
sudo mount -o loop "$BACKING_FILE" "$MOUNT_POINT"
sudo chmod 777 "$MOUNT_POINT"

echo "XFS with reflink=1 mounted at $MOUNT_POINT"
xfs_info "$MOUNT_POINT" | grep reflink
