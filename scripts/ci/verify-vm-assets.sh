#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-required}" # hint | required
missing=0
for f in /var/lib/nexus/vmlinux.bin /var/lib/nexus/rootfs.ext4; do
  if [[ ! -f "$f" ]]; then
    echo "missing VM asset: $f"
    missing=1
  fi
done

if [[ "$missing" -ne 0 ]]; then
  if [[ "$MODE" == "hint" ]]; then
    echo "Prebuilt cache not fully hydrated; provisioning step will populate assets."
    exit 0
  fi
  echo "Provisioning did not yield required VM assets."
  exit 1
fi
