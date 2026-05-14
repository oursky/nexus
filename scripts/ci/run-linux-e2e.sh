#!/usr/bin/env bash
# Run a single Linux E2E test shard (libkrun driver).
# Configuration is passed via environment variables so the YAML step stays
# a single-line entrypoint.
#
# Required env vars (set from matrix in the caller):
#   GO_P          – -p value (test binary parallelism)
#   GO_PARALLEL   – -parallel value (within a test binary)
#   GO_RUN        – -run filter (may be empty)
#   GO_TIMEOUT    – -timeout value (e.g. 8m)
#   GO_PACKAGES   – space-separated list of ./test/e2e/... packages
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT/packages/nexus"

go_run_args=()
if [[ -n "${GO_RUN:-}" ]]; then
  go_run_args=(-run "$GO_RUN")
fi

# Read space-separated package list into an array for safe expansion.
read -ra PACKAGES <<< "${GO_PACKAGES}"

# sudo -E preserves the environment; explicitly re-export the vars that sudo
# may strip even with -E, and vars set in previous steps via GITHUB_ENV.
sudo -E env \
  "PATH=$PATH" \
  "HOME=$HOME" \
  "NEXUS_PASST_PATH=${NEXUS_PASST_PATH:-}" \
  "NEXUS_CROSS_BINARY_DIR=${NEXUS_CROSS_BINARY_DIR:-}" \
  "NEXUS_E2E_BINARY=${NEXUS_E2E_BINARY:-}" \
  "NEXUS_E2E_REMOTE_PROFILE=${NEXUS_E2E_REMOTE_PROFILE:-}" \
  "NEXUS_E2E_SSH_IDENTITY=${NEXUS_E2E_SSH_IDENTITY:-}" \
  "SSH_AUTH_SOCK=${SSH_AUTH_SOCK:-}" \
  go test -tags e2e -count=1 \
  -timeout="${GO_TIMEOUT:-8m}" -v \
  -p "${GO_P:-2}" -parallel "${GO_PARALLEL:-2}" \
  "${go_run_args[@]}" \
  "${PACKAGES[@]}"
