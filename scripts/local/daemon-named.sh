#!/usr/bin/env bash
# scripts/local/daemon-named.sh
# Manage multiple named nexus daemon instances on this Linux host.
# Each instance gets its own port, state directory, and data path so they
# never collide — useful for testing multiple scenarios in parallel
# worktree/test scenario and connect the Mac app to it.
#
# Usage:
#   scripts/local/daemon-named.sh start  <name> [--port PORT] [--driver DRIVER] [--data-path PATH]
#   scripts/local/daemon-named.sh stop   <name>
#   scripts/local/daemon-named.sh status <name>
#   scripts/local/daemon-named.sh list
#   scripts/local/daemon-named.sh token  <name>
#   scripts/local/daemon-named.sh logs   <name> [--follow]
#
# Isolation scheme per instance name:
#   Binary:     $LOCAL_BIN  (default ~/.local/bin/nexus-dev, shared binary)
#   Port:       --port (default: auto-assigned from 7780+, or user-specified)
#   State dir:  ~/.local/state-nexus-<name>/nexus/
#   Data path:  ~/.local/share/nexus-<name>/  (process sandbox) or /data/nexus-<name>/ (vm)
#   Socket:     ~/.local/state-nexus-<name>/nexus/nexusd.sock
#
# Connect the Mac app to a named instance:
#   nexus-dev daemon connect <linux-host> --port <port>
#   (or use scripts/remote/mac-test-headless.sh with LOCAL_TUNNEL_PORT=<port>)
set -euo pipefail

NEXUS_BIN="${LOCAL_BIN:-$HOME/.local/bin/nexus-dev}"
BASE_PORT="${BASE_PORT:-7780}"    # First port in the auto-assign pool

# ── helpers ──────────────────────────────────────────────────────────────────

state_dir() { echo "$HOME/.local/state-nexus-${1}/nexus"; }
data_dir()  { echo "$HOME/.local/share/nexus-${1}"; }
pid_file()  { echo "$(state_dir "$1")/daemon.pid"; }

daemon_env() {
  local name="$1"
  XDG_STATE_HOME="$HOME/.local/state-nexus-${name}" \
  XDG_DATA_HOME="$HOME/.local/share/nexus-${name}" \
  HOME="$HOME"
}

run_nexus() {
  local name="$1"; shift
  XDG_STATE_HOME="$HOME/.local/state-nexus-${name}" \
  XDG_DATA_HOME="$HOME/.local/share/nexus-${name}" \
    "$NEXUS_BIN" "$@"
}

auto_port() {
  # Find next unused port starting from BASE_PORT
  local port=$BASE_PORT
  while ss -tlnp 2>/dev/null | grep -q ":${port}[[:space:]]"; do
    port=$((port + 1))
  done
  echo "$port"
}

# ── subcommands ───────────────────────────────────────────────────────────────

cmd_start() {
  local name="${1:?Usage: daemon-named.sh start <name> [--port PORT] [--driver DRIVER]}"
  shift
  local port="" driver="sandbox"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --port)    port="$2";   shift 2 ;;
      --driver)  driver="$2"; shift 2 ;;
      *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
  done

  [[ -z "$port" ]] && port=$(auto_port)

  local sdir; sdir=$(state_dir "$name")
  local ddir; ddir=$(data_dir "$name")
  mkdir -p "$sdir" "$ddir"

  # workdir-root: pass explicitly so libkrun VM images land under the instance's
  # own data dir rather than falling through to the shared /data/nexus XFS path.
  local workdir_root="${ddir}/libkrun-vms"

  echo "Starting nexus daemon '${name}' on port ${port} (driver=${driver}) ..."
  echo "  state: ${sdir}"
  echo "  data:  ${ddir}"

  run_nexus "$name" daemon start \
    --port "$port" \
    --driver "$driver" \
    --workdir-root "$workdir_root" \
    --foreground=false

  echo "✓ Daemon '${name}' started."
  echo ""
  echo "Connect the Mac app:"
  echo "  nexus-dev daemon connect <this-linux-host> --port ${port}"
  echo ""
  echo "Or use the headless RPC tunnel:"
  echo "  LOCAL_TUNNEL_PORT=$((port + 1000)) MAC_HOST=newman@minion \\"
  echo "    scripts/remote/mac-test-headless.sh"
}

cmd_stop() {
  local name="${1:?Usage: daemon-named.sh stop <name>}"
  echo "Stopping nexus daemon '${name}' ..."
  run_nexus "$name" daemon stop 2>/dev/null || true
  echo "✓ Daemon '${name}' stopped."
}

cmd_status() {
  local name="${1:?Usage: daemon-named.sh status <name>}"
  run_nexus "$name" daemon status
}

cmd_token() {
  local name="${1:?Usage: daemon-named.sh token <name>}"
  run_nexus "$name" daemon token
}

cmd_logs() {
  local name="${1:?Usage: daemon-named.sh logs <name> [--follow]}"
  shift
  local follow=false
  [[ "${1:-}" == "--follow" || "${1:-}" == "-f" ]] && follow=true

  local log_file; log_file="$(state_dir "$name")/daemon.log"
  if [[ ! -f "$log_file" ]]; then
    echo "No log file found at: $log_file" >&2
    exit 1
  fi

  if $follow; then
    tail -f "$log_file"
  else
    tail -100 "$log_file"
  fi
}

cmd_list() {
  echo "Named nexus daemon instances:"
  echo ""
  local found=false
  for sdir in "$HOME"/.local/state-nexus-*/nexus; do
    [[ -d "$sdir" ]] || continue
    local name
    name=$(basename "$(dirname "$sdir")" | sed 's/^state-nexus-//')
    local pidfile; pidfile=$(pid_file "$name")
    local status="stopped"
    if [[ -f "$pidfile" ]]; then
      local pid; pid=$(cat "$pidfile")
      if kill -0 "$pid" 2>/dev/null; then
        status="running (pid=${pid})"
      else
        status="stopped (stale pid=${pid})"
      fi
    fi
    # Try to get port from daemon status
    local port="unknown"
    port=$(XDG_STATE_HOME="$HOME/.local/state-nexus-${name}" \
           XDG_DATA_HOME="$HOME/.local/share/nexus-${name}" \
           "$NEXUS_BIN" daemon status --json 2>/dev/null \
           | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('port','?'))" 2>/dev/null || echo "?")

    printf "  %-20s  port=%-6s  %s\n" "$name" "$port" "$status"
    found=true
  done
  $found || echo "  (none)"
}

# ── dispatch ──────────────────────────────────────────────────────────────────

COMMAND="${1:-help}"
shift 2>/dev/null || true

case "$COMMAND" in
  start)  cmd_start  "$@" ;;
  stop)   cmd_stop   "$@" ;;
  status) cmd_status "$@" ;;
  token)  cmd_token  "$@" ;;
  logs)   cmd_logs   "$@" ;;
  list)   cmd_list   ;;
  help|--help|-h)
    sed -n '2,/^set -/p' "$0" | grep '^#' | sed 's/^# \?//'
    ;;
  *)
    echo "Unknown command: ${COMMAND}" >&2
    echo "Run with --help for usage." >&2
    exit 1
    ;;
esac
