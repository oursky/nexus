#!/usr/bin/env bash
set -euo pipefail

CACHE_DIR="${LIBKRUN_BAKED_CACHE_DIR:?LIBKRUN_BAKED_CACHE_DIR is required}"
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
if sudo test -f /root/.local/state/nexus/rootfs-baked-v6; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v6 "$CACHE_DIR/rootfs-baked-v6"
elif sudo test -f /root/.local/state/nexus/rootfs-baked-v5; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v5 "$CACHE_DIR/rootfs-baked-v5"
elif sudo test -f /root/.local/state/nexus/rootfs-baked-v4; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v4 "$CACHE_DIR/rootfs-baked-v4"
fi
