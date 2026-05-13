#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SWIFT_DIR="$REPO_ROOT/packages/nexus-swift"

# ── Generate BuildInfo in NexusRPC.swift with git commit + timestamp ──
BUILDINFO_FILE="$SWIFT_DIR/Sources/NexusCore/Generated/NexusRPC.swift"
if [ ! -f "$BUILDINFO_FILE" ]; then
  echo "⚠ NexusRPC.swift not found, skipping BuildInfo generation"
else
  GIT_COMMIT=$(git -C "$REPO_ROOT" rev-parse --short=12 HEAD 2>/dev/null || echo "dev")
  GIT_COMMIT_FULL=$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo "")
  BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  # Replace BuildInfo values in-place (match both "" and any content)
  sed -i '' 's/public static let gitCommit: String = "[^"]*"/public static let gitCommit: String = "'${GIT_COMMIT}'"/' "$BUILDINFO_FILE"
  sed -i '' 's/public static let gitCommitFull: String = "[^"]*"/public static let gitCommitFull: String = "'${GIT_COMMIT_FULL}'"/' "$BUILDINFO_FILE"
  sed -i '' 's/public static let buildTime: String = "[^"]*"/public static let buildTime: String = "'${BUILD_TIME}'"/' "$BUILDINFO_FILE"
  echo "✓ BuildInfo updated in NexusRPC.swift (commit=${GIT_COMMIT})"
fi

echo "Generating Xcode project..."
/opt/homebrew/bin/xcodegen generate --spec "$SWIFT_DIR/project.yml" --project "$SWIFT_DIR" --quiet 2>&1 || {
  echo "⚠ xcodegen not available, continuing with existing project"
}

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
