#!/usr/bin/env bash
# scripts/remote/e2e-shell-workflow.sh
# End-to-end remote CLI flow:
#   1) clean state
#   2) redeploy linux binary
#   3) restart daemon
#   4) connect + workspace start
#   5) run an interactive workspace shell smoke test
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"
REMOTE_PORT="${REMOTE_PORT:-7777}"
WORKSPACE_ID="${WORKSPACE_ID:-${WORKSPACE_NAME:-}}"
SKIP_DAEMON_STOP="${SKIP_DAEMON_STOP:-0}"

if [ -z "$WORKSPACE_ID" ]; then
  echo "WORKSPACE_ID (or WORKSPACE_NAME) is required."
  echo "Example: task remote:e2e-shell WORKSPACE_ID=ws-... SKIP_DAEMON_STOP=1"
  exit 1
fi

cleanup() {
  if [ "${SKIP_DAEMON_STOP}" != "1" ]; then
    ssh "$REMOTE_HOST" "$REMOTE_BIN daemon stop 2>/dev/null || true"
  fi
}

trap cleanup EXIT

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_DIR="$REPO_ROOT/packages/nexus"
LOCAL_BIN="$NEXUS_DIR/tmp/nexus-local-e2e"
LOCAL_BIN_LOG_DIR="$(dirname "$LOCAL_BIN")"
SHELL_EXPECT_LOG="$LOCAL_BIN_LOG_DIR/workspace-shell-interactive.log"

mkdir -p "$LOCAL_BIN_LOG_DIR"

echo "[1/5] Cleaning remote state..."
echo "Skipping explicit remote cleanup (daemon startup handles stale instance reconciliation)."

echo "[2/5] Deploying linux binary..."
"$SCRIPT_DIR/deploy.sh"

echo "[3/5] Restarting remote daemon..."
"$SCRIPT_DIR/daemon-restart.sh"

echo "[4/5] Building local nexus CLI used for this verification..."
go build -C "$NEXUS_DIR" -o "$LOCAL_BIN" ./cmd/nexus

if [ ! -x "$LOCAL_BIN" ]; then
  echo "Expected local CLI binary at $LOCAL_BIN but it was not produced." >&2
  exit 1
fi

echo "[5/5] Connecting, starting workspace, and testing workspace shell..."
"$LOCAL_BIN" daemon connect "$REMOTE_HOST" --port "$REMOTE_PORT"
"$LOCAL_BIN" workspace start "$WORKSPACE_ID"

if ! command -v expect >/dev/null 2>&1; then
  echo "expect is required for interactive workspace shell verification."
  exit 1
fi

echo "Running interactive workspace shell smoke (mandatory)..."
if ! WORKSPACE_ID="$WORKSPACE_ID" LOCAL_BIN="$LOCAL_BIN" /usr/bin/expect >"$SHELL_EXPECT_LOG" 2>&1 <<'EOF'
set timeout 8
set workspace_id $env(WORKSPACE_ID)
set local_binary $env(LOCAL_BIN)

spawn $local_binary workspace shell $workspace_id

send "pwd\r"
expect {
  -re "/workspace(\r)?\n" {}
  timeout {
    puts "timed out waiting for pwd output"
    exit 1
  }
  eof {
    puts "shell exited before pwd output"
    exit 1
  }
}

send "exit\r"
expect {
  eof { exit 0 }
  timeout {
    puts "timed out waiting for shell exit"
    exit 1
  }
}
EOF
then
  echo "Interactive workspace shell smoke failed."
  echo "Last 200 lines of $SHELL_EXPECT_LOG:"
  tail -n 200 "$SHELL_EXPECT_LOG" || true
  exit 1
fi

echo "E2E flow complete."
