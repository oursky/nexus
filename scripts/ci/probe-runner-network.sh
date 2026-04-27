#!/usr/bin/env bash
set -euo pipefail

# Quick probe: can this self-hosted runner forward VM TCP traffic via passt?
#
# Fast path: if baked stamp already exists (cache hit), no probe needed.
# Otherwise check runner capabilities AND test iptables NAT (which passt
# requires). If NAT doesn't work, passt can't forward VM traffic.

# ── Fast path: already baked ────────────────────────────────────────────────
if sudo test -f /root/.local/state/nexus/rootfs-baked-v7; then
  echo "[probe] Baked stamp already present; network probe not needed"
  exit 0
fi

# ── Capability checks ───────────────────────────────────────────────────────
echo "[probe] checking runner capabilities for passt VM networking..."

if ! sudo iptables -t nat -L > /dev/null 2>&1; then
  echo "[probe] Runner cannot manage iptables"
  exit 1
fi

if ! sudo unshare -n true 2>/dev/null; then
  echo "[probe] Runner cannot create network namespaces"
  exit 1
fi

if [[ ! -c /dev/net/tun ]]; then
  echo "[probe] /dev/net/tun not available"
  exit 1
fi

if ! sudo unshare -n -r bash -c 'ip link set lo up && ip link add dummy0 type dummy 2>/dev/null || exit 1' 2>/dev/null; then
  echo "[probe] Runner lacks NET_ADMIN inside network namespaces"
  exit 1
fi

# ── iptables NAT test (critical for passt) ──────────────────────────────────
# Passt creates MASQUERADE and REDIRECT rules in the nat table.
# Many container runtimes disable this even with NET_ADMIN.
echo "[probe] testing iptables NAT (required by passt)..."
if ! sudo iptables -t nat -A POSTROUTING -o lo -j MASQUERADE -m comment --comment "nexus-probe-test" 2>/dev/null; then
  echo "[probe] Cannot create iptables NAT MASQUERADE rule — passt forwarding broken"
  exit 1
fi
sudo iptables -t nat -D POSTROUTING -o lo -j MASQUERADE -m comment --comment "nexus-probe-test" 2>/dev/null || true

if ! sudo iptables -t nat -A PREROUTING -p tcp -d 127.0.0.1 --dport 65535 -j REDIRECT --to-ports 65534 -m comment --comment "nexus-probe-test" 2>/dev/null; then
  echo "[probe] Cannot create iptables NAT REDIRECT rule — passt forwarding broken"
  exit 1
fi
sudo iptables -t nat -D PREROUTING -p tcp -d 127.0.0.1 --dport 65535 -j REDIRECT --to-ports 65534 -m comment --comment "nexus-probe-test" 2>/dev/null || true

echo "[probe] Runner network forwarding looks OK"
exit 0
