#!/usr/bin/env bash
# Pre-bake libkrun host base rootfs and write dist/rootfs-linux-amd64.ext4.gz (+ .sha256).
# Prerequisites (run first): enable-kvm, install-host-prereqs, setup-xfs-reflink,
# prepare + hydrate libkrun cache (optional), build-nexus-libkrun.sh, build-passt.sh,
# provision-libkrun-host.sh, verify-vm-assets.sh required.
set -euo pipefail

export NEXUS_PASST_PATH="${NEXUS_PASST_PATH:-/tmp/passt-patched}"
BAKE_TIMEOUT="${NEXUS_RELEASE_ROOTFS_BAKE_TIMEOUT:-55m}"

rm -f "${HOME}/.local/state/nexus"/rootfs-baked-v* 2>/dev/null || true

/tmp/nexus-bin vm bake --timeout "${BAKE_TIMEOUT}"

ROOTFS="${HOME}/.local/share/nexus/vm/rootfs.ext4"
if [ ! -f "$ROOTFS" ]; then
  echo "ERROR: missing baked rootfs at $ROOTFS" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${GITHUB_WORKSPACE:-$REPO_ROOT}/dist"
mkdir -p "$OUT"

echo "Compressing host rootfs → ${OUT}/rootfs-linux-amd64.ext4.gz ..."
gzip -9 -c "$ROOTFS" > "${OUT}/rootfs-linux-amd64.ext4.gz"
ls -lh "${OUT}/rootfs-linux-amd64.ext4.gz"
( cd "$OUT" && sha256sum rootfs-linux-amd64.ext4.gz | tee rootfs-linux-amd64.ext4.gz.sha256 )
