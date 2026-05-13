#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
APP_PATH="$REPO_ROOT/packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app"
# Default port matches the Debug build (#if DEBUG in HeadlessRPCServer.swift).
# For the Release/TestFlight build, set NEXUS_HEADLESS_RPC_PORT=7779.
RPC_PORT="${NEXUS_HEADLESS_RPC_PORT:-7778}"
RPC_BASE="http://127.0.0.1:${RPC_PORT}"
SENTINEL="$HOME/.nexus-headless-rpc"

usage() {
  cat <<USAGE
Usage: $(basename "$0") <start|status|request> [args...]

Commands:
  start [--relaunch] [--wait-seconds N] [--wait-connected]
      Ensure headless RPC is enabled and NexusApp is running.
      With --wait-connected, also wait until daemon connection is ready.

  status
      GET /status from headless RPC.

  wait-connected [--wait-seconds N]
      Wait until /status reports a connected WebSocket daemon client.

  request <METHOD> <PATH> [JSON_BODY]
      Issue a raw request to headless RPC, e.g.:
      $(basename "$0") request POST /workspace/list '{}'

  project-list
      GET /project/list

  project-delete <PROJECT_ID>
      POST /project/delete

  project-delete-by-name <NAME> [--all]
      POST /project/delete-by-name
USAGE
}

require_app_bundle() {
  if [[ ! -d "$APP_PATH" ]]; then
    echo "✗ NexusApp bundle not found: $APP_PATH" >&2
    echo "  Run: scripts/swift/build.sh" >&2
    exit 1
  fi
}

rpc_status() {
  curl --silent --show-error --fail "$RPC_BASE/status"
}

wait_for_status() {
  local wait_seconds="$1"
  local deadline=$((SECONDS + wait_seconds))
  while (( SECONDS < deadline )); do
    if rpc_status >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_connected() {
  local wait_seconds="$1"
  local deadline=$((SECONDS + wait_seconds))
  while (( SECONDS < deadline )); do
    local status
    status="$(rpc_status 2>/dev/null || true)"
    if [[ "$status" == *'"connectionState":"connected"'* ]] && [[ "$status" == *'"clientType":"WebSocketDaemonClient"'* ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

cmd_start() {
  local relaunch="0"
  local wait_seconds="20"
  local wait_connected="0"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --relaunch)
        relaunch="1"
        shift
        ;;
      --wait-seconds)
        wait_seconds="$2"
        shift 2
        ;;
      --wait-connected)
        wait_connected="1"
        shift
        ;;
      *)
        echo "Unknown option for start: $1" >&2
        exit 1
        ;;
    esac
  done

  require_app_bundle

  # Enable RPC via sentinel for app launches where env vars do not propagate.
  touch "$SENTINEL"

  if [[ "$relaunch" == "1" ]]; then
    pkill -x NexusApp 2>/dev/null || true
    sleep 0.5
  fi

  if ! pgrep -x NexusApp >/dev/null 2>&1; then
    # Strip SSH agent env vars so the debug app behaves like a sandboxed
    # TestFlight install (no inherited launchd ssh-agent).
    env -u SSH_AUTH_SOCK -u SSH_AGENT_PID open "$APP_PATH"
  fi

  if ! wait_for_status "$wait_seconds"; then
    echo "✗ Headless RPC did not become ready at $RPC_BASE/status" >&2
    exit 1
  fi

  if [[ "$wait_connected" == "1" ]]; then
    if ! wait_for_connected "$wait_seconds"; then
      echo "✗ Daemon did not become connected within ${wait_seconds}s" >&2
      rpc_status || true
      exit 1
    fi
    echo "✓ NexusApp daemon connected"
  fi

  echo "✓ NexusApp headless RPC ready at $RPC_BASE"
}

cmd_status() {
  rpc_status
  echo
}

cmd_wait_connected() {
  local wait_seconds="30"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --wait-seconds)
        wait_seconds="$2"
        shift 2
        ;;
      *)
        echo "Unknown option for wait-connected: $1" >&2
        exit 1
        ;;
    esac
  done

  if ! wait_for_connected "$wait_seconds"; then
    echo "✗ Daemon did not become connected within ${wait_seconds}s" >&2
    rpc_status || true
    exit 1
  fi
  echo "✓ NexusApp daemon connected"
}

cmd_request() {
  if [[ $# -lt 2 ]]; then
    echo "request requires METHOD and PATH" >&2
    exit 1
  fi
  local method="$1"
  local path="$2"
  local body="${3:-}"

  if [[ -n "$body" ]]; then
    curl --silent --show-error --fail \
      -X "$method" \
      -H 'Content-Type: application/json' \
      --data "$body" \
      "$RPC_BASE$path"
  else
    curl --silent --show-error --fail \
      -X "$method" \
      "$RPC_BASE$path"
  fi
  echo
}

cmd_project_list() {
  curl --silent --show-error --fail "$RPC_BASE/project/list"
  echo
}

cmd_project_delete() {
  if [[ $# -lt 1 ]]; then
    echo "project-delete requires PROJECT_ID" >&2
    exit 1
  fi
  local project_id="$1"
  curl --silent --show-error --fail \
    -X POST \
    -H 'Content-Type: application/json' \
    --data "{\"projectID\":\"$project_id\"}" \
    "$RPC_BASE/project/delete"
  echo
}

cmd_project_delete_by_name() {
  if [[ $# -lt 1 ]]; then
    echo "project-delete-by-name requires NAME" >&2
    exit 1
  fi
  local name="$1"
  shift || true
  local all=false
  if [[ "${1:-}" == "--all" ]]; then
    all=true
  fi
  curl --silent --show-error --fail \
    -X POST \
    -H 'Content-Type: application/json' \
    --data "{\"name\":\"$name\",\"all\":$all}" \
    "$RPC_BASE/project/delete-by-name"
  echo
}

main() {
  local cmd="${1:-}"
  case "$cmd" in
    start)
      shift
      cmd_start "$@"
      ;;
    status)
      shift
      cmd_status "$@"
      ;;
    wait-connected)
      shift
      cmd_wait_connected "$@"
      ;;
    request)
      shift
      cmd_request "$@"
      ;;
    project-list)
      shift
      cmd_project_list "$@"
      ;;
    project-delete)
      shift
      cmd_project_delete "$@"
      ;;
    project-delete-by-name)
      shift
      cmd_project_delete_by_name "$@"
      ;;
    ""|-h|--help|help)
      usage
      ;;
    *)
      echo "Unknown command: $cmd" >&2
      usage
      exit 1
      ;;
  esac
}

main "$@"
