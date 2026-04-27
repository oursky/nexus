#!/usr/bin/env bash
set -euo pipefail

if grep -Eq '^\s*profile\s*=\s*"minimal"\s*$' Nexusfile 2>/dev/null; then
  echo "Nexusfile profile is minimal; skipping bake prewarm."
  exit 0
fi
if sudo test -f /root/.local/state/nexus/rootfs-baked-v7; then
  echo "Baked stamp already present; skipping prewarm."
  exit 0
fi

SOCK=/tmp/nexus-prewarm.sock
DB=/tmp/nexus-prewarm.db

# Purge any stale extracted nexus-libkrun-vm so the daemon extracts the fresh
# embedded binary from /tmp/nexus-bin on this run.
sudo rm -f /root/.local/share/nexus/bin/nexus-libkrun-vm \
  /root/.local/share/nexus/lib/libkrun-embed.so \
  /root/.local/share/nexus/lib/libkrunfw-embed.so

sudo CI=false NEXUS_LIBKRUN_BAKE_TIMEOUT=600s NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=1 /tmp/nexus-bin daemon start \
  --db "$DB" --socket "$SOCK" --workdir-root /data/nexus/libkrun-vms-prewarm --network=false &
DPID=$!

# Wait up to 12 minutes for the daemon socket to appear (bake can take 8-10 min).
timeout 720 bash -c 'until [ -S "$0" ]; do sleep 2; done' "$SOCK" \
  || { echo "default-profile prewarm timed out (non-fatal)"; kill $DPID 2>/dev/null || true; exit 0; }

sudo /tmp/nexus-bin daemon stop --socket "$SOCK" 2>/dev/null \
  || kill $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
