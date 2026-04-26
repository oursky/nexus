#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
APP_PATH="$REPO_ROOT/packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app"
RPC_PORT="${NEXUS_HEADLESS_RPC_PORT:-7778}"
RPC_BASE="http://127.0.0.1:${RPC_PORT}"
SENTINEL="$HOME/.nexus-headless-rpc"

usage() {
  cat <<USAGE
Usage: $(basename "$0") <start|status|request> [args...]

Commands:
  start [--relaunch] [--wait-seconds N]
      Ensure headless RPC is enabled and NexusApp is running.

  status
      GET /status from headless RPC.

  request <METHOD> <PATH> [JSON_BODY]
      Issue a raw request to headless RPC, e.g.:
      $(basename "$0") request POST /workspace/list '{}'
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

cmd_start() {
  local relaunch="0"
  local wait_seconds="20"

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
    open "$APP_PATH"
  fi

  if ! wait_for_status "$wait_seconds"; then
    echo "✗ Headless RPC did not become ready at $RPC_BASE/status" >&2
    exit 1
  fi

  echo "✓ NexusApp headless RPC ready at $RPC_BASE"
}

cmd_status() {
  rpc_status
  echo
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
    request)
      shift
      cmd_request "$@"
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
