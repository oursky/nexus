#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

# Ensure embedded binaries are present before compiling on Linux.
if [[ "$(uname -s)" == "Linux" ]]; then
  task firecracker:update
fi

cd "$ROOT/packages/nexus"
go test ./... -covermode=atomic -coverprofile=coverage.out
go tool cover -func=coverage.out | tail -n 1
