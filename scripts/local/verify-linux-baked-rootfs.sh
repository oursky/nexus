#!/usr/bin/env bash
# Offline verification for a Linux libkrun host rootfs image after `vm bake` / CI bake.
# Requires no root when the image is user-readable (uses e2fsck -n).
#
# Checks:
#   - ext4 consistency (e2fsck -n; exit ≤ 2 matches shrink/release tooling)
#   - bake stamp path exists (debugfs stat)
#   - stamp contents contain "ok" (matches guest agent WriteFile stamp)
#
# Keep NEXUS_TOOLS_STAMP_SUFFIX in sync with packages/nexus/cmd/nexus-guest-agent/sysconfig.go
# (const stampFile = "/var/lib/nexus-tools-base-v19") and libkrun BakeStampVersion.
#
# Usage:
#   bash scripts/local/verify-linux-baked-rootfs.sh [/path/to/rootfs.ext4]
#   NEXUS_VERIFY_LINUX_ROOTFS=/path/to/rootfs.ext4 bash scripts/local/verify-linux-baked-rootfs.sh
#
# Boot-level verification (KVM + sudo as in CI — mirrors scripts/local/e2e-local.sh):
#   bash scripts/ci/build-passt.sh
#   NEXUS_VM_ROOTFS=/path/to/rootfs.ext4 \
#     E2E_RUN=TestVMProof_GuestCLITools E2E_PKG=vmproof E2E_TIMEOUT=45m \
#     bash scripts/local/e2e-local.sh
set -euo pipefail

NEXUS_TOOLS_STAMP_SUFFIX="${NEXUS_TOOLS_STAMP_SUFFIX:-v19}"
STAMP_PATH="/var/lib/nexus-tools-base-${NEXUS_TOOLS_STAMP_SUFFIX}"

IMG="${NEXUS_VERIFY_LINUX_ROOTFS:-${1:-}}"
if [[ -z "$IMG" ]]; then
  IMG="${HOME}/.local/share/nexus/vm/rootfs.ext4"
fi

if [[ ! -f "$IMG" ]]; then
  echo "verify-linux-baked-rootfs: not found: $IMG" >&2
  exit 2
fi

for cmd in e2fsck debugfs; do
  command -v "$cmd" >/dev/null 2>&1 || {
    echo "verify-linux-baked-rootfs: missing command: $cmd (install e2fsprogs)" >&2
    exit 2
  }
done

echo "verify-linux-baked-rootfs: image=$IMG"

run_e2fsck_readonly() {
  local rc
  set +e
  e2fsck -n "$IMG" >/dev/null 2>&1
  rc=$?
  set -e
  if [[ "$rc" -gt 2 ]]; then
    echo "verify-linux-baked-rootfs: e2fsck -n failed (exit $rc); run e2fsck -f -y on a copy or rebake" >&2
    return "$rc"
  fi
  return 0
}

run_e2fsck_readonly
echo "verify-linux-baked-rootfs: e2fsck -n ok (exit ≤ 2)"

stat_out="$(debugfs -R "stat ${STAMP_PATH}" "$IMG" 2>&1)" || true
lower="$(printf '%s' "$stat_out" | tr '[:upper:]' '[:lower:]')"
if [[ "$lower" == *"not found"* ]]; then
  echo "verify-linux-baked-rootfs: missing bake stamp ${STAMP_PATH} inside image." >&2
  echo "  (Expected after a full bake with guest agent matching suffix ${NEXUS_TOOLS_STAMP_SUFFIX}.)" >&2
  exit 3
fi

stamp_body="$(debugfs -R "cat ${STAMP_PATH}" "$IMG" 2>/dev/null)"
if [[ "$(printf '%s' "$stamp_body" | tr -d '\r')" != *"ok"* ]]; then
  echo "verify-linux-baked-rootfs: stamp file exists but expected 'ok' marker; got:" >&2
  printf '%s\n' "$stamp_body" >&2
  exit 4
fi

echo "verify-linux-baked-rootfs: bake stamp ${STAMP_PATH} ok"
echo "verify-linux-baked-rootfs: all offline checks passed"
