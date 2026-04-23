#!/usr/bin/env bash
# scripts/remote/deploy.sh
# Cross-compile nexus for linux/amd64 and deploy to a remote host.
# Also stages the linux binary to packages/nexus-swift/Resources/nexus-linux-amd64
# so the Mac app's provision endpoint always uploads the same binary that was
# just built — never a stale embedded copy.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN=~/.local/bin/nexus] scripts/remote/deploy.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"
REMOTE_AGENT_BIN="${REMOTE_AGENT_BIN:-~/.local/bin/nexus-firecracker-agent}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
SWIFT_RESOURCES="$SCRIPT_DIR/../../packages/nexus-swift/Resources"

# Build timestamp and git commit for build.Info() traceability.
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "$SCRIPT_DIR/../.." rev-parse --short HEAD 2>/dev/null || echo dev)"
LDFLAGS="-X github.com/oursky/nexus/packages/nexus/internal/build.Time=${BUILD_TIME} \
         -X github.com/oursky/nexus/packages/nexus/internal/build.Commit=${GIT_COMMIT}"

echo "Building nexus for linux/amd64 (commit=${GIT_COMMIT} built=${BUILD_TIME})..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -C "$NEXUS_PKG" \
  -ldflags "$LDFLAGS" \
  -o ./tmp/nexus-linux \
  ./cmd/nexus

echo "Building nexus-firecracker-agent for linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -C "$NEXUS_PKG" \
  -ldflags "$LDFLAGS" \
  -o ./tmp/nexus-firecracker-agent-linux \
  ./cmd/nexus-firecracker-agent

# Keep the Mac app's embedded linux binary in sync so provision never
# re-uploads a stale version over a freshly deployed remote daemon.
if [ -d "$SWIFT_RESOURCES" ]; then
  cp "$NEXUS_PKG/tmp/nexus-linux" "$SWIFT_RESOURCES/nexus-linux-amd64"
  chmod +x "$SWIFT_RESOURCES/nexus-linux-amd64"
  echo "Staged  → packages/nexus-swift/Resources/nexus-linux-amd64 (kept in sync)"
fi

# Also embed the new agent binary inside cmd/nexus (used by the linker //go:embed).
cp "$NEXUS_PKG/tmp/nexus-firecracker-agent-linux" "$NEXUS_PKG/cmd/nexus/agent-linux-amd64"
chmod +x "$NEXUS_PKG/cmd/nexus/agent-linux-amd64"

echo "Deploying to ${REMOTE_HOST}:${REMOTE_BIN}..."
ssh "$REMOTE_HOST" "BIN=${REMOTE_BIN}; mkdir -p \"\$(dirname \"\$BIN\")\"; rm -f \"\$BIN\""
scp "$NEXUS_PKG/tmp/nexus-linux" "${REMOTE_HOST}:${REMOTE_BIN}"
ssh "$REMOTE_HOST" "chmod +x ${REMOTE_BIN}"

echo "Deploying guest agent binary to ${REMOTE_HOST}:${REMOTE_AGENT_BIN}..."
ssh "$REMOTE_HOST" "BIN=${REMOTE_AGENT_BIN}; mkdir -p \"\$(dirname \"\$BIN\")\"; rm -f \"\$BIN\""
scp "$NEXUS_PKG/tmp/nexus-firecracker-agent-linux" "${REMOTE_HOST}:${REMOTE_AGENT_BIN}"
ssh "$REMOTE_HOST" "chmod +x ${REMOTE_AGENT_BIN}"

echo "Deployed successfully (commit=${GIT_COMMIT} built=${BUILD_TIME})."
