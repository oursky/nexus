#!/usr/bin/env bash
set -euo pipefail

SOCK=/tmp/nexus-prebuilt.sock
DB=/tmp/nexus-prebuilt.db

sudo NEXUS_LIBKRUN_BAKE_TIMEOUT=300s NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=1 /tmp/nexus-bin daemon start \
  --db "$DB" --socket "$SOCK" --workdir-root /tmp/nexus-prebuilt-work &
DPID=$!

timeout 600 bash -c 'until [ -S "$0" ]; do sleep 2; done' "$SOCK" \
  || { echo "prebuild timed out after 600s"; kill $DPID; exit 1; }

sudo /tmp/nexus-bin daemon stop --socket "$SOCK" 2>/dev/null \
  || kill $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
