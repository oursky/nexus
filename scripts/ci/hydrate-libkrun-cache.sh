#!/usr/bin/env bash
set -euo pipefail

CACHE_DIR="${LIBKRUN_BAKED_CACHE_DIR:?LIBKRUN_BAKED_CACHE_DIR is required}"

if [[ ! -f "$CACHE_DIR/vmlinux.bin" || ! -f "$CACHE_DIR/rootfs.ext4" ]]; then
  echo "No complete baked cache payload found; continuing with cold provisioning."
  exit 0
fi

sudo install -d -m 0755 /var/lib/nexus /root/.local/share/nexus/vm /root/.local/state/nexus
for f in vmlinux.bin rootfs.ext4; do
  sudo cp "$CACHE_DIR/$f" "/var/lib/nexus/$f"
  sudo cp "$CACHE_DIR/$f" "/root/.local/share/nexus/vm/$f"
  sudo chmod 644 "/var/lib/nexus/$f" "/root/.local/share/nexus/vm/$f"
done

if [[ -f "$CACHE_DIR/rootfs-agent.sha256" ]]; then
  sudo cp "$CACHE_DIR/rootfs-agent.sha256" /root/.local/state/nexus/rootfs-agent.sha256
fi
if [[ -f "$CACHE_DIR/rootfs-baked-v7" ]]; then
  sudo cp "$CACHE_DIR/rootfs-baked-v7" /root/.local/state/nexus/rootfs-baked-v7
elif [[ -f "$CACHE_DIR/rootfs-baked-v6" ]]; then
  sudo cp "$CACHE_DIR/rootfs-baked-v6" /root/.local/state/nexus/rootfs-baked-v7
elif [[ -f "$CACHE_DIR/rootfs-baked-v5" ]]; then
  sudo cp "$CACHE_DIR/rootfs-baked-v5" /root/.local/state/nexus/rootfs-baked-v7
elif [[ -f "$CACHE_DIR/rootfs-baked-v4" ]]; then
  sudo cp "$CACHE_DIR/rootfs-baked-v4" /root/.local/state/nexus/rootfs-baked-v7
fi

mkdir -p "$HOME/.local/state/nexus"
if sudo test -f /root/.local/state/nexus/rootfs-agent.sha256; then
  sudo cat /root/.local/state/nexus/rootfs-agent.sha256 > "$HOME/.local/state/nexus/rootfs-agent.sha256"
fi
