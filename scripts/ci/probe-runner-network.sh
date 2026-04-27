#!/usr/bin/env bash
set -euo pipefail

# Quick probe: can this self-hosted runner forward VM TCP traffic via passt?
#
# Fast path: if baked stamp already exists (cache hit), no probe needed.
# Otherwise check runner capabilities (iptables, network namespaces) that
# passt requires. If missing, fail fast instead of wasting 10 minutes.

# ── Fast path: already baked ────────────────────────────────────────────────
if sudo test -f /root/.local/state/nexus/rootfs-baked-v7; then
  echo "[probe] Baked stamp already present; network probe not needed"
  exit 0
fi

# ── Capability checks ───────────────────────────────────────────────────────
echo "[probe] checking runner capabilities for passt VM networking..."

# Passt needs iptables for NAT rules
if ! sudo iptables -t nat -L > /dev/null 2>&1; then
  echo "[probe] Runner cannot manage iptables; passt TCP forwarding likely broken"
  echo "[probe] skipping prewarm on this runner"
  exit 1
fi

# Passt needs network namespaces
if ! sudo unshare -n true 2>/dev/null; then
  echo "[probe] Runner cannot create network namespaces"
  echo "[probe] skipping prewarm on this runner"
  exit 1
fi

# Passt needs /dev/net/tun for TAP devices
if [[ ! -c /dev/net/tun ]]; then
  echo "[probe] /dev/net/tun not available"
  echo "[probe] skipping prewarm on this runner"
  exit 1
fi

# Passt needs CAP_NET_ADMIN (check if we can add a dummy interface in a ns)
if ! sudo unshare -n -r bash -c 'ip link set lo up && ip link add dummy0 type dummy 2>/dev/null || exit 1' 2>/dev/null; then
  echo "[probe] Runner lacks NET_ADMIN capability inside network namespaces"
  echo "[probe] skipping prewarm on this runner"
  exit 1
fi

echo "[probe] Runner capabilities look OK"
exit 0
