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

# ── 0. Bridge subnet ──────────────────────────────────────────────────────────
# Use NEXUS_BRIDGE_SUBNET if set, otherwise default to 172.26.0.0/16.
# If the chosen subnet overlaps an existing route, fail immediately — set
# NEXUS_BRIDGE_SUBNET to a free /16 before re-running.
VM_ASSETS_DIR=/var/lib/nexus
BRIDGE_SUBNET_FILE="$VM_ASSETS_DIR/bridge-subnet"
mkdir -p "$VM_ASSETS_DIR"

BRIDGE_SUBNET="${NEXUS_BRIDGE_SUBNET:-172.26.0.0/16}"
echo "==> Bridge subnet: $BRIDGE_SUBNET"

# Fail fast if the subnet overlaps any existing kernel route.
_conflict=$(python3 - << PYEOF
import subprocess, ipaddress, sys
net = ipaddress.ip_network('$BRIDGE_SUBNET', strict=False)
out = subprocess.check_output(['ip', '-4', 'route', 'show'], text=True, stderr=subprocess.DEVNULL)
for line in out.splitlines():
    parts = line.split()
    if parts and '/' in parts[0]:
        try:
            if net.overlaps(ipaddress.ip_network(parts[0], strict=False)):
                print(parts[0])
                sys.exit(0)
        except ValueError:
            pass
PYEOF
)
if [ -n "$_conflict" ]; then
  echo "ERROR: $BRIDGE_SUBNET overlaps existing route $_conflict"
  echo "       Set NEXUS_BRIDGE_SUBNET to a free /16 before running 'nexus daemon start'."
  echo "       Example: NEXUS_BRIDGE_SUBNET=172.20.0.0/16 nexus daemon start"
  exit 1
fi

echo "$BRIDGE_SUBNET" > "$BRIDGE_SUBNET_FILE"

# Derive gateway: first host in the /16 (x.y.0.1)
BRIDGE_GW=$(python3 -c "import ipaddress; net=ipaddress.ip_network('$BRIDGE_SUBNET',strict=False); print(str(list(net.hosts())[0]))")
echo "==> Bridge gateway: $BRIDGE_GW"

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
echo "==> Configuring systemd-networkd for nexusbr0 ($BRIDGE_GW)..."
mkdir -p /etc/systemd/network

# Extract prefix length from BRIDGE_SUBNET (always 16 for /16)
BRIDGE_PREFIX=$(echo "$BRIDGE_SUBNET" | cut -d'/' -f2)

cat > /etc/systemd/network/10-nexusbr0.netdev << 'NEXUS_EOF'
[NetDev]
Name=nexusbr0
Kind=bridge
NEXUS_EOF

cat > /etc/systemd/network/11-nexusbr0.network << NEXUS_EOF
[Match]
Name=nexusbr0

[Network]
Address=${BRIDGE_GW}/${BRIDGE_PREFIX}
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
echo "==> Bringing up nexusbr0 ($BRIDGE_GW/$BRIDGE_PREFIX)..."
ip link add nexusbr0 type bridge 2>/dev/null || true
ip addr replace "${BRIDGE_GW}/${BRIDGE_PREFIX}" dev nexusbr0
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
echo "==> Configuring iptables for $BRIDGE_SUBNET..."
if command -v iptables >/dev/null 2>&1; then
  iptables -t nat -C POSTROUTING -s "$BRIDGE_SUBNET" ! -d "$BRIDGE_SUBNET" -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s "$BRIDGE_SUBNET" ! -d "$BRIDGE_SUBNET" -j MASQUERADE
  iptables -C FORWARD -i nexusbr0 -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -i nexusbr0 -j ACCEPT
  iptables -C FORWARD -o nexusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -o nexusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
fi

# ── 7. Policy routing (override Tailscale / VPN catch-all rules) ─────────────
echo "==> Adding policy routing rules for $BRIDGE_SUBNET..."
ip rule show | grep -qF "to $BRIDGE_SUBNET" || \
  ip rule add pref 5190 to "$BRIDGE_SUBNET" lookup main
ip rule show | grep -qF "from $BRIDGE_SUBNET" || \
  ip rule add pref 5191 from "$BRIDGE_SUBNET" lookup main

# ── 8. Add invoking user to kvm group ────────────────────────────────────────
echo "==> Checking kvm group membership..."
if getent group kvm >/dev/null 2>&1 && [ -n "${SUDO_USER:-}" ]; then
  if ! id -nG "$SUDO_USER" | tr ' ' '\n' | grep -qx kvm; then
    usermod -aG kvm "$SUDO_USER"
    echo "==> Added $SUDO_USER to kvm group"
  fi
fi

# ── 9. Download VM kernel (idempotent) ────────────────────────────────────────
KERNEL_PATH="$VM_ASSETS_DIR/vmlinux.bin"
ROOTFS_PATH="$VM_ASSETS_DIR/rootfs.ext4"
KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/vmlinux-5.10.239"
SQUASHFS_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/ubuntu-24.04.squashfs"
KERNEL_CACHE="/tmp/nexus-vmlinux.bin"
SQUASHFS_CACHE="/tmp/nexus-ubuntu.squashfs"
# Direct ext4 rootfs cache: avoids the full squashfs→ext4 conversion.
# Populated automatically by nexus daemon implode (before wiping /var/lib/nexus).
ROOTFS_CACHE="/tmp/nexus-rootfs.ext4"

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
# Write a well-hardened sshd_config that allows root login with keys only.
_write_sshd_config() {
  local mount_dir="$1"
  mkdir -p "$mount_dir/etc/ssh"
  cat > "$mount_dir/etc/ssh/sshd_config" << 'SSHD_EOF'
Port 22
ListenAddress 0.0.0.0
PermitRootLogin yes
PubkeyAuthentication yes
PasswordAuthentication no
ChallengeResponseAuthentication no
UsePAM no
X11Forwarding no
PrintMotd no
AcceptEnv LANG LC_* VSCODE_AGENT_FOLDER CURSOR_AGENT_FOLDER
Subsystem sftp /usr/lib/openssh/sftp-server
SSHD_EOF
  mkdir -p "$mount_dir/root/.ssh"
  chmod 700 "$mount_dir/root/.ssh"
  # Pre-create the privilege separation dir as a directory with correct
  # permissions. Ubuntu 24.04 sshd expects /run/sshd (not /var/run/sshd).
  mkdir -p "$mount_dir/run/sshd"
  chmod 0755 "$mount_dir/run/sshd"
}

# Install openssh-server into a mounted rootfs via chroot + apt.
# Requires proc/dev/sys bind-mounts; the caller sets them up and tears down.
_install_sshd_in_mounted_rootfs() {
  local mount_dir="$1"

  if [ -f "$mount_dir/usr/sbin/sshd" ]; then
    echo "  -> openssh-server already present in rootfs; skipping install."
    _write_sshd_config "$mount_dir"
    return 0
  fi

  echo "  -> Installing openssh-server in rootfs (chroot + apt)..."

  # Bind-mount kernel pseudo-filesystems so apt/dpkg work inside the chroot.
  mount --bind /proc  "$mount_dir/proc"
  mount --bind /dev   "$mount_dir/dev"
  mount --bind /sys   "$mount_dir/sys"
  cp -f /etc/resolv.conf "$mount_dir/etc/resolv.conf" 2>/dev/null || true

  DEBIAN_FRONTEND=noninteractive chroot "$mount_dir" \
    apt-get update -qq -o Acquire::ForceIPv4=true

  DEBIAN_FRONTEND=noninteractive chroot "$mount_dir" \
    apt-get install -y --no-install-recommends openssh-server

  umount "$mount_dir/proc" 2>/dev/null || true
  umount "$mount_dir/sys"  2>/dev/null || true
  # /dev may have sub-mounts; unmount lazily so we don't block.
  umount -l "$mount_dir/dev" 2>/dev/null || true

  _write_sshd_config "$mount_dir"
  echo "  -> openssh-server installed and configured."
}

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
  # Ensure openssh-server is present and correctly configured.
  _install_sshd_in_mounted_rootfs "$mount_dir"
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
    # Use truncate (sparse file) for instant allocation instead of dd which
    # writes zeros sequentially and can take several minutes for a large image.
    truncate -s 4G "$ROOTFS_PATH"
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
    echo "  -> Installing and configuring openssh-server in rootfs..."
    _install_sshd_in_mounted_rootfs "$ROOTFS_MOUNT"
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
