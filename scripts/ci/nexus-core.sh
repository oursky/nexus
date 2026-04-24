#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

# Build embedded guest-agent binary before compiling the nexus binary.
# The //go:embed directives in embed_agent_*.go require this binary at compile time.
cd packages/nexus && go generate ./cmd/nexus/ && cd -
(
  cd packages/nexus
  go build ./...
  go vet ./...
)
./scripts/ci/nexus-e2e.sh
