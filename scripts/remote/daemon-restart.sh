#!/usr/bin/env bash
# scripts/remote/daemon-restart.sh
# Stop and start the nexus daemon on a remote host using the nexus CLI.
#
# Usage: REMOTE_HOST=user@host [REMOTE_BIN=~/.local/bin/nexus] [REMOTE_PORT=7777] scripts/remote/daemon-restart.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set. Create .env.local with REMOTE_HOST=user@hostname}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"
REMOTE_AGENT_BIN="${REMOTE_AGENT_BIN:-~/.local/bin/nexus-firecracker-agent}"
REMOTE_PORT="${REMOTE_PORT:-7777}"
REMOTE_KERNEL="${REMOTE_KERNEL:-}"
REMOTE_ROOTFS="${REMOTE_ROOTFS:-}"

echo "Stopping daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "${REMOTE_BIN} daemon stop 2>/dev/null || true"

if [ -z "$REMOTE_KERNEL" ]; then
  REMOTE_KERNEL="$(ssh "$REMOTE_HOST" 'for p in "$HOME/.local/share/nexus/runtime/vmlinux.bin" "/var/lib/nexus/vmlinux.bin"; do [ -f "$p" ] && { printf "%s" "$p"; break; }; done')"
fi
if [ -z "$REMOTE_ROOTFS" ]; then
  REMOTE_ROOTFS="$(ssh "$REMOTE_HOST" 'for p in "$HOME/.local/share/nexus/runtime/rootfs.ext4" "/var/lib/nexus/rootfs.ext4"; do [ -f "$p" ] && { printf "%s" "$p"; break; }; done')"
fi
if [ -z "$REMOTE_KERNEL" ] || [ -z "$REMOTE_ROOTFS" ]; then
  echo "Could not resolve remote kernel/rootfs paths." >&2
  echo "Set REMOTE_KERNEL and REMOTE_ROOTFS explicitly or provision runtime assets." >&2
  exit 1
fi

echo "Starting daemon on ${REMOTE_HOST}..."
ssh "$REMOTE_HOST" "NEXUS_FIRECRACKER_AGENT_BIN=${REMOTE_AGENT_BIN} nohup ${REMOTE_BIN} daemon start --kernel ${REMOTE_KERNEL} --rootfs ${REMOTE_ROOTFS} --network --bind 127.0.0.1 --port ${REMOTE_PORT} > /tmp/nexus-daemon.log 2>&1 &"

sleep 3

echo "Checking health..."
ssh "$REMOTE_HOST" "curl -sf http://127.0.0.1:${REMOTE_PORT}/healthz && echo ' ✓ daemon healthy' || echo ' ✗ daemon not healthy'"
