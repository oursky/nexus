#!/usr/bin/env bash
# scripts/remote/deploy.sh
# Cross-compile nexus for linux/amd64 and deploy to a remote host.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN=~/.local/bin/nexus] scripts/remote/deploy.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"

echo "Building nexus for linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -C "$NEXUS_PKG" -o ./tmp/nexus-linux ./cmd/nexus

echo "Deploying to ${REMOTE_HOST}:${REMOTE_BIN}..."
ssh "$REMOTE_HOST" "BIN=${REMOTE_BIN}; mkdir -p \"\$(dirname \"\$BIN\")\"; rm -f \"\$BIN\""
scp "$NEXUS_PKG/tmp/nexus-linux" "${REMOTE_HOST}:${REMOTE_BIN}"
ssh "$REMOTE_HOST" "chmod +x ${REMOTE_BIN}"

echo "Deployed successfully."
