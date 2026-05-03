#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

# Build embedded guest-agent binary before compiling on Linux.
if [[ "$(uname -s)" == "Linux" ]]; then
  cd packages/nexus && go generate ./cmd/nexus/ && cd -
fi

cd "$ROOT/packages/nexus"

# Some self-hosted runners have incomplete Go toolchains missing 'covdata'.
# covdata is only needed for packages without tests; run coverage only on
# packages that have test files to avoid the tooling error.
PACKAGES_WITH_TESTS=$(go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' ./... | tr '\n' ' ')

if [ -n "$PACKAGES_WITH_TESTS" ]; then
  go test -covermode=atomic -coverprofile=coverage.out $PACKAGES_WITH_TESTS
  go tool cover -func=coverage.out | tail -n 1
else
  echo "No packages with tests found"
  exit 1
fi
