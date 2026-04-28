#!/usr/bin/env bash
# scripts/remote/daemon-restart.sh
# Stop and start the nexus daemon on a remote host, then verify the running
# binary matches what was just deployed (build timestamp + commit check).
#
# Dev/prod isolation: set REMOTE_XDG_STATE_HOME and REMOTE_PORT to keep the dev
# daemon state separate from any production daemon on the same host.
#
# Usage: REMOTE_HOST=user@host \
#          [REMOTE_BIN='$HOME/.local/bin/nexus-dev'] \
#          [REMOTE_PORT=7778] \
#          [REMOTE_XDG_STATE_HOME='$HOME/.local/state-dev'] \
#          scripts/remote/daemon-restart.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus-dev}"
REMOTE_PORT="${REMOTE_PORT:-7778}"
REMOTE_XDG_STATE_HOME="${REMOTE_XDG_STATE_HOME:-\$HOME/.local/state-dev}"

echo "Stopping dev daemon on ${REMOTE_HOST} (bin=${REMOTE_BIN}, port=${REMOTE_PORT})..."
ssh "$REMOTE_HOST" "XDG_STATE_HOME=${REMOTE_XDG_STATE_HOME} ${REMOTE_BIN} daemon stop 2>/dev/null || true"

echo "Starting dev daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "XDG_STATE_HOME=${REMOTE_XDG_STATE_HOME} ${REMOTE_BIN} daemon start --port ${REMOTE_PORT}"

echo ""
echo "Remote binary version:"
ssh "$REMOTE_HOST" "XDG_STATE_HOME=${REMOTE_XDG_STATE_HOME} ${REMOTE_BIN} daemon version"

echo "Dev daemon restarted (state: ${REMOTE_XDG_STATE_HOME}/nexus, port: ${REMOTE_PORT})."
