#!/usr/bin/env bash
# scripts/remote/mac-build-app.sh
# Called from a Linux dev machine: rsyncs the repo to the Mac, then triggers
# a Swift build + open of NexusApp on the Mac via SSH.
#
# Prerequisites:
#   - MAC_HOST set to user@mac-hostname (e.g. newman@minion)
#   - MAC_REPO_ROOT set to the repo path on the Mac (default: ~/magic/nexus)
#   - SSH access from this Linux host to the Mac
#
# Usage: MAC_HOST=newman@minion scripts/remote/mac-build-app.sh
set -euo pipefail

MAC_HOST="${MAC_HOST:?MAC_HOST is not set. Add MAC_HOST=user@mac-hostname to .env.local}"
MAC_REPO_ROOT="${MAC_REPO_ROOT:-~/magic/nexus}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"

# ── 1. Sync repo to Mac ──────────────────────────────────────────────────────
# Resolve MAC_REPO_ROOT on the Mac side (~ expands to Mac's home, not Linux's).
MAC_HOME=$(ssh "$MAC_HOST" 'echo $HOME')
if [[ "$MAC_REPO_ROOT" == ~* ]]; then
  REMOTE_REPO_ROOT="${MAC_HOME}${MAC_REPO_ROOT#\~}"
else
  REMOTE_REPO_ROOT="$MAC_REPO_ROOT"
fi

echo "Syncing repo → ${MAC_HOST}:${REMOTE_REPO_ROOT} ..."
rsync -rlptz --delete \
  --exclude='.git/' \
  --exclude='packages/nexus/tmp/' \
  --exclude='packages/nexus-swift/.build/' \
  --exclude='packages/nexus-swift/build/' \
  --exclude='node_modules/' \
  --exclude='.worktrees/' \
  --exclude='*.ext4' \
  --exclude='.case-studies/' \
  --exclude='scripts/fixtures/' \
  --exclude='.opencode/' \
  "${REPO_ROOT}/" \
  "${MAC_HOST}:${REMOTE_REPO_ROOT}/"

echo "Sync complete."

# ── 2. Build + open NexusApp on Mac ─────────────────────────────────────────
echo "Building NexusApp on ${MAC_HOST} ..."
ssh "$MAC_HOST" bash -lc "\"
  set -euo pipefail
  cd ${REMOTE_REPO_ROOT}
  scripts/swift/build.sh
  scripts/swift/open.sh
\""

echo "NexusApp launched on ${MAC_HOST}."
