#!/usr/bin/env bash
# Prepare loopback SSH so the nexus CLI can use the same path as production:
# profile + ssh -L tunnel + WebSocket (no NEXUS_E2E_DAEMON_WEBSOCKET).
#
# Intended for Linux CI (e.g. GitHub Actions). On macOS or without sudo, skip.
# Source this file so exports apply to the current shell:
#   source scripts/ci/e2e-ssh-bootstrap.sh
set -euo pipefail

if [[ "${NEXUS_E2E_SKIP_SSH_BOOTSTRAP:-}" == "1" ]]; then
  echo "e2e-ssh-bootstrap: skipped (NEXUS_E2E_SKIP_SSH_BOOTSTRAP=1)"
  return 0 2>/dev/null || exit 0
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "e2e-ssh-bootstrap: skip (not Linux — use direct WebSocket e2e locally)"
  return 0 2>/dev/null || exit 0
fi

if ! command -v sudo >/dev/null 2>&1; then
  echo "e2e-ssh-bootstrap: no sudo; skipping"
  return 0 2>/dev/null || exit 0
fi

export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -qq
sudo apt-get install -y openssh-server

mkdir -p "${HOME}/.ssh"
chmod 700 "${HOME}/.ssh"
KEY="${NEXUS_E2E_SSH_IDENTITY:-/tmp/nexus-e2e-ci-ed25519}"
if [[ ! -f "$KEY" ]]; then
  ssh-keygen -t ed25519 -f "$KEY" -N "" -q
fi
touch "${HOME}/.ssh/authorized_keys"
chmod 600 "${HOME}/.ssh/authorized_keys"
if ! grep -qF "$(cat "$KEY.pub")" "${HOME}/.ssh/authorized_keys" 2>/dev/null; then
  cat "$KEY.pub" >> "${HOME}/.ssh/authorized_keys"
fi

sudo mkdir -p /run/sshd
sudo service ssh start 2>/dev/null || sudo systemctl start ssh 2>/dev/null || true

U="${USER:-runner}"
H="${U}@127.0.0.1"
if ! ssh -i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes "$H" 'echo e2e-ssh-ok' | grep -q e2e-ssh-ok; then
  echo "e2e-ssh-bootstrap: loopback ssh test failed" >&2
  exit 1
fi

export NEXUS_E2E_REMOTE_PROFILE=1
export NEXUS_E2E_SSH_IDENTITY="$KEY"
export NEXUS_E2E_SSH_HOST="$H"
export NEXUS_E2E_SSH_PORT="${NEXUS_E2E_SSH_PORT:-22}"

echo "e2e-ssh-bootstrap: OK (REMOTE_PROFILE=1 identity=$KEY host=$H)"

if [[ -n "${GITHUB_ENV:-}" ]]; then
  {
    echo "NEXUS_E2E_REMOTE_PROFILE=1"
    echo "NEXUS_E2E_SSH_IDENTITY=$KEY"
    echo "NEXUS_E2E_SSH_HOST=$H"
    echo "NEXUS_E2E_SSH_PORT=${NEXUS_E2E_SSH_PORT}"
  } >>"$GITHUB_ENV"
fi
