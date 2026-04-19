#!/usr/bin/env bash
set -euo pipefail

# Run NexusUITests against a nexus daemon.
#
# Two modes:
#
#   SSH tunnel mode (default):
#     Starts an isolated daemon on linuxbox:<TEST_DAEMON_PORT> (default 7778),
#     sets up an SSH tunnel so the Mac app connects through it.
#     Uses a separate DB/socket so tests don't pollute the dev daemon state.
#
#   No-tunnel mode (--no-tunnel):
#     Skips SSH daemon lifecycle entirely. Assumes a daemon is already running
#     locally (or in a VM with port-forwarding) on TEST_DAEMON_PORT (default 7777).
#     Sets NEXUS_DAEMON_URL directly so AppState bypasses the SSH tunnel path.
#     Used in CI with a Lima VM.
#
# Usage:
#   bash scripts/test-xcui.sh                  # SSH tunnel mode (all tests)
#   bash scripts/test-xcui.sh --only <TestId>  # run one test
#   bash scripts/test-xcui.sh --no-tunnel      # no-tunnel mode (CI)

ONLY_TESTING="NexusUITests"
NO_TUNNEL=0

# Isolated test daemon configuration (SSH tunnel mode)
TEST_DAEMON_PORT="${TEST_DAEMON_PORT:-7778}"
REMOTE_USER="newman@linuxbox"
REMOTE_NEXUS="/home/newman/magic/bin/nexus"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --only)      shift; ONLY_TESTING="NexusUITests/$1"; shift ;;
    --no-tunnel) NO_TUNNEL=1; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# In no-tunnel mode the daemon is already running locally on the default port.
if [[ "$NO_TUNNEL" -eq 1 ]]; then
  TEST_DAEMON_PORT="${TEST_DAEMON_PORT:-7777}"
fi

# Compute run ID: YYYYMMDD-HHMM-<sha8> (fallback: nosha)
TIMESTAMP=$(date +%Y%m%d-%H%M)
GIT_SHA=$(git rev-parse --short=8 HEAD 2>/dev/null || echo "nosha")
RUN_ID="${TIMESTAMP}-${GIT_SHA}"
export RUN_ID

BUILD_DIR="packages/nexus-swift/.build/xcresults"
RESULT_BUNDLE="${BUILD_DIR}/NexusUITests-${RUN_ID}.xcresult"

# Create all contract directories
mkdir -p "${BUILD_DIR}/reports/${RUN_ID}"
mkdir -p "${BUILD_DIR}/screenshots/${RUN_ID}"
mkdir -p "${BUILD_DIR}/index"

# ── Daemon lifecycle (SSH tunnel mode only) ──────────────────────────────────
if [[ "$NO_TUNNEL" -eq 0 ]]; then
  echo "[test-daemon] Starting isolated daemon on ${REMOTE_USER}:${TEST_DAEMON_PORT}"

  # Kill any existing daemon on the test port first.
  ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
    "pkill -f 'nexus daemon start.*--port ${TEST_DAEMON_PORT}' 2>/dev/null || true; \
     sleep 1" 2>&1 || true

  # Use a run-scoped DB so every run starts with zero state — no leftover
  # projects from prior runs regardless of cleanup timing.
  TEST_DB_PATH="/tmp/nexus-testd-${TEST_DAEMON_PORT}-${RUN_ID}.db"
  TEST_SOCK_PATH="/tmp/nexus-testd-${TEST_DAEMON_PORT}.sock"

  # Also kill the dev daemon on 7777 so the app can't accidentally connect to it.
  # The app's profile points to port 7777; if it's running, the app will connect there
  # instead of via the SSH tunnel to our test daemon at 7778.
  ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
    "pkill -f 'nexus daemon start.*--port 7777' 2>/dev/null || true; \
     echo '[test-daemon] Dev daemon on 7777 killed (if running)'" 2>&1 || true

  # Start fresh isolated daemon (no --token: use the global stored token so fetchRemoteToken() matches)
  ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
    "nohup ${REMOTE_NEXUS} daemon start \
      --network \
      --port ${TEST_DAEMON_PORT} \
      --db ${TEST_DB_PATH} \
      --socket ${TEST_SOCK_PATH} \
      > /tmp/nexus-testd-${TEST_DAEMON_PORT}.log 2>&1 & \
     echo \"PID=\$!\"" 2>&1

  # Wait for daemon to be ready (up to 30s to account for cold start on linuxbox)
  echo "[test-daemon] Waiting for daemon to be ready..."
  DAEMON_READY=0
  for i in $(seq 1 30); do
    if ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
         "curl -sf http://127.0.0.1:${TEST_DAEMON_PORT}/healthz > /dev/null 2>&1"; then
      echo "[test-daemon] Daemon ready after ${i}s"
      DAEMON_READY=1
      break
    fi
    sleep 1
  done

  if [[ "$DAEMON_READY" -eq 0 ]]; then
    echo "[test-daemon] ERROR: daemon failed to start within 30s"
    exit 1
  fi

  cleanup_daemon() {
    echo "[test-daemon] Stopping isolated daemon on ${REMOTE_USER}:${TEST_DAEMON_PORT}"
    ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
      "pkill -f 'nexus daemon start.*--port ${TEST_DAEMON_PORT}' 2>/dev/null || true; \
       rm -f ${TEST_DB_PATH}" 2>&1 || true
    # Restart the dev daemon on 7777 so local development isn't broken
    echo "[test-daemon] Restarting dev daemon on ${REMOTE_USER}:7777"
    ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
      "/home/newman/magic/bin/nexus daemon start --network --port 7777 > /tmp/nexus-devd.log 2>&1 &" 2>&1 || true
  }
  trap cleanup_daemon EXIT
else
  echo "[test-daemon] No-tunnel mode: assuming daemon at 127.0.0.1:${TEST_DAEMON_PORT}"
  cleanup_daemon() { true; }
fi

# Remove any stale bundle at this path (xcodebuild refuses to overwrite)
rm -rf "$RESULT_BUNDLE"

# ── App log streaming ─────────────────────────────────────────────────────────
# Stream com.nexus.NexusApp os_log output to a file alongside the xcresult.
# This captures SSH tunnel stderr, healthz attempts, and DaemonLog push messages.
APP_LOG_FILE="${BUILD_DIR}/reports/${RUN_ID}/app.log"
echo "[log-stream] Streaming app logs → ${APP_LOG_FILE}"
log stream \
  --predicate 'subsystem == "com.nexus.NexusApp"' \
  --level debug \
  --style syslog \
  > "$APP_LOG_FILE" 2>&1 &
LOG_STREAM_PID=$!

stop_log_streams() {
  kill "$LOG_STREAM_PID" 2>/dev/null || true
}

# Also stream the daemon log (SSH tunnel mode: remote tail; no-tunnel: local file if exists)
DAEMON_LOG_FILE="${BUILD_DIR}/reports/${RUN_ID}/daemon.log"
if [[ "$NO_TUNNEL" -eq 0 ]]; then
  echo "[log-stream] Tailing daemon log → ${DAEMON_LOG_FILE}"
  ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$REMOTE_USER" \
    "tail -f /tmp/nexus-testd-${TEST_DAEMON_PORT}.log" \
    > "$DAEMON_LOG_FILE" 2>&1 &
  DAEMON_LOG_PID=$!
  stop_log_streams() {
    kill "$LOG_STREAM_PID" 2>/dev/null || true
    kill "$DAEMON_LOG_PID" 2>/dev/null || true
  }
fi

trap 'stop_log_streams; cleanup_daemon' EXIT

# ── Run tests ────────────────────────────────────────────────────────────────
set +e
# Export env vars for the test process and app via launchEnvironment.
#
# SSH tunnel mode: set NEXUS_DAEMON_URL + NEXUS_DAEMON_PORT + NEXUS_DAEMON_SSH_TARGET
#   so AppState enters tunnel mode (connectRemoteAndLoad path).
#   NEXUS_UI_TEST_DAEMON_URL is the direct HTTP URL used by CreateFlow/Terminal test
#   healthz guards and the legacy env var name those classes read.
#
# No-tunnel mode: set only NEXUS_DAEMON_URL (no PORT) so AppState enters direct
#   bypass mode (load() path). Also set NEXUS_UI_TEST_DAEMON_URL for legacy classes.
if [[ "$NO_TUNNEL" -eq 0 ]]; then
  export NEXUS_DAEMON_URL="ws://127.0.0.1/dummy"
  export NEXUS_DAEMON_SSH_TARGET="${REMOTE_USER}"
  export NEXUS_DAEMON_PORT="${TEST_DAEMON_PORT}"
  # Legacy env var name used by NexusCreateFlowUITests / NexusTerminalUITests
  export NEXUS_UI_TEST_DAEMON_URL="http://127.0.0.1:${TEST_DAEMON_PORT}"
else
  export NEXUS_DAEMON_URL="ws://127.0.0.1:${TEST_DAEMON_PORT}"
  # Legacy env var name used by NexusCreateFlowUITests / NexusTerminalUITests
  export NEXUS_UI_TEST_DAEMON_URL="http://127.0.0.1:${TEST_DAEMON_PORT}"
fi

# Build -only-testing args: one flag per test suite
ONLY_TESTING_ARGS=()
for suite in $ONLY_TESTING; do
  ONLY_TESTING_ARGS+=(-only-testing "$suite")
done

xcodebuild test \
  -project packages/nexus-swift/NexusApp.xcodeproj \
  -scheme NexusApp \
  -destination 'platform=macOS' \
  "${ONLY_TESTING_ARGS[@]}" \
  -resultBundlePath "$RESULT_BUNDLE"
XCODE_EXIT=$?
set -e

scripts/xcresult-report.sh "$RESULT_BUNDLE" "$RUN_ID" || true

echo ""
echo "Result bundle: $RESULT_BUNDLE"
echo "Run ID: $RUN_ID"
echo "Report:     ${BUILD_DIR}/reports/${RUN_ID}/report.md"
echo "App log:    ${APP_LOG_FILE}"
if [[ "$NO_TUNNEL" -eq 0 ]]; then
  echo "Daemon log: ${DAEMON_LOG_FILE}"
fi
echo "Open with:  open $RESULT_BUNDLE"

exit $XCODE_EXIT
