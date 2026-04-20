#!/usr/bin/env bash
# Integration tests under packages/nexus/test/e2e (build tag: e2e).
# On Linux CI, sources e2e-ssh-bootstrap.sh so CLI tests use profile + SSH tunnel like a remote host.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT/packages/nexus"

if [[ "${CI:-}" == "true" ]] && [[ "$(uname -s)" == "Linux" ]] && [[ -f "$ROOT/scripts/ci/e2e-ssh-bootstrap.sh" ]]; then
  # shellcheck source=/dev/null
  source "$ROOT/scripts/ci/e2e-ssh-bootstrap.sh"
fi

if [[ "${CI:-}" == "true" ]] && [[ "$(uname -s)" == "Linux" ]] && [[ "${NEXUS_E2E_REMOTE_PROFILE:-}" != "1" ]]; then
  echo "nexus-e2e: fatal: Linux CI must set NEXUS_E2E_REMOTE_PROFILE=1 (e2e-ssh-bootstrap failed or was skipped)" >&2
  exit 1
fi

# CI_E2E_BENCH: temporarily run only TestNodeInfo to time the Firecracker
# daemon start path end-to-end before expanding to the full suite.
go test -tags e2e -count=1 -timeout=5m -v -run TestNodeInfo ./test/e2e/daemon/
