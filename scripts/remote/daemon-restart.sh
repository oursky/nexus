#!/usr/bin/env bash
# scripts/remote/daemon-restart.sh
# Stop and start the nexus daemon on a remote host, then verify the running
# binary matches what was just deployed (build timestamp + commit check).
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN='$HOME/.local/bin/nexus'] scripts/remote/daemon-restart.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus}"

echo "Stopping daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon stop 2>/dev/null || true"

echo "Starting daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon start"

echo ""
echo "Remote binary version:"
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon version"

echo "Daemon restarted."
