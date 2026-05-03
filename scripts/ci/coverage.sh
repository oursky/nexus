#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

# Build embedded guest-agent binary before compiling on Linux.
if [[ "$(uname -s)" == "Linux" ]]; then
  cd packages/nexus && go generate ./cmd/nexus/ && cd -
fi

cd "$ROOT/packages/nexus"

# Verify the Go toolchain is complete before running coverage.
# Self-hosted runners must have a full Go installation (including covdata).
if ! go tool covdata --help >/dev/null 2>&1; then
  echo "ERROR: Go toolchain is incomplete — 'covdata' tool is missing." >&2
  echo "Install a complete Go distribution on this runner." >&2
  echo "Current Go version: $(go version)" >&2
  echo "GOROOT: $(go env GOROOT)" >&2
  exit 1
fi

go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -n 1
