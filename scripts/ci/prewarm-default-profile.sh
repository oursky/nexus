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

sudo CI=false NEXUS_LIBKRUN_BAKE_TIMEOUT=180s NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=1 /tmp/nexus-bin daemon start \
  --db "$DB" --socket "$SOCK" --workdir-root /data/nexus/libkrun-vms-prewarm --network=false > /tmp/nexus-prewarm-daemon.log 2>&1 &
DPID=$!

echo "prewarm: waiting for daemon socket (max 300s, bake timeout 180s)..."

# Wait for socket, but also check if daemon is still alive
SECONDS_WAITED=0
while [ $SECONDS_WAITED -lt 300 ]; do
  if [ -S "$SOCK" ]; then
    echo "prewarm: daemon socket ready"
    break
  fi
  if ! kill -0 $DPID 2>/dev/null; then
    echo "prewarm: daemon died before socket appeared (see /tmp/nexus-prewarm-daemon.log)"
    wait $DPID 2>/dev/null || true
    echo "prewarm: daemon exit code: $?"
    cat /tmp/nexus-prewarm-daemon.log | tail -20 || true
    exit 0
  fi
  sleep 2
  SECONDS_WAITED=$((SECONDS_WAITED + 2))
done

if [ ! -S "$SOCK" ]; then
  echo "default-profile prewarm timed out (non-fatal)"
  kill $DPID 2>/dev/null || true
  wait $DPID 2>/dev/null || true
  exit 0
fi

sudo /tmp/nexus-bin daemon stop --socket "$SOCK" 2>/dev/null \
  || kill $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
