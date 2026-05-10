#!/usr/bin/env bash
set -euo pipefail

# Rebuild the x86_64 vmlinux kernel on the remote Linux host and copy it back locally.
#
# The kernel is committed at packages/nexus/cmd/nexus/assets/vmlinux and
# embedded in the nexus binary via //go:embed. Run this when you need to
# update the kernel version or config, then commit the result.
#
# Required env:
#   REMOTE_HOST  - user@hostname of the Linux build host
#
# Optional env:
#   KERNEL_VERSION - kernel version to build (default: 6.12.76)
#
# Usage:
#   REMOTE_HOST=user@host scripts/remote/rebuild-kernel.sh
#
# After this script completes, commit the updated kernel:
#   git add packages/nexus/cmd/nexus/assets/vmlinux
#   git commit -m "chore(kernel): rebuild vmlinux ${KERNEL_VERSION}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is required}"
KERNEL_VERSION="${KERNEL_VERSION:-6.12.76}"
OUTPUT_PATH="$ROOT_DIR/packages/nexus/cmd/nexus/assets/vmlinux"
REMOTE_SCRIPT_PATH="/tmp/nexus-build-kernel.sh"
REMOTE_OUTPUT="/tmp/nexus-kernel-build/vmlinux-${KERNEL_VERSION}"

echo "==> Rebuilding x86_64 kernel ${KERNEL_VERSION} on ${REMOTE_HOST}..."
echo ""

# Upload the build script to the remote host
scp -q "$ROOT_DIR/scripts/nexus/build-kernel.sh" "${REMOTE_HOST}:${REMOTE_SCRIPT_PATH}"

# Run the build remotely inside Docker (avoids host dependency issues like missing libelf-dev)
ssh "${REMOTE_HOST}" \
  "set -e; \
   echo 'Starting Docker-based kernel build...'; \
   rm -f ${REMOTE_OUTPUT}; \
   docker run --rm \
     -v /tmp/nexus-kernel-build:/build \
     -v ${REMOTE_SCRIPT_PATH}:/build-kernel.sh:ro \
     ubuntu:24.04 \
     bash -c 'set -e; \
       apt-get update -qq >/dev/null; \
       apt-get install -y -qq libelf-dev bc bison flex libssl-dev gcc make curl >/dev/null; \
       export KERNEL_VERSION=${KERNEL_VERSION}; \
       export BUILD_DIR=/build; \
       bash /build-kernel.sh --arch x86_64 /build/vmlinux-${KERNEL_VERSION}'; \
   rm -f ${REMOTE_SCRIPT_PATH}"

echo ""
echo "==> Copying kernel back to ${OUTPUT_PATH}..."
scp "${REMOTE_HOST}:${REMOTE_OUTPUT}" "${OUTPUT_PATH}"

# Verify it's a valid ELF
MAGIC=$(xxd -l 4 -p "${OUTPUT_PATH}" 2>/dev/null || true)
if [[ "$MAGIC" != "7f454c46" ]]; then
  echo "ERROR: Copied file is not a valid ELF binary."
  exit 1
fi

echo ""
echo "=== Done ==="
echo "Kernel: ${OUTPUT_PATH}"
echo "Size:   $(du -h "${OUTPUT_PATH}" | cut -f1)"
echo ""
echo "Next steps:"
echo "  git add ${OUTPUT_PATH}"
echo "  git commit -m 'chore(kernel): rebuild vmlinux ${KERNEL_VERSION} x86_64'"
