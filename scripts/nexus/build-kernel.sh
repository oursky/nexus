#!/usr/bin/env bash
set -euo pipefail

# Build or download a vmlinux ELF kernel for embedding into the nexus binary.
#
# Uses libkrunfw's kernel config as a base (tuned for microVMs) and adds
# the networking options Docker needs (bridge, netfilter, iptables/nat).
# The output is a plain vmlinux ELF that libkrun loads with format=ELF.
#
# Quick-start (no toolchain required):
#   NEXUS_KERNEL_URL=https://example.com/vmlinux-6.6.59-linux-x86_64 \
#     build-kernel.sh <output-path>
#
# Build from source (requires: build-essential, libncurses-dev, bison, flex,
# libssl-dev, bc, libelf-dev):
#   FORCE_KERNEL_REBUILD=1 build-kernel.sh <output-path>
#
# Usage:
#   build-kernel.sh <output-path>
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
ARCH="$(uname -m)"

echo "=== Nexus kernel build ==="
echo "Kernel version: ${KERNEL_VERSION}"
echo "Output: ${OUTPUT_PATH}"
echo "Build dir: ${BUILD_DIR}"
echo "Parallel jobs: ${JOBS}"
echo "Arch: ${ARCH}"

# If a valid ELF already exists at the output path, skip everything.
if [[ -f "${OUTPUT_PATH}" ]]; then
  MAGIC=$(xxd -l 4 -p "${OUTPUT_PATH}" 2>/dev/null || true)
  if [[ "$MAGIC" == "7f454c46" ]]; then
    echo "Valid ELF kernel already exists at ${OUTPUT_PATH}, skipping build."
    exit 0
  fi
fi

# ── Fast path: download prebuilt kernel ─────────────────────────────────────
# If NEXUS_KERNEL_URL is set (or we can construct one), try downloading first.
# This avoids the slow source build and does not require host toolchains.
PREBUILT_URL="${NEXUS_KERNEL_URL:-}"

if [[ -z "$PREBUILT_URL" && -z "${FORCE_KERNEL_REBUILD:-}" ]]; then
  # Try to infer a GitHub release asset URL if this is a checkout of a known repo.
  # The release tag convention is: kernel-<version> (e.g. kernel-6.6.59)
  # The asset name convention is: vmlinux-<version>-linux-<arch>
  if [[ -d "$ROOT_DIR/.git" ]]; then
    REMOTE_URL="$(git -C "$ROOT_DIR" remote get-url origin 2>/dev/null || true)"
    if [[ "$REMOTE_URL" == *"github.com"* ]]; then
      # Extract owner/repo from git remote URL
      # Handles both https://github.com/owner/repo.git and git@github.com:owner/repo.git
      REPO_PATH="$(echo "$REMOTE_URL" | sed -E 's/.*github\.com[:\/]([^\/]+)\/([^\/]+)(\.git)?$/\1\/\2/')"
      if [[ "$REPO_PATH" != *"*" && -n "$REPO_PATH" ]]; then
        PREBUILT_URL="https://github.com/${REPO_PATH}/releases/download/kernel-${KERNEL_VERSION}/vmlinux-${KERNEL_VERSION}-linux-${ARCH}"
        echo "Inferred prebuilt kernel URL: ${PREBUILT_URL}"
      fi
    fi
  fi
fi

if [[ -n "$PREBUILT_URL" && -z "${FORCE_KERNEL_REBUILD:-}" ]]; then
  echo "Attempting to download prebuilt kernel..."
  mkdir -p "$(dirname "${OUTPUT_PATH}")"
  if curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
      -o "${OUTPUT_PATH}" "$PREBUILT_URL" 2>/dev/null; then
    MAGIC=$(xxd -l 4 -p "${OUTPUT_PATH}" 2>/dev/null || true)
    if [[ "$MAGIC" == "7f454c46" ]]; then
      echo "Downloaded valid prebuilt kernel."
      echo "Size: $(du -h "${OUTPUT_PATH}" | cut -f1)"
      file "${OUTPUT_PATH}"
      exit 0
    else
      echo "Downloaded file is not a valid ELF, removing..."
      rm -f "${OUTPUT_PATH}"
    fi
  else
    echo "Prebuilt kernel download failed."
  fi
  echo "Falling back to source build..."
fi

# ── Slow path: build from source ────────────────────────────────────────────
# This requires host build tools and is slow (~10-15 min).

echo "Building kernel from source..."

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
file "${OUTPUT_PATH}"
