#!/usr/bin/env bash
# scripts/remote/daemon-restart.sh
# Stop and start the nexus daemon on a remote host.
# nexus daemon start auto-provisions all prerequisites (firecracker, kernel,
# rootfs, tap-helper) — no flags needed.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN=~/.local/bin/nexus] scripts/remote/daemon-restart.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"

echo "Stopping daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon stop 2>/dev/null || true"

echo "Starting daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon start"

echo "Daemon restarted."
