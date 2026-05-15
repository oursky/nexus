#!/usr/bin/env bash
# stage-linux-nexus.sh — cross-compile the nexus daemon for Linux amd64 and stage
# the binary into packages/nexus-swift/Resources/ so the Mac app can bundle it
# for remote auto-provisioning.
#
# Usage:
#   ./scripts/swift/stage-linux-nexus.sh
#
# The compiled binary is placed at:
#   packages/nexus-swift/Resources/nexus-linux-amd64
#
# Requirements:
#   - Go 1.22+ with CGO_ENABLED=0 (pure-Go CLI build; libkrun is separate)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
NEXUS_PKG="$REPO_ROOT/packages/nexus"
RESOURCES_DIR="$REPO_ROOT/packages/nexus-swift/Resources"
EMBED_DIR="$NEXUS_PKG/cmd/nexus"
STAGED_AGENT_FILES=()

mkdir -p "$RESOURCES_DIR"
mkdir -p "$NEXUS_PKG/tmp"

cleanup_staged_agents() {
  if [[ "${#STAGED_AGENT_FILES[@]}" -gt 0 ]]; then
    rm -f "${STAGED_AGENT_FILES[@]}"
  fi
}
trap cleanup_staged_agents EXIT

stage_embedded_agent() {
  local arch="$1"
  local built="$NEXUS_PKG/tmp/nexus-guest-agent-linux-${arch}"
  local staged="$EMBED_DIR/agent-linux-${arch}"

  echo "==> Preparing embedded guest agent for linux/${arch}…"
  (
    cd "$NEXUS_PKG"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
      -trimpath \
      -ldflags="-s -w" \
      -o "$built" \
      ./cmd/nexus-guest-agent
  )
  cp "$built" "$staged"
  chmod +x "$staged"
  STAGED_AGENT_FILES+=("$staged")
}

stage_embedded_agent amd64

echo "==> Building nexus for linux/amd64…"
(
  cd "$NEXUS_PKG"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o "$RESOURCES_DIR/nexus-linux-amd64" \
    ./cmd/nexus
)
chmod +x "$RESOURCES_DIR/nexus-linux-amd64"
size="$(du -sh "$RESOURCES_DIR/nexus-linux-amd64" | cut -f1)"
echo "    OK: $RESOURCES_DIR/nexus-linux-amd64 ($size)"

echo ""
echo "Staged Linux nexus binary to: $RESOURCES_DIR"
echo "Run 'xcodegen generate' in packages/nexus-swift/ to pick them up."
