#!/usr/bin/env bash
set -euo pipefail

# Sets up Appium and appium-mcp locally in the repo root (pnpm workspace),
# so the opencode.json MCP config works without any global installs.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$SCRIPT_DIR/.."

echo "==> Installing appium + appium-mcp into repo root devDependencies..."
cd "$ROOT"

pnpm add -w --save-dev appium appium-mcp

echo "==> Installing Appium mac2 driver (stored in .appium/)..."
APPIUM_HOME="$ROOT/.appium" ./node_modules/.bin/appium driver install mac2

echo "==> Done. Verify with:"
echo "    APPIUM_HOME=.appium ./node_modules/.bin/appium driver list --installed"
