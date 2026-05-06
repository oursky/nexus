#!/usr/bin/env bash
# scripts/remote/mac-test-headless.sh
# From the Linux dev machine: enables the headless RPC sentinel on the Mac,
# opens an SSH tunnel, and verifies the RPC is alive and connected.
#
# Usage: MAC_HOST=newman@minion scripts/remote/mac-test-headless.sh
set -euo pipefail

MAC_HOST="${MAC_HOST:?MAC_HOST is not set. Add MAC_HOST=user@mac-hostname to .env.local}"

# Port on the *local* Linux side of the tunnel → Mac's 127.0.0.1:7778
LOCAL_TUNNEL_PORT="${LOCAL_TUNNEL_PORT:-17778}"

# ── 1. Enable headless RPC on Mac ────────────────────────────────────────────
echo "Enabling headless RPC sentinel on ${MAC_HOST} ..."
ssh "$MAC_HOST" "touch ~/.nexus-headless-rpc"

# ── 2. Open SSH tunnel to Mac's headless RPC port ───────────────────────────
# -f  background
# -N  no command (tunnel only)
# -L  local:remote port forward
# -o  exit if no forwarding is possible
echo "Opening SSH tunnel: localhost:${LOCAL_TUNNEL_PORT} → ${MAC_HOST}:127.0.0.1:7778 ..."
ssh -fN \
  -L "${LOCAL_TUNNEL_PORT}:127.0.0.1:7778" \
  -o ExitOnForwardFailure=yes \
  -o ControlMaster=no \
  "$MAC_HOST"

TUNNEL_PID=$(pgrep -f "ssh.*${LOCAL_TUNNEL_PORT}:127.0.0.1:7778.*${MAC_HOST}" | head -1 || true)
cleanup() { [ -n "${TUNNEL_PID:-}" ] && kill "$TUNNEL_PID" 2>/dev/null || true; }
trap cleanup EXIT

sleep 1

# ── 3. Check /status ─────────────────────────────────────────────────────────
echo "Checking headless RPC at http://127.0.0.1:${LOCAL_TUNNEL_PORT}/status ..."
RESP=$(curl -sf --max-time 5 "http://127.0.0.1:${LOCAL_TUNNEL_PORT}/status" 2>/dev/null || echo '{}')
echo "Response: $RESP"

python3 - <<EOF
import json, sys
d = json.loads("""$RESP""")
assert d.get('ok'), f"ok=false: {d}"
conn = d.get('connection', {})
state = conn.get('connectionState', 'unknown')
print(f"✓ headless RPC active  connectionState={state}")
if state != 'connected':
    print(f"  WARNING: app is not connected to a daemon (state={state})")
    print("  Run: nexus-dev daemon connect <host>  (or provision from the Mac app)")
EOF
