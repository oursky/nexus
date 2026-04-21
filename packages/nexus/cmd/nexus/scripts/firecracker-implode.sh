#!/usr/bin/env bash
# nexus daemon implode — tear down all Firecracker host state
#
# This script is run with root/sudo by "nexus daemon implode".  It is the
# exact inverse of firecracker-setup.sh.  You can review every operation here.
#
# Variables injected by nexus (prepended as export statements):
#   NEXUS_INSTALL_BIN_DIR  user-local bin directory (e.g. /home/user/.local/bin)

set -euo pipefail

: "${NEXUS_INSTALL_BIN_DIR:?}"

VM_ASSETS_DIR=/var/lib/nexus

# ── 1. Remove nexus-* TAP interfaces ─────────────────────────────────────────
echo "==> Removing nexus TAP interfaces..."
for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | \
               sed 's/@.*//' | grep -E '^nexus-' || true); do
  ip link set "$iface" down 2>/dev/null || true
  ip link delete "$iface" 2>/dev/null || true
  echo "    removed TAP: $iface"
done

# ── 2. Remove nexusbr0 bridge ─────────────────────────────────────────────────
echo "==> Removing nexusbr0 bridge..."
if ip link show nexusbr0 >/dev/null 2>&1; then
  ip link set nexusbr0 down 2>/dev/null || true
  ip link delete nexusbr0 2>/dev/null || true
fi

# ── 3. Remove iptables rules ──────────────────────────────────────────────────
echo "==> Removing iptables rules..."
if command -v iptables >/dev/null 2>&1; then
  iptables -t nat -D POSTROUTING -s 172.26.0.0/16 ! -d 172.26.0.0/16 -j MASQUERADE 2>/dev/null || true
  iptables -D FORWARD -i nexusbr0 -j ACCEPT 2>/dev/null || true
  iptables -D FORWARD -o nexusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
fi

# ── 4. Remove policy routing rules ────────────────────────────────────────────
echo "==> Removing policy routing rules..."
ip rule del pref 5190 to 172.26.0.0/16 lookup main 2>/dev/null || true
ip rule del pref 5191 from 172.26.0.0/16 lookup main 2>/dev/null || true

# ── 5. Remove systemd-networkd units ─────────────────────────────────────────
echo "==> Removing systemd-networkd units..."
rm -f /etc/systemd/network/10-nexusbr0.netdev
rm -f /etc/systemd/network/11-nexusbr0.network
rm -f /etc/systemd/network/12-nexus-tap.network
systemctl daemon-reload 2>/dev/null || true
systemctl restart systemd-networkd 2>/dev/null || true

# ── 6. Remove sysctl config ───────────────────────────────────────────────────
echo "==> Removing sysctl config..."
rm -f /etc/sysctl.d/99-nexus-ip-forward.conf

# ── 7. Remove installed binaries (current and legacy locations) ──────────────
echo "==> Removing nexus binaries from ${NEXUS_INSTALL_BIN_DIR}..."
rm -f "$NEXUS_INSTALL_BIN_DIR/nexus-tap-helper"
rm -f "$NEXUS_INSTALL_BIN_DIR/firecracker"
# Legacy: older nexus versions installed to /usr/local/bin.
rm -f /usr/local/bin/nexus-tap-helper
rm -f /usr/local/bin/firecracker

# ── 8. Save rootfs cache then remove VM assets ────────────────────────────────
echo "==> Caching VM rootfs for fast re-provisioning..."
ROOTFS_CACHE="/tmp/nexus-rootfs.ext4"
KERNEL_CACHE="/tmp/nexus-vmlinux.bin"
if [ -f "$VM_ASSETS_DIR/rootfs.ext4" ]; then
  cp "$VM_ASSETS_DIR/rootfs.ext4" "$ROOTFS_CACHE" && echo "    cached: $ROOTFS_CACHE" || true
fi
if [ -f "$VM_ASSETS_DIR/vmlinux.bin" ]; then
  cp "$VM_ASSETS_DIR/vmlinux.bin" "$KERNEL_CACHE" && echo "    cached: $KERNEL_CACHE" || true
fi

echo "==> Removing VM assets from ${VM_ASSETS_DIR}..."
rm -f "$VM_ASSETS_DIR/vmlinux.bin"
rm -f "$VM_ASSETS_DIR/rootfs.ext4"
rmdir "$VM_ASSETS_DIR" 2>/dev/null || true

echo "==> Privileged cleanup complete."
