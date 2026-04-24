#!/usr/bin/env bash
# scripts/local/deploy.sh
# Build nexus for the local OS/arch and install to ~/.local/bin/nexus.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
LOCAL_BIN="${LOCAL_BIN:-$HOME/.local/bin/nexus}"

BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "$SCRIPT_DIR/../.." rev-parse --short HEAD 2>/dev/null || echo dev)"
LDFLAGS="-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Time=${BUILD_TIME} \
         -X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Commit=${GIT_COMMIT}"

echo "Building nexus for $(go env GOOS)/$(go env GOARCH) (commit=${GIT_COMMIT} built=${BUILD_TIME})..."
go build -C "$NEXUS_PKG" -ldflags "$LDFLAGS" -o ./tmp/nexus-local ./cmd/nexus

mkdir -p "$(dirname "$LOCAL_BIN")"
rm -f "$LOCAL_BIN"
cp "$NEXUS_PKG/tmp/nexus-local" "$LOCAL_BIN"
chmod +x "$LOCAL_BIN"

echo "Installed $LOCAL_BIN"
echo "$($LOCAL_BIN daemon version)"
