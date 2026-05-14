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

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w" \
  -o "$NEXUS_CROSS_OUT_DIR/nexus-darwin-arm64" \
  ./cmd/nexus

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "NEXUS_CROSS_BINARY_DIR=$NEXUS_CROSS_OUT_DIR" >> "$GITHUB_ENV"
fi
