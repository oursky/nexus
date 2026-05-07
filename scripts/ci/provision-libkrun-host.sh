#!/usr/bin/env bash
set -euo pipefail
RUNNER_USER="${USER:-$(id -un)}"
STEP_TIMEOUT_SECONDS="${STEP_TIMEOUT_SECONDS:-300}"

run_with_timeout() {
  if command -v timeout >/dev/null 2>&1; then
    timeout "$STEP_TIMEOUT_SECONDS" "$@"
  else
    "$@"
  fi
}

SOCK=/tmp/nexus-provision.sock
DB=/tmp/nexus-provision.db

# Purge any stale extracted nexus-libkrun-vm so the daemon extracts the fresh
# embedded binary from /tmp/nexus-bin on this run. Prevents ABI mismatches
# when the committed binary changes but the extracted copy persists.
run_with_timeout sudo rm -f /root/.local/share/nexus/bin/nexus-libkrun-vm \
  /root/.local/share/nexus/lib/libkrun-embed.so \
  /root/.local/share/nexus/lib/libkrunfw-embed.so

run_with_timeout sudo NEXUS_LIBKRUN_SKIP_BAKE=1 NEXUS_LIBKRUN_BAKE_TIMEOUT=120s NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=1 /tmp/nexus-bin daemon start \
  --db "$DB" --socket "$SOCK" --workdir-root /data/nexus/libkrun-vms-provision --network=false &
DPID=$!

timeout "$STEP_TIMEOUT_SECONDS" bash -c 'until [ -S "$0" ]; do sleep 2; done' "$SOCK" \
  || { echo "daemon setup timed out after ${STEP_TIMEOUT_SECONDS}s"; kill $DPID; exit 1; }

run_with_timeout sudo /tmp/nexus-bin daemon stop --socket "$SOCK" 2>/dev/null \
  || kill $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
sudo chown -R "$RUNNER_USER":"$RUNNER_USER" "$HOME/.local" 2>/dev/null || true

ROOT_VM=/root/.local/share/nexus/vm
if sudo test -d "$ROOT_VM"; then
  sudo install -d -m 0755 /var/lib/nexus
  for f in vmlinux.bin rootfs.ext4; do
    SRC="$ROOT_VM/$f"
    DST="/var/lib/nexus/$f"
    if sudo test -f "$SRC" && ! sudo test -e "$DST"; then
      sudo ln "$SRC" "$DST" 2>/dev/null || sudo cp "$SRC" "$DST"
      sudo chmod 644 "$DST"
    fi
  done
fi

ROOT_HASH=/root/.local/state/nexus/rootfs-agent.sha256
USER_HASH="$HOME/.local/state/nexus/rootfs-agent.sha256"
if sudo test -f "$ROOT_HASH"; then
  mkdir -p "$(dirname "$USER_HASH")"
  sudo cat "$ROOT_HASH" > "$USER_HASH"
fi

# Install VM assets to the runner user's XDG data dir so that subsequent
# daemon starts (which run as root with HOME=$HOME) find rootfs.ext4 and
# vmlinux.bin already present and skip the expensive download+build step.
ROOT_VM=/root/.local/share/nexus/vm
USER_VM="$HOME/.local/share/nexus/vm"
if sudo test -d "$ROOT_VM"; then
  mkdir -p "$USER_VM"
  for f in vmlinux.bin rootfs.ext4; do
    SRC="/var/lib/nexus/$f"
    DST="$USER_VM/$f"
    if [ -f "$SRC" ] && [ ! -e "$DST" ]; then
      ln "$SRC" "$DST" 2>/dev/null || cp "$SRC" "$DST"
    fi
  done
fi
