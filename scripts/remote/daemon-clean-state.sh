#!/usr/bin/env bash
# scripts/remote/daemon-clean-state.sh
# Normalize remote runtime state by removing stale libkrun workspace VMs and
# lingering processes before restarting the daemon.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN='$HOME/.local/bin/nexus'] [REMOTE_PORT=7777] \
#        [DRY_RUN=1] scripts/remote/daemon-clean-state.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus}"
REMOTE_PORT="${REMOTE_PORT:-7777}"
DRY_RUN="${DRY_RUN:-0}"

if ! command -v ssh >/dev/null 2>&1; then
  echo "ssh not found" >&2
  exit 1
fi

run_remote() {
  ssh "$REMOTE_HOST" REMOTE_BIN="$REMOTE_BIN" REMOTE_PORT="$REMOTE_PORT" DRY_RUN="$DRY_RUN" bash -s <<'REMOTE_SCRIPT'
    set -euo pipefail

    remote_bin="$REMOTE_BIN"
    remote_port="$REMOTE_PORT"
    dry_run="${DRY_RUN:-0}"

    remote_bin="${remote_bin/#\~/$HOME}"

    state_root="${XDG_STATE_HOME:-$HOME/.local/state}/nexus"
    data_root="${XDG_DATA_HOME:-$HOME/.local/share}/nexus"

    state_libkrun="$state_root/libkrun-vms"
    state_workspaces="$state_root/workspaces"
    socket_path="$state_root/nexusd.sock"

    run() {
      if [ "$dry_run" = "1" ]; then
        echo "[dry-run] $*"
      else
        eval "$1"
      fi
    }

    run "echo \"Cleaning remote nexus runtime state on $(hostname)\""
    run "\"$remote_bin\" daemon stop 2>/dev/null || true"
    run "lsof -ti tcp:${remote_port} 2>/dev/null | xargs -r kill -9 2>/dev/null || true"

    # Kill stale VM processes.
    run "pkill -f 'nexus-guest-agent' || true"
    run "pkill -f 'nexus-libkrun-vm' || true"
    run "pkill -f 'passt' || true"

    # Remove stale workspace/runtime state.
    run "mkdir -p \"$state_libkrun\" \"$state_workspaces\" \"$data_root\""
    run "rm -rf \"${state_libkrun}\"/* \"${state_workspaces}\"/* \"${socket_path}\""
    run "find \"${state_root}\" -maxdepth 2 -type d -name 'ws-*' -print0 | xargs -0 -r rm -rf"

    run "echo \"cleanup complete\""
REMOTE_SCRIPT
}

run_remote
