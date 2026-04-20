#!/usr/bin/env bash
# nexus daemon start — Firecracker host setup
#
# This script is run with root/sudo by "nexus daemon start" on first use (and
# is a fast no-op once the host is already configured).  You can review every
# operation here before running nexus.
#
# Variables injected by nexus (prepended as export statements):
#   NEXUS_SETUP_TAP_HELPER_SRC   path to the extracted nexus-tap-helper binary
#   NEXUS_SETUP_AGENT_SRC        path to the extracted nexus-firecracker-agent binary
#   NEXUS_SETUP_FIRECRACKER_SRC  path to the extracted firecracker binary
#   NEXUS_INSTALL_BIN_DIR        user-local bin directory (e.g. /home/user/.local/bin)

set -euo pipefail

# ── Sanity-check required variables ──────────────────────────────────────────
: "${NEXUS_SETUP_TAP_HELPER_SRC:?}"
: "${NEXUS_SETUP_AGENT_SRC:?}"
: "${NEXUS_SETUP_FIRECRACKER_SRC:?}"
: "${NEXUS_INSTALL_BIN_DIR:?}"

# ── 1. Install nexus-tap-helper with cap_net_admin ───────────────────────────
echo "==> Installing nexus-tap-helper to ${NEXUS_INSTALL_BIN_DIR}..."
mkdir -p "$NEXUS_INSTALL_BIN_DIR"
cp "$NEXUS_SETUP_TAP_HELPER_SRC" "$NEXUS_INSTALL_BIN_DIR/nexus-tap-helper"
chmod 755 "$NEXUS_INSTALL_BIN_DIR/nexus-tap-helper"
setcap cap_net_admin=ep "$NEXUS_INSTALL_BIN_DIR/nexus-tap-helper"

# ── 2. Install firecracker ────────────────────────────────────────────────────
echo "==> Installing firecracker to ${NEXUS_INSTALL_BIN_DIR}..."
cp "$NEXUS_SETUP_FIRECRACKER_SRC" "$NEXUS_INSTALL_BIN_DIR/firecracker"
chmod 755 "$NEXUS_INSTALL_BIN_DIR/firecracker"

# ── 3. Configure systemd-networkd for the nexusbr0 bridge ────────────────────
echo "==> Configuring systemd-networkd for nexusbr0..."
mkdir -p /etc/systemd/network

cat > /etc/systemd/network/10-nexusbr0.netdev << 'NEXUS_EOF'
[NetDev]
Name=nexusbr0
Kind=bridge
NEXUS_EOF

cat > /etc/systemd/network/11-nexusbr0.network << 'NEXUS_EOF'
[Match]
Name=nexusbr0

[Network]
Address=172.26.0.1/16
IPForward=yes
IPMasquerade=ipv4
ConfigureWithoutCarrier=yes
IgnoreCarrierLoss=yes
NEXUS_EOF

cat > /etc/systemd/network/12-nexus-tap.network << 'NEXUS_EOF'
[Match]
Name=nexus-*

[Network]
Bridge=nexusbr0
NEXUS_EOF

systemctl enable systemd-networkd
systemctl restart systemd-networkd

# ── 4. Bring up the bridge immediately (don't wait for networkd) ──────────────
echo "==> Bringing up nexusbr0..."
ip link add nexusbr0 type bridge 2>/dev/null || true
ip addr replace 172.26.0.1/16 dev nexusbr0
ip link set nexusbr0 up

# Wait up to 15 s for the bridge route to become active.
retries=15
while [ $retries -gt 0 ]; do
  if ! ip route show dev nexusbr0 | grep -q 'linkdown'; then break; fi
  retries=$((retries - 1))
  sleep 1
done
if ip route show dev nexusbr0 | grep -q 'linkdown'; then
  echo "WARN: nexusbr0 route still linkdown; check TAP attach path"
fi

# ── 5. Enable IP forwarding ───────────────────────────────────────────────────
echo "==> Enabling IP forwarding..."
sysctl -w net.ipv4.ip_forward=1
printf 'net.ipv4.ip_forward = 1\n' > /etc/sysctl.d/99-nexus-ip-forward.conf

# ── 6. NAT and forwarding iptables rules ─────────────────────────────────────
echo "==> Configuring iptables..."
if command -v iptables >/dev/null 2>&1; then
  iptables -t nat -C POSTROUTING -s 172.26.0.0/16 ! -d 172.26.0.0/16 -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s 172.26.0.0/16 ! -d 172.26.0.0/16 -j MASQUERADE
  iptables -C FORWARD -i nexusbr0 -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -i nexusbr0 -j ACCEPT
  iptables -C FORWARD -o nexusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -o nexusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
fi

# ── 7. Policy routing (override Tailscale / VPN catch-all rules) ─────────────
echo "==> Adding policy routing rules..."
ip rule show | grep -q '^5190:.* to 172.26.0.0/16 .* lookup main' || \
  ip rule add pref 5190 to 172.26.0.0/16 lookup main
ip rule show | grep -q '^5191:.* from 172.26.0.0/16 .* lookup main' || \
  ip rule add pref 5191 from 172.26.0.0/16 lookup main

# ── 8. Add invoking user to kvm group ────────────────────────────────────────
echo "==> Checking kvm group membership..."
if getent group kvm >/dev/null 2>&1 && [ -n "${SUDO_USER:-}" ]; then
  if ! id -nG "$SUDO_USER" | tr ' ' '\n' | grep -qx kvm; then
    usermod -aG kvm "$SUDO_USER"
    echo "==> Added $SUDO_USER to kvm group"
  fi
fi

# ── 9. Download VM kernel (idempotent) ────────────────────────────────────────
VM_ASSETS_DIR=/var/lib/nexus
KERNEL_PATH="$VM_ASSETS_DIR/vmlinux.bin"
ROOTFS_PATH="$VM_ASSETS_DIR/rootfs.ext4"
KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/vmlinux-5.10.239"
SQUASHFS_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/ubuntu-24.04.squashfs"
KERNEL_CACHE="/tmp/nexus-vmlinux.bin"
SQUASHFS_CACHE="/tmp/nexus-ubuntu.squashfs"
# Direct ext4 rootfs cache: avoids the full squashfs→ext4 conversion.
# Populated automatically by nexus daemon implode (before wiping /var/lib/nexus).
ROOTFS_CACHE="/tmp/nexus-rootfs.ext4"

mkdir -p "$VM_ASSETS_DIR"

if [ ! -f "$KERNEL_PATH" ]; then
  if [ -f "$KERNEL_CACHE" ]; then
    echo "==> Using cached kernel..."
    cp "$KERNEL_CACHE" "$KERNEL_PATH"
  else
    echo "==> Downloading Firecracker kernel..."
    wget -q -O "$KERNEL_PATH" "$KERNEL_URL"
  fi
fi

# ── 10. Build VM rootfs (idempotent) ──────────────────────────────────────────
_update_agent_in_rootfs() {
  local rootfs_path="$1"
  local mount_dir
  mount_dir=$(mktemp -d)
  trap 'umount "$mount_dir" 2>/dev/null || true; rm -rf "$mount_dir"' RETURN
  mount -o loop "$rootfs_path" "$mount_dir"
  mkdir -p "$mount_dir/usr/local/bin" "$mount_dir/workspace"
  cp "$NEXUS_SETUP_AGENT_SRC" "$mount_dir/usr/local/bin/nexus-firecracker-agent"
  chmod 755 "$mount_dir/usr/local/bin/nexus-firecracker-agent"
  printf '#!/bin/sh\nexec /usr/local/bin/nexus-firecracker-agent\n' > "$mount_dir/sbin/init"
  chmod 755 "$mount_dir/sbin/init"
  ln -sf /sbin/init "$mount_dir/init" 2>/dev/null || cp "$mount_dir/sbin/init" "$mount_dir/init"
  umount "$mount_dir"
  trap - RETURN
  rm -rf "$mount_dir"
}

if [ ! -f "$ROOTFS_PATH" ]; then
  # Fast path: use the pre-built rootfs cache (saved by `nexus daemon implode`).
  if [ -f "$ROOTFS_CACHE" ]; then
    echo "==> Restoring VM rootfs from cache..."
    cp "$ROOTFS_CACHE" "$ROOTFS_PATH"
    echo "==> Updating nexus-firecracker-agent in restored rootfs..."
    _update_agent_in_rootfs "$ROOTFS_PATH"
    echo "    done."
  else
    # Full build: download squashfs → convert to ext4.
    echo "==> Building Firecracker rootfs (this takes a few minutes)..."
    SQUASHFS_TMP=$(mktemp -d)
    ROOTFS_MOUNT=$(mktemp -d)
    trap 'umount "$ROOTFS_MOUNT" 2>/dev/null || true; rm -rf "$SQUASHFS_TMP" "$ROOTFS_MOUNT"' EXIT

    if [ -f "$SQUASHFS_CACHE" ]; then
      echo "  -> Using cached squashfs..."
      cp "$SQUASHFS_CACHE" "$SQUASHFS_TMP/rootfs.squashfs"
    else
      echo "  -> Downloading squashfs rootfs..."
      wget -q -O "$SQUASHFS_TMP/rootfs.squashfs" "$SQUASHFS_URL"
    fi

    echo "  -> Extracting squashfs..."
    unsquashfs -d "$SQUASHFS_TMP/rootfs" "$SQUASHFS_TMP/rootfs.squashfs"

    echo "  -> Creating ext4 image..."
    dd if=/dev/zero of="$ROOTFS_PATH" bs=1M count=4096 status=none
    mkfs.ext4 -F -q "$ROOTFS_PATH"

    mount -o loop "$ROOTFS_PATH" "$ROOTFS_MOUNT"
    echo "  -> Copying rootfs tree..."
    rsync -a "$SQUASHFS_TMP/rootfs/" "$ROOTFS_MOUNT/"
    echo "  -> Injecting nexus-firecracker-agent as PID 1..."
    mkdir -p "$ROOTFS_MOUNT/usr/local/bin" "$ROOTFS_MOUNT/workspace"
    cp "$NEXUS_SETUP_AGENT_SRC" "$ROOTFS_MOUNT/usr/local/bin/nexus-firecracker-agent"
    chmod 755 "$ROOTFS_MOUNT/usr/local/bin/nexus-firecracker-agent"
    printf '#!/bin/sh\nexec /usr/local/bin/nexus-firecracker-agent\n' > "$ROOTFS_MOUNT/sbin/init"
    chmod 755 "$ROOTFS_MOUNT/sbin/init"
    ln -sf /sbin/init "$ROOTFS_MOUNT/init" 2>/dev/null || cp "$ROOTFS_MOUNT/sbin/init" "$ROOTFS_MOUNT/init"
    umount "$ROOTFS_MOUNT"
    trap - EXIT
    rm -rf "$SQUASHFS_TMP" "$ROOTFS_MOUNT"
    echo "  -> rootfs built successfully."
  fi
else
  # Rootfs already exists — update agent in-place (always ship the latest).
  echo "==> Updating nexus-firecracker-agent in rootfs..."
  _update_agent_in_rootfs "$ROOTFS_PATH"
fi

# ── 11. Fix ownership so the invoking user can read/write VM assets ───────────
if [ -n "${SUDO_USER:-}" ]; then
  chown "$SUDO_USER":"$SUDO_USER" "$KERNEL_PATH" 2>/dev/null || true
  chmod 644 "$KERNEL_PATH"
  chown "$SUDO_USER":"$SUDO_USER" "$ROOTFS_PATH" 2>/dev/null || true
  chmod 600 "$ROOTFS_PATH"
fi

echo "==> Firecracker host setup complete."
