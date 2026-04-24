#!/usr/bin/env bash
# stage-linux-nexus.sh — cross-compile the nexus daemon for Linux and stage the
# resulting binaries into packages/nexus-swift/Resources/ so the Mac app can
# bundle them for remote auto-provisioning.
#
# Usage:
#   ./scripts/swift/stage-linux-nexus.sh [amd64|arm64|both]
#
# The compiled binaries are placed at:
#   packages/nexus-swift/Resources/nexus-linux-amd64
#   packages/nexus-swift/Resources/nexus-linux-arm64
#
# Requirements:
#   - Go 1.22+ with CGO_ENABLED=0 (pure-Go build, no Firecracker linkage)
#   - Or Docker for cross-compilation if building on macOS for Linux
#
# Note: The embedded smolvm binary (packages/nexus/cmd/nexus/smolvm-linux-{arch})
# must be staged before building for the embed to work.  If unavailable, the
# binary will still work but will fall back to the system-installed smolvm.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
NEXUS_PKG="$REPO_ROOT/packages/nexus"
RESOURCES_DIR="$REPO_ROOT/packages/nexus-swift/Resources"
ARCH="${1:-both}"

mkdir -p "$RESOURCES_DIR"

build_for_arch() {
    local arch="$1"
    local goos="linux"
    local goarch="$arch"
    local out="$RESOURCES_DIR/nexus-linux-${arch}"

    echo "==> Building nexus for ${goos}/${goarch}…"
    (
        cd "$NEXUS_PKG"
        CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
            -trimpath \
            -ldflags="-s -w" \
            -o "$out" \
            ./cmd/nexus
    )
    chmod +x "$out"
    local size
    size=$(du -sh "$out" | cut -f1)
    echo "    OK: $out ($size)"
}

case "$ARCH" in
    amd64)  build_for_arch amd64 ;;
    arm64)  build_for_arch arm64 ;;
    both)   build_for_arch amd64; build_for_arch arm64 ;;
    *)
        echo "Usage: $0 [amd64|arm64|both]" >&2
        exit 1
        ;;
esac

echo ""
echo "Staged Linux nexus binaries to: $RESOURCES_DIR"
echo "Run 'xcodegen generate' in packages/nexus-swift/ to pick them up."
