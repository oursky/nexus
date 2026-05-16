#!/usr/bin/env bash
# scripts/ci/shrink-rootfs-for-release.sh
# Shrink a raw ext4 guest disk image to the smallest size that still holds the
# filesystem. Release gzip artifacts mostly reflect allocated ext4 data; trimming
# trailing unused blocks yields much smaller downloads.
#
# Consumers grow back to operational headroom at install/daemon start
# (truncate + resize2fs); see internal/infra/runtime/guestrootfs.
#
# Usage: shrink-rootfs-for-release.sh <rootfs.ext4>
set -euo pipefail

IMG="${1:?usage: shrink-rootfs-for-release.sh <rootfs.ext4>}"

for cmd in e2fsck resize2fs dumpe2fs truncate; do
  command -v "$cmd" >/dev/null 2>&1 || {
    echo "shrink-rootfs-for-release: missing required command: $cmd" >&2
    exit 1
  }
done

if [[ ! -f "$IMG" ]]; then
  echo "shrink-rootfs-for-release: not a file: $IMG" >&2
  exit 1
fi

# e2fsck exit 1/2 = repaired (OK); >2 = failure. Use -y so multiply-claimed blocks from abrupt VM shutdown can be fixed unattended.
run_e2fsck_pass() {
  local img="$1"
  local rc
  set +e
  e2fsck -f -y "$img"
  rc=$?
  set -e
  if [[ "$rc" -gt 2 ]]; then
    echo "shrink-rootfs-for-release: e2fsck failed (exit $rc)" >&2
    return "$rc"
  fi
  return 0
}

run_e2fsck_pass "$IMG"
resize2fs -M "$IMG"

bc="$(dumpe2fs -h "$IMG" 2>/dev/null | awk '/^Block count:/ {print $3}')"
bs="$(dumpe2fs -h "$IMG" 2>/dev/null | awk '/^Block size:/ {print $3}')"
if [[ -z "$bc" || -z "$bs" ]]; then
  echo "shrink-rootfs-for-release: could not read Block count / Block size from dumpe2fs" >&2
  exit 1
fi

bytes=$((bc * bs))
truncate -s "$bytes" "$IMG"
run_e2fsck_pass "$IMG"

echo "shrink-rootfs-for-release: $(basename "$IMG") → ${bytes} bytes ($(numfmt --to=iec "$bytes" 2>/dev/null || echo "${bytes} B"))"
