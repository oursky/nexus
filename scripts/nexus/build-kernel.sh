#!/usr/bin/env bash
set -euo pipefail

# Rebuild the custom kernel from source and install it to the repo assets dir.
#
# The kernel is committed directly to the repo and embedded via //go:embed:
#   - packages/nexus/cmd/nexus/assets/vmlinux   (x86_64, ELF)
#   - packages/nexus/cmd/nexus/assets/Image     (arm64, raw)
#
# Only run this script when you need to update the kernel version or config.
#
# Uses libkrunfw's kernel config as a base (tuned for microVMs) and adds
# the networking options Docker needs (bridge, netfilter, iptables/nat).
#
# Requires: build-essential, libncurses-dev, bison, flex, libssl-dev, bc,
#           libelf-dev, binutils
# Optional: cross-compiler for building foreign architectures
#
# Usage:
#   build-kernel.sh [--arch x86_64|arm64] [output-path]
#
# Examples:
#   build-kernel.sh --arch x86_64 packages/nexus/cmd/nexus/assets/vmlinux
#   build-kernel.sh --arch arm64  packages/nexus/cmd/nexus/assets/Image

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

ARCH=""
OUTPUT_PATH=""

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      ARCH="$2"
      shift 2
      ;;
    --arch=*)
      ARCH="${1#*=}"
      shift
      ;;
    *)
      OUTPUT_PATH="$1"
      shift
      ;;
  esac
done

# Auto-detect arch if not specified
if [[ -z "$ARCH" ]]; then
  case "$(uname -m)" in
    x86_64)  ARCH=x86_64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "ERROR: Cannot auto-detect arch: $(uname -m)"; exit 1 ;;
  esac
fi

# Set defaults per arch
if [[ "$ARCH" == "x86_64" ]]; then
  OUTPUT_PATH="${OUTPUT_PATH:-$ROOT_DIR/packages/nexus/cmd/nexus/assets/vmlinux}"
  KERNEL_TARGET="vmlinux"
  KERNEL_FORMAT="ELF"
  LIBKRUNFW_CONFIG_URL="https://raw.githubusercontent.com/smol-machines/libkrunfw/main/config-libkrunfw_x86_64"
  CROSS_COMPILE="${CROSS_COMPILE:-}"
elif [[ "$ARCH" == "arm64" ]]; then
  OUTPUT_PATH="${OUTPUT_PATH:-$ROOT_DIR/packages/nexus/cmd/nexus/assets/Image}"
  KERNEL_TARGET="Image"
  KERNEL_FORMAT="raw"
  LIBKRUNFW_CONFIG_URL="https://raw.githubusercontent.com/smol-machines/libkrunfw/main/config-libkrunfw_aarch64"
  CROSS_COMPILE="${CROSS_COMPILE:-aarch64-linux-gnu-}"
else
  echo "ERROR: unsupported arch '$ARCH'. Supported: x86_64, arm64"
  exit 1
fi

KERNEL_VERSION="${KERNEL_VERSION:-6.12.76}"
KERNEL_TARBALL="linux-${KERNEL_VERSION}.tar.xz"
KERNEL_URL="https://cdn.kernel.org/pub/linux/kernel/v6.x/${KERNEL_TARBALL}"
BUILD_DIR="${BUILD_DIR:-/tmp/nexus-kernel-build}"
JOBS="$(nproc 2>/dev/null || echo 4)"

echo "=== Nexus kernel rebuild ==="
echo "Arch:           ${ARCH}"
echo "Kernel version: ${KERNEL_VERSION}"
echo "Output:         ${OUTPUT_PATH}"
echo "Build dir:      ${BUILD_DIR}"
echo "Parallel jobs:  ${JOBS}"
if [[ -n "$CROSS_COMPILE" ]]; then
  echo "Cross-compile:  ${CROSS_COMPILE}"
fi
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
echo "Fetching libkrunfw base config (${ARCH})..."
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
CONFIG_IP_NF_RAW=y
CONFIG_IP_NF_TARGET_MASQUERADE=y
CONFIG_IP_NF_TARGET_REJECT=y

# Matches/targets Docker uses
CONFIG_NETFILTER_XT_TABLES=y
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
if [[ -n "$CROSS_COMPILE" ]]; then
  make ARCH="${ARCH}" CROSS_COMPILE="${CROSS_COMPILE}" olddefconfig "-j${JOBS}"
else
  make olddefconfig "-j${JOBS}"
fi

# Step 5: Build kernel
echo "Building ${KERNEL_TARGET} (${ARCH})..."
if [[ -n "$CROSS_COMPILE" ]]; then
  make ARCH="${ARCH}" CROSS_COMPILE="${CROSS_COMPILE}" "${KERNEL_TARGET}" "-j${JOBS}"
else
  make "${KERNEL_TARGET}" "-j${JOBS}"
fi

# Step 6: Install
mkdir -p "$(dirname "${OUTPUT_PATH}")"
cp "${KERNEL_TARGET}" "${OUTPUT_PATH}"

# Strip debug symbols to reduce size (ELF only)
if [[ "$ARCH" == "x86_64" ]]; then
  strip --strip-debug "${OUTPUT_PATH}"
fi

echo ""
echo "=== Build complete ==="
echo "Output: ${OUTPUT_PATH}"
echo "Size:   $(du -h "${OUTPUT_PATH}" | cut -f1)"
echo "Format: ${KERNEL_FORMAT}"
if [[ "$ARCH" == "x86_64" ]]; then
  readelf -h "${OUTPUT_PATH}" | head -5
else
  file "${OUTPUT_PATH}"
fi
echo ""
echo "Remember to commit the updated kernel:"
echo "  git add ${OUTPUT_PATH}"
echo "  git commit -m 'chore(kernel): rebuild ${KERNEL_TARGET} ${KERNEL_VERSION} ${ARCH}'"
