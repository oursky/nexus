#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

task firecracker:update
# Build embedded agent + tap-helper binaries before compiling the nexus binary.
# The //go:embed directives in embed_agent_*.go / embed_tap_helper_*.go require
# these binaries to exist at compile time.
cd packages/nexus && go generate ./cmd/nexus/ && cd -
(
  cd packages/nexus
  go build ./...
  go vet ./...
)
./scripts/ci/nexus-e2e.sh
