#!/usr/bin/env bash
set -euo pipefail
RUNNER_USER="${USER:-$(id -un)}"

OUT="prebuilt-libkrun-vm-linux-amd64.tar.gz"
mkdir -p /tmp/prebuilt-vm

sudo cp /root/.local/share/nexus/vm/vmlinux.bin /tmp/prebuilt-vm/vmlinux.bin
sudo cp /root/.local/share/nexus/vm/rootfs.ext4 /tmp/prebuilt-vm/rootfs.ext4
sudo cp /root/.local/state/nexus/rootfs-agent.sha256 /tmp/prebuilt-vm/rootfs-agent.sha256 || true
if sudo test -f /root/.local/state/nexus/rootfs-baked-v7; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v7 /tmp/prebuilt-vm/rootfs-baked-v7
elif sudo test -f /root/.local/state/nexus/rootfs-baked-v6; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v6 /tmp/prebuilt-vm/rootfs-baked-v7
elif sudo test -f /root/.local/state/nexus/rootfs-baked-v5; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v5 /tmp/prebuilt-vm/rootfs-baked-v7
elif sudo test -f /root/.local/state/nexus/rootfs-baked-v4; then
  sudo cp /root/.local/state/nexus/rootfs-baked-v4 /tmp/prebuilt-vm/rootfs-baked-v7
fi

sudo chown -R "$RUNNER_USER":"$RUNNER_USER" /tmp/prebuilt-vm
tar -C /tmp/prebuilt-vm -czf "$OUT" .
sha256sum "$OUT" > "$OUT.sha256"
