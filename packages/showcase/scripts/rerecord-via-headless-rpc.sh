#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHOWCASE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$SHOWCASE_DIR/../.." && pwd)"
RPC_HELPER="$REPO_ROOT/scripts/swift/headless-rpc.sh"

RPC_PORT="${NEXUS_HEADLESS_RPC_PORT:-7778}"
RPC_BASE="http://127.0.0.1:${RPC_PORT}"
RPC_CONNECT_TIMEOUT_SECONDS="${NEXUS_RPC_CONNECT_TIMEOUT_SECONDS:-2}"
RPC_MAX_TIME_SECONDS="${NEXUS_RPC_MAX_TIME_SECONDS:-10}"
OUT_DIR="$SHOWCASE_DIR/public/recordings"
OUT_LOG="$OUT_DIR/latest-terminal.log"
OUT_JSON="$OUT_DIR/latest-session.json"
RENDER_OUT="${REMOTION_OUTPUT:-$SHOWCASE_DIR/out/nexus-showcase.mp4}"

# Optional remote host connect/provision inputs.
SSH_TARGET="${NEXUS_SSH_TARGET:-${REMOTE_HOST:-}}"
SSH_PORT="${NEXUS_SSH_PORT:-${REMOTE_PORT:-}}"
SSH_IDENTITY="${NEXUS_SSH_IDENTITY:-}"

remote_user="${SSH_TARGET%%@*}"
if [[ "$remote_user" == "$SSH_TARGET" || -z "$remote_user" ]]; then
  remote_user="newman"
fi

WORKSPACE_NAME="${NEXUS_WORKSPACE_NAME:-showcase-rerecord-$(date +%Y%m%d-%H%M%S)}"
WORKSPACE_REPO="${NEXUS_WORKSPACE_REPO:-/home/${remote_user}/magic/nexus}"
WORKSPACE_REF="${NEXUS_WORKSPACE_REF:-main}"
WORKSPACE_BACKEND="${NEXUS_WORKSPACE_BACKEND:-}"
TERMINAL_CMD="${NEXUS_TERMINAL_CMD:-nexus --version; uname -a; pwd}"
TERMINAL_READ_SECONDS="${NEXUS_TERMINAL_READ_SECONDS:-8}"
WORKSPACE_READY_TIMEOUT_SECONDS="${NEXUS_WORKSPACE_READY_TIMEOUT_SECONDS:-180}"
REMOTION_RENDER="${REMOTION_RENDER:-1}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "✗ Missing required command: $1" >&2
    exit 1
  fi
}

json_extract() {
  local key="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -r "$key"
  else
    python3 -c 'import json,sys; obj=json.load(sys.stdin); expr=sys.argv[1];
parts=expr.strip(".").split(".");
for p in parts:
    obj=obj.get(p) if isinstance(obj,dict) else None
print("" if obj is None else obj)' "$key"
  fi
}

rpc() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl --silent --show-error --fail \
      --connect-timeout "$RPC_CONNECT_TIMEOUT_SECONDS" \
      --max-time "$RPC_MAX_TIME_SECONDS" \
      -X "$method" \
      -H 'Content-Type: application/json' \
      --data "$body" \
      "$RPC_BASE$path"
  else
    curl --silent --show-error --fail \
      --connect-timeout "$RPC_CONNECT_TIMEOUT_SECONDS" \
      --max-time "$RPC_MAX_TIME_SECONDS" \
      -X "$method" \
      "$RPC_BASE$path"
  fi
}

rpc_post_with_retry() {
  local path="$1"
  local body="$2"
  local attempts="${3:-4}"
  local delay="${4:-2}"
  local i
  local response

  for ((i = 1; i <= attempts; i++)); do
    response="$(curl --silent --show-error \
      --connect-timeout "$RPC_CONNECT_TIMEOUT_SECONDS" \
      --max-time "$RPC_MAX_TIME_SECONDS" \
      -X POST \
      -H 'Content-Type: application/json' \
      --data "$body" \
      "$RPC_BASE$path" 2>&1 || true)"

    # Treat common transient daemon startup races as retryable.
    if [[ "$response" == *"no daemon connected"* || "$response" == *"The network connection was lost."* || "$response" == *"not ready"* || "$response" == *"curl:"* ]]; then
      if (( i < attempts )); then
        sleep "$delay"
        continue
      fi
    fi

    printf '%s' "$response"
    return 0
  done

  printf '%s' "$response"
  return 0
}

extract_tab_id() {
  local response="$1"
  local tab
  tab="$(printf '%s' "$response" | json_extract '.tabID' 2>/dev/null || true)"
  if [[ -z "$tab" || "$tab" == "null" ]]; then
    return 1
  fi
  printf '%s' "$tab"
}

open_terminal_with_retry() {
  local workspace_id="$1"
  local attempts="${2:-10}"
  local delay="${3:-2}"
  local i
  local response
  local tab
  local payload

  payload=$(cat <<JSON
{"workspaceID":"$workspace_id","name":"showcase-rerecord"}
JSON
)

  for ((i = 1; i <= attempts; i++)); do
    response="$(curl --silent --show-error \
      --connect-timeout "$RPC_CONNECT_TIMEOUT_SECONDS" \
      --max-time "$RPC_MAX_TIME_SECONDS" \
      -X POST \
      -H 'Content-Type: application/json' \
      --data "$payload" \
      "$RPC_BASE/terminal/open" 2>&1 || true)"

    if tab="$(extract_tab_id "$response")"; then
      printf '%s\n%s' "$tab" "$response"
      return 0
    fi

    # Retry transient guest session races while VM shell is still settling.
    if [[ "$response" == *"connection reset by peer"* || "$response" == *"not ready"* || "$response" == *"guest shell.open"* || "$response" == *"The network connection was lost."* || "$response" == *"curl:"* ]]; then
      if (( i < attempts )); then
        sleep "$delay"
        continue
      fi
    fi

    printf '\n%s' "$response"
    return 0
  done

  printf '\n%s' "$response"
  return 0
}

main() {
  need_cmd curl
  need_cmd pnpm

  mkdir -p "$OUT_DIR" "$(dirname "$RENDER_OUT")"

  "$RPC_HELPER" start

  daemon_status="$(rpc GET /daemon/status || true)"
  has_profile="$(printf '%s' "$daemon_status" | json_extract '.hasProfile' 2>/dev/null || true)"
  client_connected="$(printf '%s' "$daemon_status" | json_extract '.clientConnected' 2>/dev/null || true)"

  if [[ "$client_connected" == "true" ]]; then
    echo "Using existing headless daemon client connection."
  elif [[ -n "$SSH_TARGET" && "$has_profile" != "true" ]]; then
    echo "Connecting daemon profile via headless RPC: $SSH_TARGET"
    payload=$(cat <<JSON
{"sshTarget":"$SSH_TARGET"${SSH_PORT:+,"sshPort":$SSH_PORT}${SSH_IDENTITY:+,"sshIdentity":"$SSH_IDENTITY"}}
JSON
)
    rpc POST /daemon/connect "$payload" >/dev/null
  elif [[ -n "$SSH_TARGET" ]]; then
    echo "Daemon profile exists but is not connected; proceeding without reconnect."
  fi

  run_started_at="$(date +%s)"
  echo "Creating workspace via headless RPC: $WORKSPACE_NAME"
  t_create_start="$(date +%s)"
  create_payload=$(cat <<JSON
{"name":"$WORKSPACE_NAME","repo":"$WORKSPACE_REPO","ref":"$WORKSPACE_REF","backend":"$WORKSPACE_BACKEND"}
JSON
)
  create_resp="$(rpc_post_with_retry /workspace/create "$create_payload" 6 2)"
  t_create_end="$(date +%s)"
  workspace_id="$(printf '%s' "$create_resp" | json_extract '.workspaceID')"

  if [[ -z "$workspace_id" ]]; then
    echo "✗ Failed to read workspaceID from response: $create_resp" >&2
    exit 1
  fi

  t_start_start="$(date +%s)"
  start_resp="$(rpc_post_with_retry /workspace/start "{\"workspaceID\":\"$workspace_id\"}" 6 2)"
  t_start_end="$(date +%s)"
  if [[ "$start_resp" != *"\"ok\":true"* ]]; then
    echo "✗ Failed to start workspace: $start_resp" >&2
    exit 1
  fi

  # workspace.start is async: wait until list reflects a runnable state.
  t_ready_start="$(date +%s)"
  ready=0
  ready_deadline=$((SECONDS + WORKSPACE_READY_TIMEOUT_SECONDS))
  while (( SECONDS < ready_deadline )); do
    list_resp="$(rpc GET /workspace/list)"
    state="$(
      printf '%s' "$list_resp" | python3 -c 'import json,sys
ws=sys.argv[1]
try:
 d=json.load(sys.stdin)
 for item in d.get("workspaces", []):
  if item.get("id")==ws:
   print(item.get("state",""))
   raise SystemExit
 print("")
except Exception:
 print("")' "$workspace_id"
    )"
    if [[ "$state" == "running" || "$state" == "started" ]]; then
      ready=1
      break
    fi
    sleep 2
  done
  t_ready_end="$(date +%s)"
  if [[ "$ready" != "1" ]]; then
    echo "timing: create=$((t_create_end - t_create_start))s start=$((t_start_end - t_start_start))s ready=$((t_ready_end - t_ready_start))s total=$((t_ready_end - run_started_at))s" >&2
    echo "✗ Workspace did not become ready after start (workspaceID=$workspace_id timeout=${WORKSPACE_READY_TIMEOUT_SECONDS}s)" >&2
    exit 1
  fi

  t_open_start="$(date +%s)"
  open_result="$(open_terminal_with_retry "$workspace_id" 10 2)"
  t_open_end="$(date +%s)"
  tab_id="$(printf '%s' "$open_result" | head -n1)"
  open_resp="$(printf '%s' "$open_result" | tail -n +2)"

  if [[ -z "$tab_id" || "$tab_id" == "null" ]]; then
    echo "timing: create=$((t_create_end - t_create_start))s start=$((t_start_end - t_start_start))s ready=$((t_ready_end - t_ready_start))s open=$((t_open_end - t_open_start))s total=$((t_open_end - run_started_at))s" >&2
    echo "✗ Failed to open terminal tab for workspace $workspace_id: $open_resp" >&2
    exit 1
  fi

  rpc POST /terminal/clear "{\"tabID\":\"$tab_id\"}" >/dev/null
  rpc POST /terminal/write "{\"tabID\":\"$tab_id\",\"text\":$(printf '%s' "${TERMINAL_CMD}\n" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')}" >/dev/null

  : > "$OUT_LOG"
  deadline=$((SECONDS + TERMINAL_READ_SECONDS))
  while (( SECONDS < deadline )); do
    read_resp="$(rpc GET "/terminal/read?tabID=$tab_id")"
    chunk="$(printf '%s' "$read_resp" | json_extract '.output')"
    if [[ -n "$chunk" ]]; then
      printf '%s' "$chunk" >> "$OUT_LOG"
    fi
    sleep 1
  done

  cat > "$OUT_JSON" <<JSON
{
  "recordedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "rpcBase": "$RPC_BASE",
  "workspaceID": "$workspace_id",
  "workspaceName": "$WORKSPACE_NAME",
  "tabID": "$tab_id",
  "terminalLog": "public/recordings/$(basename "$OUT_LOG")"
}
JSON

  echo "Saved terminal capture: $OUT_LOG"
  echo "Saved session metadata: $OUT_JSON"
  t_done="$(date +%s)"
  echo "timing: create=$((t_create_end - t_create_start))s start=$((t_start_end - t_start_start))s ready=$((t_ready_end - t_ready_start))s open=$((t_open_end - t_open_start))s total=$((t_done - run_started_at))s"

  if [[ "$REMOTION_RENDER" == "1" ]]; then
    (
      cd "$SHOWCASE_DIR"
      pnpm exec remotion render src/index.ts NexusShowcase "$RENDER_OUT" --overwrite
    )
    echo "Rendered: $RENDER_OUT"
  else
    echo "Skipping remotion render (REMOTION_RENDER=$REMOTION_RENDER)"
  fi
}

main "$@"
