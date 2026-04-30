#!/usr/bin/env bash
# Build NexusApp (Release, ad-hoc signed) and package into a DMG for CI artifacts.
# Does NOT require notarization or a Developer ID certificate — suitable for
# quickly distributing a build to testers who can bypass Gatekeeper.
#
# Outputs:
#   packages/nexus-swift/release/NexusApp-<VERSION>-macos-<ARCH>.dmg
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SWIFT_DIR="$REPO_ROOT/packages/nexus-swift"
OUT_DIR="$SWIFT_DIR/release"

mkdir -p "$OUT_DIR"

VERSION="${NEXUS_VERSION:-dev}"
ARCH="$(uname -m)"
case "$ARCH" in
  arm64)  ARCH_LABEL=arm64 ;;
  x86_64) ARCH_LABEL=amd64 ;;
  *)      ARCH_LABEL="$ARCH" ;;
esac

DERIVED="$SWIFT_DIR/.build/xcbuild-dmg-ci"

echo "==> Resolving Swift packages..."
xcodebuild \
  -project "$SWIFT_DIR/NexusApp.xcodeproj" \
  -scheme NexusApp \
  -derivedDataPath "$DERIVED" \
  -resolvePackageDependencies 2>&1 | tail -3

echo "==> Building NexusApp (Release, ad-hoc signing)..."
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
  CODE_SIGN_STYLE=Manual \
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
echo "✓ NexusApp built: $APP"

# ── Package into a DMG ────────────────────────────────────────────────────────
DMG_NAME="NexusApp-${VERSION}-macos-${ARCH_LABEL}.dmg"
DMG_PATH="$OUT_DIR/$DMG_NAME"
STAGING="$(mktemp -d)"
trap 'rm -rf "$STAGING"' EXIT

# Copy app into staging dir and add an Applications symlink for easy drag-install.
ditto "$APP" "$STAGING/NexusApp.app"
ln -s /Applications "$STAGING/Applications"

echo "==> Creating DMG: $DMG_PATH"
hdiutil create \
  -volname "NexusApp ${VERSION}" \
  -srcfolder "$STAGING" \
  -ov \
  -format UDZO \
  "$DMG_PATH"

echo "✓ DMG: $DMG_PATH ($(du -sh "$DMG_PATH" | cut -f1))"
