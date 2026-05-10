#!/usr/bin/env bash
# scripts/remote/daemon-logs.sh
# Tail the daemon log from the remote host.
#
# Usage: REMOTE_HOST=user@host [REMOTE_XDG_STATE_HOME='$HOME/.local/state-dev'] scripts/remote/daemon-logs.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_XDG_STATE_HOME="${REMOTE_XDG_STATE_HOME:-\$HOME/.local/state-dev}"

# Log file lives next to the daemon socket under the state dir.
ssh "$REMOTE_HOST" "tail -100 \"${REMOTE_XDG_STATE_HOME}/nexus/daemon.log\""
