#!/usr/bin/env bash
# On linux/amd64, stage libkrun/passt blobs under cmd/nexus/ (required for
# go:embed in cmd/nexus and for linking cmd/nexus-libkrun-vm).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ "$(go env GOOS)" == "linux" && "$(go env GOARCH)" == "amd64" ]]; then
  bash "$ROOT/scripts/ci/stage-nexus-linux-embeds.sh"
fi

exec "$@"
