#!/usr/bin/env bash
# scripts/local/daemon-restart-prod.sh
# Stop and start the PRODUCTION nexus daemon on this local Linux host.
#
# The prod daemon is isolated from the dev daemon:
#   binary  ~/.local/bin/nexus        (no dev build tag)
#   port    7777
#   state   ~/.local/state/nexus/
#   data    ~/.local/share/nexus/
#   VM workspace state defaults to /data/nexus/default when /data/nexus exists
#
# Usage: [PROD_BIN=~/.local/bin/nexus] \
#          [PROD_PORT=7777] \
#          [PROD_XDG_STATE_HOME=~/.local/state] \
#          scripts/local/daemon-restart-prod.sh
set -euo pipefail

PROD_BIN="${PROD_BIN:-$HOME/.local/bin/nexus}"
PROD_PORT="${PROD_PORT:-7777}"
PROD_XDG_STATE_HOME="${PROD_XDG_STATE_HOME:-$HOME/.local/state}"
PROD_XDG_DATA_HOME="${PROD_XDG_DATA_HOME:-$HOME/.local/share}"

echo "Stopping prod daemon (bin=${PROD_BIN}, port=${PROD_PORT})..."
XDG_STATE_HOME="${PROD_XDG_STATE_HOME}" \
XDG_DATA_HOME="${PROD_XDG_DATA_HOME}" \
  "${PROD_BIN}" daemon stop 2>/dev/null || true

echo "Starting prod daemon..."
# Integration/E2E shells often export NEXUS_E2E_DAEMON_WEBSOCKET (+ stale token env).
# That forces every CLI invocation onto the wrong loopback port/token versus prod :7777.
unset NEXUS_E2E_DAEMON_WEBSOCKET || true
XDG_STATE_HOME="${PROD_XDG_STATE_HOME}" \
XDG_DATA_HOME="${PROD_XDG_DATA_HOME}" \
  "${PROD_BIN}" daemon start \
    --port "${PROD_PORT}"

echo ""
echo "Prod daemon version:"
XDG_STATE_HOME="${PROD_XDG_STATE_HOME}" \
XDG_DATA_HOME="${PROD_XDG_DATA_HOME}" \
  "${PROD_BIN}" daemon version

echo ""
echo "Prod daemon restarted (state: ${PROD_XDG_STATE_HOME}/nexus, data: ${PROD_XDG_DATA_HOME}/nexus, port: ${PROD_PORT})."
