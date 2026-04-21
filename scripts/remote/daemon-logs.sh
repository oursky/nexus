#!/usr/bin/env bash
# scripts/remote/daemon-logs.sh
# Tail the daemon log from the remote host.
#
# Usage: REMOTE_HOST=user@host scripts/remote/daemon-logs.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"

# Log file lives next to the daemon socket under the default state dir (~/.local/state/nexus on Linux).
ssh "$REMOTE_HOST" 'tail -100 "${XDG_STATE_HOME:-$HOME/.local/state}/nexus/daemon.log"'
