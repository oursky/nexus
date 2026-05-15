#!/usr/bin/env bash
# Cross-compile the nexus binary for darwin/arm64 and stage it for fat bundle tests.
# The bundle exporter embeds both linux-amd64 and darwin-arm64 binaries.
#
# Env vars:
#   NEXUS_CROSS_OUT_DIR – output directory (default: <repo>/packages/nexus-swift/Resources)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

NEXUS_CROSS_OUT_DIR="${NEXUS_CROSS_OUT_DIR:-${GITHUB_WORKSPACE:-$REPO_ROOT}/packages/nexus-swift/Resources}"
mkdir -p "$NEXUS_CROSS_OUT_DIR"

cd "$REPO_ROOT/packages/nexus"

# Darwin/arm64 nexus embeds the Linux/arm64 guest agent (see embed_agent_darwin_arm64.go).
# `go generate ./cmd/nexus` on linux/amd64 skips darwin-only sources, so ensure the blob exists.
AGENT_ARM64="$REPO_ROOT/packages/nexus/cmd/nexus/agent-linux-arm64"
if [[ ! -f "$AGENT_ARM64" ]]; then
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w" \
    -o "$AGENT_ARM64" \
    ./cmd/nexus-guest-agent/
fi

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w" \
  -o "$NEXUS_CROSS_OUT_DIR/nexus-darwin-arm64" \
  ./cmd/nexus

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "NEXUS_CROSS_BINARY_DIR=$NEXUS_CROSS_OUT_DIR" >> "$GITHUB_ENV"
fi
