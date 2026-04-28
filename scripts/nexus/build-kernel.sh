#!/usr/bin/env bash
set -euo pipefail

# Rebuild the vmlinux ELF kernel from source and embed it in the nexus binary.
#
# The kernel is committed directly to the repo at
# packages/nexus/cmd/nexus/assets/vmlinux and embedded via //go:embed.
# Only run this script when you need to update the kernel version or config.
#
# Uses libkrunfw's kernel config as a base (tuned for microVMs) and adds
# the networking options Docker needs (bridge, netfilter, iptables/nat).
#
# Requires: build-essential, libncurses-dev, bison, flex, libssl-dev, bc,
#           libelf-dev, binutils
#
# Usage:
#   build-kernel.sh [output-path]
#
# Example:
#   build-kernel.sh packages/nexus/cmd/nexus/assets/vmlinux

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

OUTPUT_PATH="${1:-$ROOT_DIR/packages/nexus/cmd/nexus/assets/vmlinux}"
KERNEL_VERSION="${KERNEL_VERSION:-6.6.59}"
KERNEL_TARBALL="linux-${KERNEL_VERSION}.tar.xz"
KERNEL_URL="https://cdn.kernel.org/pub/linux/kernel/v6.x/${KERNEL_TARBALL}"
LIBKRUNFW_CONFIG_URL="https://raw.githubusercontent.com/smol-machines/libkrunfw/main/config-libkrunfw_x86_64"

BUILD_DIR="${BUILD_DIR:-/tmp/nexus-kernel-build}"
JOBS="$(nproc 2>/dev/null || echo 4)"

echo "=== Nexus kernel rebuild ==="
echo "Kernel version: ${KERNEL_VERSION}"
echo "Output: ${OUTPUT_PATH}"
echo "Build dir: ${BUILD_DIR}"
echo "Parallel jobs: ${JOBS}"
echo ""

mkdir -p "${BUILD_DIR}"
cd "${BUILD_DIR}"

# Step 1: Download kernel source
if [[ ! -f "${KERNEL_TARBALL}" ]]; then
    echo "Downloading kernel source..."
    curl -fsSL --retry 3 --connect-timeout 30 --max-time 300 \
      "${KERNEL_URL}" -o "${KERNEL_TARBALL}"
fi

# Step 2: Extract
if [[ ! -d "linux-${KERNEL_VERSION}" ]]; then
    echo "Extracting kernel source..."
    tar -xf "${KERNEL_TARBALL}"
fi

cd "linux-${KERNEL_VERSION}"

# Step 3: Fetch libkrunfw config
echo "Fetching libkrunfw base config..."
curl -fsSL --retry 3 "${LIBKRUNFW_CONFIG_URL}" -o .config

# Step 4: Add Docker networking options
echo "Enabling Docker networking options..."

cat >> .config <<'DOCKER_NET'
CONFIG_BRIDGE=y
CONFIG_BRIDGE_NETFILTER=y

# Netfilter core
CONFIG_NETFILTER=y
CONFIG_NETFILTER_ADVANCED=y
CONFIG_NF_CONNTRACK=y
CONFIG_NF_CONNTRACK_EVENTS=y
CONFIG_NF_NAT=y
CONFIG_NF_NAT_MASQUERADE=y

# IPv4 netfilter
CONFIG_IP_NF_IPTABLES=y
CONFIG_IP_NF_FILTER=y
CONFIG_IP_NF_NAT=y
CONFIG_IP_NF_TARGET_MASQUERADE=y
CONFIG_IP_NF_TARGET_REJECT=y

# Matches/targets Docker uses
CONFIG_NETFILTER_XT_MATCH_ADDRTYPE=y
CONFIG_NETFILTER_XT_MATCH_CONNTRACK=y
CONFIG_NETFILTER_XT_MATCH_IPVS=y

# IPVS (for kube-proxy / some Docker setups)
CONFIG_IP_VS=y
CONFIG_IP_VS_NFCT=y
CONFIG_IP_VS_PROTO_TCP=y
CONFIG_IP_VS_PROTO_UDP=y
CONFIG_IP_VS_RR=y

# Dummy device for Docker bridge testing
CONFIG_DUMMY=y

# Enable forwarding
CONFIG_IP_FORWARD=y
DOCKER_NET

# Normalize the config (resolve dependencies, new options, etc.)
make olddefconfig "-j${JOBS}"

# Step 5: Build vmlinux ELF
echo "Building vmlinux..."
make vmlinux "-j${JOBS}"

# Step 6: Install
mkdir -p "$(dirname "${OUTPUT_PATH}")"
cp vmlinux "${OUTPUT_PATH}"

# Strip debug symbols to reduce size
strip --strip-debug "${OUTPUT_PATH}"

echo ""
echo "=== Build complete ==="
echo "Output: ${OUTPUT_PATH}"
echo "Size: $(du -h "${OUTPUT_PATH}" | cut -f1)"
readelf -h "${OUTPUT_PATH}" | head -5
echo ""
echo "Remember to commit the updated kernel:"
echo "  git add ${OUTPUT_PATH}"
echo "  git commit -m 'chore(kernel): rebuild vmlinux ${KERNEL_VERSION}'"
