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

echo "Building NexusApp (swift build)..."
swift build --package-path "$SWIFT_DIR" -c debug 2>&1
EXIT=$?

if [ "$EXIT" -ne 0 ]; then
  echo "✗ NexusApp build FAILED (exit $EXIT)"
  exit "$EXIT"
fi

echo "✓ NexusApp built"

# Create minimal .app bundle for open.sh compatibility
APP_DIR="$SWIFT_DIR/.build/xcbuild/Build/Products/Debug/NexusApp.app"
mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

# Copy executable (swift build output)
BINARY_PATH="$SWIFT_DIR/.build/arm64-apple-macosx/debug/NexusApp"
if [ -f "$BINARY_PATH" ]; then
  cp "$BINARY_PATH" "$APP_DIR/Contents/MacOS/NexusApp"
  chmod +x "$APP_DIR/Contents/MacOS/NexusApp"
fi

# Minimal Info.plist
if [ ! -f "$APP_DIR/Contents/Info.plist" ]; then
  cat > "$APP_DIR/Contents/Info.plist" << 'INFOEOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>NexusApp</string>
    <key>CFBundleIdentifier</key>
    <string>com.oursky.nexus</string>
    <key>CFBundleName</key>
    <string>NexusApp</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleVersion</key>
    <string>1</string>
    <key>LSMinimumSystemVersion</key>
    <string>14.0</string>
</dict>
</plist>
INFOEOF
fi
