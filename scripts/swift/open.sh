#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
APP_PATH="$REPO_ROOT/packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app"
HEADLESS_HELPER="$SCRIPT_DIR/headless-rpc.sh"

if [ ! -d "$APP_PATH" ]; then
  echo "✗ App bundle not found at $APP_PATH"
  echo "  Run: task dev:swift  (or: scripts/swift/build.sh)"
  exit 1
fi

if [[ "${NEXUS_HEADLESS_RPC_ENABLE:-1}" == "1" && -x "$HEADLESS_HELPER" ]]; then
  "$HEADLESS_HELPER" start --relaunch
else
  echo "Opening NexusApp..."
  # Kill any running instance first so we get a clean relaunch.
  pkill -x NexusApp 2>/dev/null || true
  sleep 0.5
  # Strip SSH agent env vars so the debug app behaves like a TestFlight install:
  # no inherited launchd ssh-agent, must use SandboxSSHAgent + bookmarks.
  env -u SSH_AUTH_SOCK -u SSH_AGENT_PID open "$APP_PATH"
  echo "✓ NexusApp launched (SSH_AUTH_SOCK/SSH_AGENT_PID unset — simulates App Sandbox)"
fi
