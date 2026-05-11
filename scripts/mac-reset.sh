#!/usr/bin/env bash
# scripts/mac-reset.sh — Full reset of Nexus Mac app state
# Run on the Mac: bash scripts/mac-reset.sh
set -euo pipefail

BUNDLE_ID="com.oursky.nexus.local"

# 1. Kill the app
pkill -x NexusApp 2>/dev/null || true
sleep 1

# 2. Kill ALL orphaned SSH tunnels spawned by Nexus
# Kill ALL SSH tunnels with -N (no shell) + forwarding — the exact signature of our tunnels
pkill -f "ssh.*-N.*-L" 2>/dev/null || true
pkill -f "ssh.*-N.*-R" 2>/dev/null || true
sleep 0.5

# 3. Remove UserDefaults (daemon profiles)
defaults delete "${BUNDLE_ID}" nexus.daemonProfiles 2>/dev/null || true
rm -f "${HOME}/Library/Preferences/${BUNDLE_ID}.plist" 2>/dev/null || true

# 4. Remove Nexus config/logs
rm -rf "${HOME}/.config/nexus" 2>/dev/null || true

# 5. Remove Nexus local data (workspace records)
rm -rf "${HOME}/.local/share/nexus" 2>/dev/null || true

# 6. Remove Keychain entries
for svc in nexus-daemon-token nexus/token nexus-daemon nexus; do
    security delete-generic-password -s "${svc}" 2>/dev/null || true
done
# Also delete by account name (some may use different label format)
security delete-generic-password -a daemon-token -s nexus 2>/dev/null || true

echo "✓ Nexus state reset complete."
echo ""
echo "Next: run 'task dev:swift' to rebuild and relaunch, or:"
echo "  open packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app"
echo "  then use 'defaults' to write a new profile:"
echo "  json='[{\"profileId\":\"local\",\"name\":\"dev\",\"port\":7778,\"isDefault\":true,\"lastKnownStatus\":\"unknown\",\"sshTarget\":\"newman@linuxbox\"}]'"
echo "  echo -n \"\$json\" | xxd -p | tr -d '\\n' | xargs -I{} defaults write ${BUNDLE_ID} nexus.daemonProfiles -data '{}'"
