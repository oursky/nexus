#!/usr/bin/env bash
# Select the Xcode installation for the current runner.
# Prefers NEXUS_MACOS_DEVELOPER_DIR if set; falls back to the latest Xcode
# found under /Volumes/xcodes (convention on self-hosted hkfc runners).
set -euo pipefail

if [[ -n "${NEXUS_MACOS_DEVELOPER_DIR:-}" ]]; then
  XCODE_DIR="$NEXUS_MACOS_DEVELOPER_DIR"
else
  XCODE_DIR="$(ls -d /Volumes/xcodes/Xcode_*.app 2>/dev/null | sort -V | tail -1)/Contents/Developer"
fi

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "DEVELOPER_DIR=$XCODE_DIR" >> "$GITHUB_ENV"
fi

echo "Selected Xcode: $XCODE_DIR"
xcode-select -p || true
