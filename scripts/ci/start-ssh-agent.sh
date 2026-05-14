#!/usr/bin/env bash
# Start an SSH agent and add the E2E identity key.
# Exports SSH_AUTH_SOCK and SSH_AGENT_PID to GITHUB_ENV for subsequent steps.
set -euo pipefail

# shellcheck disable=SC2046
eval $(ssh-agent -s)

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "SSH_AUTH_SOCK=$SSH_AUTH_SOCK" >> "$GITHUB_ENV"
  echo "SSH_AGENT_PID=$SSH_AGENT_PID" >> "$GITHUB_ENV"
fi

KEY="${NEXUS_E2E_SSH_IDENTITY:-/tmp/nexus-e2e-ci-ed25519}"
if [[ -f "$KEY" ]]; then
  ssh-add "$KEY"
fi
