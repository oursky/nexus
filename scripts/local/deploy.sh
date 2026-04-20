#!/usr/bin/env bash
# scripts/local/deploy.sh
# Build nexus for the local OS/arch and install to ~/.local/bin/nexus.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
LOCAL_BIN="${LOCAL_BIN:-$HOME/.local/bin/nexus}"

echo "Building nexus for $(go env GOOS)/$(go env GOARCH)..."
go build -C "$NEXUS_PKG" -o ./tmp/nexus-local ./cmd/nexus

mkdir -p "$(dirname "$LOCAL_BIN")"
rm -f "$LOCAL_BIN"
cp "$NEXUS_PKG/tmp/nexus-local" "$LOCAL_BIN"
chmod +x "$LOCAL_BIN"

echo "Installed $LOCAL_BIN"
