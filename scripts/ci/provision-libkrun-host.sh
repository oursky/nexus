#!/usr/bin/env bash
set -euo pipefail
RUNNER_USER="${USER:-$(id -un)}"

SOCK=/tmp/nexus-provision.sock
DB=/tmp/nexus-provision.db

sudo NEXUS_LIBKRUN_SKIP_BAKE=1 NEXUS_LIBKRUN_BAKE_TIMEOUT=120s NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=1 /tmp/nexus-bin daemon start \
  --db "$DB" --socket "$SOCK" --workdir-root /data/nexus/libkrun-vms-provision --network=false &
DPID=$!

timeout 180 bash -c 'until [ -S "$0" ]; do sleep 2; done' "$SOCK" \
  || { echo "daemon setup timed out after 180s"; kill $DPID; exit 1; }

sudo /tmp/nexus-bin daemon stop --socket "$SOCK" 2>/dev/null \
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
