#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
APP_PATH="$REPO_ROOT/packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app"

if [ ! -d "$APP_PATH" ]; then
  echo "✗ App bundle not found at $APP_PATH"
  echo "  Run: task dev:swift  (or: scripts/swift/build.sh)"
  exit 1
fi

echo "Opening NexusApp..."
# Kill any running instance first so we get a clean relaunch
pkill -x NexusApp 2>/dev/null || true
sleep 0.5
open "$APP_PATH"
echo "✓ NexusApp launched"
