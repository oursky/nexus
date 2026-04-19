#!/usr/bin/env bash
# scripts/remote/daemon-restart.sh
# Stop and start the nexus daemon on a remote host using the nexus CLI.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN=~/.local/bin/nexus] [REMOTE_PORT=7777] scripts/remote/daemon-restart.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"
REMOTE_PORT="${REMOTE_PORT:-7777}"

echo "Stopping daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon stop 2>/dev/null || true"

echo "Starting daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "nohup ${REMOTE_BIN} daemon start --network --bind 127.0.0.1 --port ${REMOTE_PORT} > /tmp/nexus-daemon.log 2>&1 &"

sleep 3

echo "Checking health..."
ssh "$REMOTE_HOST" "curl -sf http://127.0.0.1:${REMOTE_PORT}/healthz && echo ' ✓ daemon healthy' || echo ' ✗ daemon not healthy'"
