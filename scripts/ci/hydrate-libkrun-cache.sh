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
# Copy any baked stamp files from cache (handles current and legacy versions).
for stamp in "$CACHE_DIR"/rootfs-baked-v*; do
  if [[ -f "$stamp" ]]; then
    sudo cp "$stamp" /root/.local/state/nexus/
  fi
done

mkdir -p "$HOME/.local/state/nexus"
if sudo test -f /root/.local/state/nexus/rootfs-agent.sha256; then
  sudo cat /root/.local/state/nexus/rootfs-agent.sha256 > "$HOME/.local/state/nexus/rootfs-agent.sha256"
fi
