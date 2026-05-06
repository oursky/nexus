#!/usr/bin/env bash
# scripts/workspace-sync.sh
# Volume sync: continuously (or one-shot) rsyncs a nexus workspace directory
# from this Linux host back to a target path on the Mac.
#
# Modelled after e2b / sandbox0 volume sync: changes made inside a nexus process
# sandbox workspace are mirrored to the Mac so the Mac can rebuild the Swift app
# (or any other artifact) from the live workspace state.
#
# Usage:
#   # one-shot sync
#   MAC_HOST=newman@minion WORKSPACE_PATH=~/.local/share/nexus/workspaces/ws-123 \
#     scripts/workspace-sync.sh
#
#   # watch mode (re-sync on changes using inotifywait if available, else polling)
#   MAC_HOST=newman@minion WORKSPACE_PATH=... MAC_TARGET_PATH=~/magic/nexus \
#     scripts/workspace-sync.sh --watch
#
# ENV VARS:
#   MAC_HOST           SSH target (required)
#   WORKSPACE_PATH     Source path on this Linux host (required)
#   MAC_TARGET_PATH    Destination path on the Mac (default: MAC_REPO_ROOT or ~/magic/nexus)
#   SYNC_INTERVAL      Polling interval in seconds when inotifywait unavailable (default: 3)
set -euo pipefail

MAC_HOST="${MAC_HOST:?MAC_HOST is not set. Add MAC_HOST=user@mac-hostname to .env.local}"
WORKSPACE_PATH="${WORKSPACE_PATH:?WORKSPACE_PATH is not set. Set it to the workspace directory to sync.}"
MAC_TARGET_PATH="${MAC_TARGET_PATH:-${MAC_REPO_ROOT:-~/magic/nexus}}"
SYNC_INTERVAL="${SYNC_INTERVAL:-3}"
WATCH=false

for arg in "$@"; do
  case "$arg" in
    --watch|-w) WATCH=true ;;
  esac
done

# Normalise: strip trailing slash so rsync destination is predictable.
WORKSPACE_PATH="${WORKSPACE_PATH%/}"

do_sync() {
  # Resolve the remote target path on the Mac.
  # Strategy: query the Mac's home dir, then map the local path to the
  # equivalent path under the Mac home.  This handles the common case where
  # both machines use ~/magic/nexus but have different home prefixes
  # (/home/newman vs /Users/newman).
  local mac_home
  mac_home=$(ssh "$MAC_HOST" 'echo $HOME')
  local linux_home="$HOME"

  # If MAC_TARGET_PATH lives under the Linux home, mirror it under Mac home.
  # Otherwise use it verbatim (assumed to be a full path already correct for Mac).
  local remote_target
  if [[ "$MAC_TARGET_PATH" == "${linux_home}/"* ]]; then
    local rel="${MAC_TARGET_PATH#"${linux_home}/"}"
    remote_target="${mac_home}/${rel}"
  else
    remote_target="$MAC_TARGET_PATH"
  fi

  rsync -rlptz --delete \
    --exclude='.git/' \
    --exclude='tmp/' \
    --exclude='*.ext4' \
    --exclude='node_modules/' \
    --exclude='.build/' \
    --exclude='.worktrees/' \
    --exclude='.case-studies/' \
    --exclude='scripts/fixtures/' \
    --exclude='.opencode/' \
    "${WORKSPACE_PATH}/" \
    "${MAC_HOST}:${remote_target}/"
}

if ! $WATCH; then
  echo "Syncing ${WORKSPACE_PATH} → ${MAC_HOST}:${MAC_TARGET_PATH} (one-shot) ..."
  do_sync
  echo "✓ Sync complete."
  exit 0
fi

echo "Watch sync: ${WORKSPACE_PATH} → ${MAC_HOST}:${MAC_TARGET_PATH}"
echo "Press Ctrl+C to stop."

# Initial sync
echo "[$(date +%T)] Initial sync ..."
do_sync
echo "[$(date +%T)] ✓ ready"

if command -v inotifywait >/dev/null 2>&1; then
  # Efficient event-driven sync via inotifywait (inotify-tools)
  echo "[$(date +%T)] Using inotifywait for change detection."
  while inotifywait -r -q \
    --event modify,create,delete,move \
    --exclude '/\.git/' \
    "$WORKSPACE_PATH" 2>/dev/null; do
    echo "[$(date +%T)] Change detected — syncing ..."
    do_sync
    echo "[$(date +%T)] ✓ synced"
  done
else
  # Fallback: checksum-based polling every SYNC_INTERVAL seconds
  echo "[$(date +%T)] inotifywait not found — falling back to ${SYNC_INTERVAL}s polling."
  echo "             Install inotify-tools for lower-latency sync."
  while true; do
    sleep "$SYNC_INTERVAL"
    do_sync
    echo "[$(date +%T)] ✓ synced"
  done
fi
