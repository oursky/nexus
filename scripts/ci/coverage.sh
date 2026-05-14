#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

# Build embedded guest-agent binary before compiling on Linux.
if [[ "$(uname -s)" == "Linux" ]]; then
  cd packages/nexus && go generate ./cmd/nexus/ && cd -
fi

cd "$ROOT/packages/nexus"

bash "$ROOT/scripts/ci/with-linux-nexus-cgo.sh" \
  go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -n 1
