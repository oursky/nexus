#!/usr/bin/env bash
set -euo pipefail

# Quick probe: can this self-hosted runner forward VM TCP traffic via passt?
#
# Starts a minimal bake VM and monitors the serial log. If apt-get stalls at
# "0% [Connecting..." for too long, passt TCP forwarding is broken in this
# runner environment (common in restricted containers without NET_ADMIN).
#
# Exits 0  → network OK, prewarm can proceed
# Exits 1  → network broken, skip prewarm to avoid a 10-minute timeout

SOCK=/tmp/nexus-probe.sock
DB=/tmp/nexus-probe.db

# Clean up any stale probe artifacts
sudo rm -rf "$SOCK" "$DB" /tmp/nexus-probe-*

echo "[probe] starting daemon with 90s bake timeout..."
sudo CI=false NEXUS_LIBKRUN_BAKE_TIMEOUT=90s NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=1 /tmp/nexus-bin daemon start \
  --db "$DB" --socket "$SOCK" --workdir-root /tmp/nexus-probe --network=false > /dev/null 2>&1 &
DPID=$!

cleanup() {
  sudo /tmp/nexus-bin daemon stop --socket "$SOCK" > /dev/null 2>&1 || true
  kill $DPID 2>/dev/null || true
  wait $DPID 2>/dev/null || true
}
trap cleanup EXIT

# Wait for socket (max 60s)
if ! timeout 60 bash -c 'until [ -S "$0" ]; do sleep 1; done' "$SOCK"; then
  echo "[probe] daemon start timed out"
  exit 1
fi

# Wait for bake VM to boot and apt-get to start (typical: 20-40s)
sleep 35

# Find the bake log
BAKE_LOG=$(ls /tmp/nexus-probe-*/bake.log.hvc0 2>/dev/null | head -1 || true)
if [[ ! -f "$BAKE_LOG" ]]; then
  echo "[probe] no bake log found — assuming network broken"
  exit 1
fi

# Check for DNS failure
if grep -q "Temporary failure resolving" "$BAKE_LOG"; then
  echo "[probe] DNS resolution failed inside VM"
  exit 1
fi

# Check for TCP stall: apt-get stuck at 0% with no progress
# A healthy run shows package download progress within ~60s.
if grep -q "0% \[Connecting" "$BAKE_LOG"; then
  # See if any download progressed past 0%
  if ! grep -E "[1-9][0-9]*% \[.*(Working|Connecting|Waiting)" "$BAKE_LOG" > /dev/null; then
    echo "[probe] apt-get stalled at 0% — passt TCP forwarding is broken in this runner"
    exit 1
  fi
fi

echo "[probe] VM network forwarding looks OK"
exit 0
