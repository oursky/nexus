#!/usr/bin/env bash
# scripts/local/deploy.sh
# Build nexus for the local OS/arch and install to ~/.local/bin/nexus.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
LOCAL_BIN="${LOCAL_BIN:-$HOME/.local/bin/nexus-dev}"

BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "$SCRIPT_DIR/../.." rev-parse --short HEAD 2>/dev/null || echo dev)"
LDFLAGS="-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Time=${BUILD_TIME} \
         -X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Commit=${GIT_COMMIT}"

echo "Building nexus for $(go env GOOS)/$(go env GOARCH) (commit=${GIT_COMMIT} built=${BUILD_TIME})..."
# Dev build: enable file-based token storage and headless RPC
go build -C "$NEXUS_PKG" -tags dev -ldflags "$LDFLAGS" -o ./tmp/nexus-local ./cmd/nexus

echo "Building pty-host..."
go build -C "$NEXUS_PKG" -tags dev -ldflags "$LDFLAGS" -o ./tmp/pty-host ./cmd/pty-host

mkdir -p "$(dirname "$LOCAL_BIN")"
rm -f "$LOCAL_BIN"
cp "$NEXUS_PKG/tmp/nexus-local" "$LOCAL_BIN"
chmod +x "$LOCAL_BIN"

# Install pty-host alongside nexus so the daemon can find it
LOCAL_PTY_HOST="$(dirname "$LOCAL_BIN")/pty-host"
rm -f "$LOCAL_PTY_HOST"
cp "$NEXUS_PKG/tmp/pty-host" "$LOCAL_PTY_HOST"
chmod +x "$LOCAL_PTY_HOST"

if [ "$(uname -s)" = "Darwin" ]; then
  ENTITLEMENTS="$NEXUS_PKG/nexus.entitlements"
  if [ -f "$ENTITLEMENTS" ]; then
    codesign --sign - --force --entitlements "$ENTITLEMENTS" "$LOCAL_BIN" 2>/dev/null && \
      echo "Codesigned with hypervisor entitlements" || \
      echo "Warning: codesign failed (hypervisor access may be unavailable)"
  fi
fi

echo "Installed $LOCAL_BIN"
echo "$($LOCAL_BIN daemon version)"
