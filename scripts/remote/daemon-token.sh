#!/usr/bin/env bash
# scripts/remote/daemon-token.sh
# Print the daemon auth token from the remote host.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN='$HOME/.local/bin/nexus'] scripts/remote/daemon-token.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus}"

ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon token"
