#!/usr/bin/env bash
set -euo pipefail

CACHE_DIR="${LIBKRUN_BAKED_CACHE_DIR:-}"
if [[ -z "$CACHE_DIR" ]]; then
  echo "LIBKRUN_BAKED_CACHE_DIR not set; nothing to refresh."
  exit 0
fi
mkdir -p "$CACHE_DIR"

if sudo test -f /root/.local/share/nexus/vm/vmlinux.bin; then
  sudo cp /root/.local/share/nexus/vm/vmlinux.bin "$CACHE_DIR/vmlinux.bin"
fi
if sudo test -f /root/.local/share/nexus/vm/rootfs.ext4; then
  sudo cp /root/.local/share/nexus/vm/rootfs.ext4 "$CACHE_DIR/rootfs.ext4"
fi
if sudo test -f /root/.local/state/nexus/rootfs-agent.sha256; then
  sudo cp /root/.local/state/nexus/rootfs-agent.sha256 "$CACHE_DIR/rootfs-agent.sha256"
fi
# Copy any baked stamp files to cache (handles current and legacy versions).
for stamp in /root/.local/state/nexus/rootfs-baked-v*; do
  if sudo test -f "$stamp"; then
    sudo cp "$stamp" "$CACHE_DIR/$(basename "$stamp")"
  fi
done
