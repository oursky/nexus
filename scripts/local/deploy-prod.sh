#!/usr/bin/env bash
# scripts/local/deploy-prod.sh
# Build the PRODUCTION nexus binary (no dev tag) and install to ~/.local/bin/nexus.
#
# The prod binary uses:
#   - KeychainStore for tokens on macOS (not FileStore)
#   - libkrun VM driver
#   - No debug/dev-only code paths
#
# Usage: [PROD_BIN=~/.local/bin/nexus] scripts/local/deploy-prod.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
PROD_BIN="${PROD_BIN:-$HOME/.local/bin/nexus}"

BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "$SCRIPT_DIR/../.." rev-parse --short HEAD 2>/dev/null || echo dev)"
# Override at link time for eyeball / prod alignment, e.g. NEXUS_BUILD_VERSION=v1.2.3
NEXUS_BUILD_VERSION="${NEXUS_BUILD_VERSION:-prod-${GIT_COMMIT}}"
LDFLAGS="-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Time=${BUILD_TIME} \
         -X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Commit=${GIT_COMMIT} \
         -X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Version=${NEXUS_BUILD_VERSION}"
# Optional prod engine hostname shown in `nexus daemon version`, e.g. NEXUS_DEPLOY_DOMAIN=engine.example.com
if [[ -n "${NEXUS_DEPLOY_DOMAIN:-}" ]]; then
  LDFLAGS+=" -X github.com/oursky/nexus/packages/nexus/internal/buildinfo.DeployDomain=${NEXUS_DEPLOY_DOMAIN}"
fi

echo "Building nexus (prod) for $(go env GOOS)/$(go env GOARCH) (version=${NEXUS_BUILD_VERSION} commit=${GIT_COMMIT} built=${BUILD_TIME})..."
# Ensure embedded agent binary is present (go:embed requires it at build time).
go generate -C "$NEXUS_PKG" ./cmd/nexus/
# Prod build: no dev tag (uses KeychainStore / SecretService, release code paths).
# We intentionally omit -tags libkrun here: the nexus-libkrun-vm binary and
# shared libraries are already installed on this host. The stub build skips
# re-extraction while keeping the libkrun driver fully functional.
# To embed libs for a fresh-host installer, run `task dev:libkrun-vm` first to
# produce cmd/nexus/libkrun-embed.so, then rebuild with -tags libkrun.
go build -C "$NEXUS_PKG" -ldflags "$LDFLAGS" -o ./tmp/nexus-prod ./cmd/nexus

echo "Building pty-host (prod)..."
go build -C "$NEXUS_PKG" -ldflags "$LDFLAGS" -o ./tmp/pty-host-prod ./cmd/pty-host

mkdir -p "$(dirname "$PROD_BIN")"
rm -f "$PROD_BIN"
cp "$NEXUS_PKG/tmp/nexus-prod" "$PROD_BIN"
chmod +x "$PROD_BIN"

# Install pty-host alongside nexus so the daemon can find it.
# Prod daemon shares pty-host with dev (same binary, no build-tag differences).
LOCAL_PTY_HOST="$(dirname "$PROD_BIN")/pty-host"
rm -f "$LOCAL_PTY_HOST"
cp "$NEXUS_PKG/tmp/pty-host-prod" "$LOCAL_PTY_HOST"
chmod +x "$LOCAL_PTY_HOST"

if [ "$(uname -s)" = "Darwin" ]; then
  ENTITLEMENTS="$NEXUS_PKG/nexus.entitlements"
  if [ -f "$ENTITLEMENTS" ]; then
    codesign --sign - --force --entitlements "$ENTITLEMENTS" "$PROD_BIN" 2>/dev/null && \
      echo "Codesigned with hypervisor entitlements" || \
      echo "Warning: codesign failed (hypervisor access may be unavailable)"
  fi
fi

echo "Installed $PROD_BIN"
echo "$($PROD_BIN daemon version)"
