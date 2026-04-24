#!/usr/bin/env bash
# Release build for CI / GitHub Actions (ad-hoc code signing).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SWIFT_DIR="$REPO_ROOT/packages/nexus-swift"
OUT_DIR="$SWIFT_DIR/release"

mkdir -p "$OUT_DIR"

VERSION="${NEXUS_VERSION:-dev}"
ARCH="$(uname -m)"
case "$ARCH" in
arm64) ARCH_LABEL=arm64 ;;
x86_64) ARCH_LABEL=amd64 ;;
*) ARCH_LABEL="$ARCH" ;;
esac

DERIVED="$SWIFT_DIR/.build/xcbuild-release-ci"

echo "Resolving Swift packages..."
xcodebuild \
  -project "$SWIFT_DIR/NexusApp.xcodeproj" \
  -scheme NexusApp \
  -derivedDataPath "$DERIVED" \
  -resolvePackageDependencies

echo "Building NexusApp (Release)..."
LOG=$(mktemp)
set +e
xcodebuild \
  -scheme NexusApp \
  -project "$SWIFT_DIR/NexusApp.xcodeproj" \
  -configuration Release \
  -destination "generic/platform=macOS" \
  -derivedDataPath "$DERIVED" \
  CODE_SIGN_IDENTITY="-" \
  CODE_SIGNING_REQUIRED=YES \
  build >"$LOG" 2>&1
EXIT=$?
set -e

grep -E "^.*(error:|BUILD (SUCCEEDED|FAILED))" "$LOG" || true
rm -f "$LOG"

if [ "$EXIT" -ne 0 ]; then
  echo "✗ NexusApp Release build FAILED (exit $EXIT)"
  exit "$EXIT"
fi

APP="$DERIVED/Build/Products/Release/NexusApp.app"
if [ ! -d "$APP" ]; then
  echo "✗ Expected app bundle missing: $APP"
  exit 1
fi

ZIP_NAME="NexusApp-${VERSION}-macos-${ARCH_LABEL}.zip"
ditto -c -k --keepParent "$APP" "$OUT_DIR/$ZIP_NAME"
echo "✓ Packaged $OUT_DIR/$ZIP_NAME"
