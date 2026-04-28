#!/usr/bin/env bash
# scripts/remote/daemon-status.sh
# Check daemon status on the remote host.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN='$HOME/.local/bin/nexus-dev'] [REMOTE_PORT=7778] scripts/remote/daemon-status.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus-dev}"
REMOTE_PORT="${REMOTE_PORT:-7778}"

ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon status 2>&1; echo '---'; curl -sf http://127.0.0.1:${REMOTE_PORT}/healthz && echo ' (healthy)' || echo ' (unreachable)'"
