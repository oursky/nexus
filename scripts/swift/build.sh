#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SWIFT_DIR="$REPO_ROOT/packages/nexus-swift"

echo "Building NexusApp..."
LOG=$(mktemp)
set +e
xcodebuild \
  -scheme NexusApp \
  -project "$SWIFT_DIR/NexusApp.xcodeproj" \
  -configuration Debug \
  -derivedDataPath "$SWIFT_DIR/.build/xcbuild" \
  build > "$LOG" 2>&1
EXIT=$?
set -e

# Always show errors and the final status line
grep -E "^.*(error:|BUILD (SUCCEEDED|FAILED))" "$LOG" || true
rm -f "$LOG"

if [ "$EXIT" -ne 0 ]; then
  echo "✗ NexusApp build FAILED (exit $EXIT)"
  exit "$EXIT"
fi

echo "✓ NexusApp built"
