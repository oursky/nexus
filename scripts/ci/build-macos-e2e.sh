#!/usr/bin/env bash
# Build nexus for macOS E2E, codesign with hypervisor entitlements,
# and download gvproxy. The gvproxy download is parallelized with Go compilation
# to save ~20-30 s. Exports NEXUS_E2E_BINARY and adds the binary dir to PATH
# via GITHUB_ENV / GITHUB_PATH when running in CI.
#
# Env vars (all have defaults):
#   NEXUS_E2E_BINARY_OUT   – output path for the nexus binary  (default: /tmp/nexus-bin)
#   GVPROXY_VERSION        – gvproxy release tag without "v"   (default: 0.8.8)
#   GVPROXY_CACHE_DIR      – directory to cache gvproxy binary (default: ~/.cache/nexus/bin)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT/packages/nexus"

NEXUS_E2E_BINARY_OUT="${NEXUS_E2E_BINARY_OUT:-/tmp/nexus-bin}"
GVPROXY_VERSION="${GVPROXY_VERSION:-0.8.8}"
GVPROXY_CACHE_DIR="${GVPROXY_CACHE_DIR:-$HOME/.cache/nexus/bin}"

export CGO_ENABLED=0

mkdir -p "$GVPROXY_CACHE_DIR"

# Download gvproxy in the background while Go compiles.
(curl -fsSL --retry 3 -L \
  "https://github.com/containers/gvisor-tap-vsock/releases/download/v${GVPROXY_VERSION}/gvproxy-darwin" \
  -o "$GVPROXY_CACHE_DIR/gvproxy" \
  && chmod +x "$GVPROXY_CACHE_DIR/gvproxy") &
GVPROXY_PID=$!

go build -o "$NEXUS_E2E_BINARY_OUT" ./cmd/nexus/

# Codesign nexus binary (Hypervisor.framework + Virtualization.framework).
codesign --entitlements "$SCRIPT_DIR/nexus-entitlements.plist" \
  --force --sign - "$NEXUS_E2E_BINARY_OUT"

# Wait for gvproxy download to finish.
wait "$GVPROXY_PID"

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "NEXUS_E2E_BINARY=$NEXUS_E2E_BINARY_OUT" >> "$GITHUB_ENV"
fi
if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$(dirname "$NEXUS_E2E_BINARY_OUT")" >> "$GITHUB_PATH"
fi
