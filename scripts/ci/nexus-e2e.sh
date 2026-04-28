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

# -short skips tests that boot libkrun VMs (3-5 min each).
# NEXUS_E2E_SHORT=0 disables -short so full VM lifecycle tests run.
short_flag="-short"
if [[ "${NEXUS_E2E_SHORT:-1}" == "0" ]]; then
  short_flag=""
fi

go test -tags e2e -count=1 -timeout=30m ${short_flag} -v ./test/e2e/...
