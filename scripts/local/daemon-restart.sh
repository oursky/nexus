#!/usr/bin/env bash
# scripts/local/daemon-restart.sh
# Stop and start the nexus daemon on this local Linux host.
#
# Dev/prod isolation: defaults keep the dev daemon state separate from any
# production daemon on the same host.
#
# Usage: [LOCAL_BIN=~/.local/bin/nexus-dev] \
#          [LOCAL_PORT=7778] \
#          [LOCAL_XDG_STATE_HOME=~/.local/state-dev] \
#          scripts/local/daemon-restart.sh
set -euo pipefail

LOCAL_BIN="${LOCAL_BIN:-$HOME/.local/bin/nexus-dev}"
LOCAL_PORT="${LOCAL_PORT:-7778}"
LOCAL_XDG_STATE_HOME="${LOCAL_XDG_STATE_HOME:-$HOME/.local/state-dev}"
LOCAL_XDG_DATA_HOME="${LOCAL_XDG_DATA_HOME:-$HOME/.local/share-dev}"
LOCAL_WORKDIR_ROOT="${LOCAL_WORKDIR_ROOT:-/data/nexus/nexus-dev}"

echo "Stopping dev daemon (bin=${LOCAL_BIN}, port=${LOCAL_PORT})..."
XDG_STATE_HOME="${LOCAL_XDG_STATE_HOME}" \
XDG_DATA_HOME="${LOCAL_XDG_DATA_HOME}" \
  "${LOCAL_BIN}" daemon stop 2>/dev/null || true

echo "Starting dev daemon..."
XDG_STATE_HOME="${LOCAL_XDG_STATE_HOME}" \
XDG_DATA_HOME="${LOCAL_XDG_DATA_HOME}" \
  "${LOCAL_BIN}" daemon start \
    --port "${LOCAL_PORT}" \
    --workdir-root "${LOCAL_WORKDIR_ROOT}"

echo ""
echo "Local binary version:"
XDG_STATE_HOME="${LOCAL_XDG_STATE_HOME}" \
XDG_DATA_HOME="${LOCAL_XDG_DATA_HOME}" \
  "${LOCAL_BIN}" daemon version

echo "Dev daemon restarted (state: ${LOCAL_XDG_STATE_HOME}/nexus, data: ${LOCAL_XDG_DATA_HOME}/nexus, workdir: ${LOCAL_WORKDIR_ROOT}, port: ${LOCAL_PORT})."
