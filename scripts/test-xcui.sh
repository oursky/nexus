#!/usr/bin/env bash
set -euo pipefail

# Run NexusUITests against the remote daemon via the app's normal SSH tunnel.
# No token injection needed — the app uses SSHTunnelManager with newman@linuxbox.
#
# Usage:
#   bash scripts/test-xcui.sh                  # normal run
#   bash scripts/test-xcui.sh --only <TestId>  # run one test (xcodebuild -only-testing)

ONLY_TESTING="NexusUITests"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --only) shift; ONLY_TESTING="NexusUITests/$1"; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

TIMESTAMP=$(date +%Y%m%d-%H%M%S)
RESULT_BUNDLE="packages/nexus-swift/.build/xcresults/NexusUITests-${TIMESTAMP}.xcresult"
mkdir -p "$(dirname "$RESULT_BUNDLE")"

xcodebuild test \
  -project packages/nexus-swift/NexusApp.xcodeproj \
  -scheme NexusApp \
  -destination 'platform=macOS' \
  -only-testing "$ONLY_TESTING" \
  -resultBundlePath "$RESULT_BUNDLE"
XCODE_EXIT=$?

scripts/xcresult-report.sh "$RESULT_BUNDLE" || true

echo ""
echo "Result bundle: $RESULT_BUNDLE"
echo "Open with: open $RESULT_BUNDLE"

exit $XCODE_EXIT
