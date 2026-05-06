#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LIMA_INSTANCE="${NEXUS_LIMA_INSTANCE:-nexus-libkrun}"
GUEST_ROOT="${NEXUS_GUEST_REPO_ROOT:-/Users/newman/magic/nexus}"

detect_guest_file() {
  local candidate
  for candidate in "$@"; do
    [[ -z "$candidate" ]] && continue
    if limactl shell "${LIMA_INSTANCE}" -- bash -lc "test -f '$candidate'" >/dev/null 2>&1; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

VM_KERNEL="${NEXUS_VM_KERNEL:-}"
if [[ -z "$VM_KERNEL" ]]; then
  VM_KERNEL="$(detect_guest_file \
    "${GUEST_ROOT}/packages/nexus/cmd/nexus/assets/vmlinux" \
    "/var/lib/nexus/vmlinux.bin" \
    "/root/.local/share/nexus/vm/vmlinux.bin" || true)"
fi

VM_ROOTFS="${NEXUS_VM_ROOTFS:-}"
if [[ -z "$VM_ROOTFS" ]]; then
  VM_ROOTFS="$(detect_guest_file \
    "/var/lib/nexus/rootfs.ext4" \
    "/root/.local/share/nexus/vm/rootfs.ext4" || true)"
fi

VM_KERNEL="${VM_KERNEL:-${GUEST_ROOT}/packages/nexus/cmd/nexus/assets/vmlinux}"
VM_ROOTFS="${VM_ROOTFS:-/var/lib/nexus/rootfs.ext4}"

export NEXUS_E2E_REMOTE_PROFILE="${NEXUS_E2E_REMOTE_PROFILE:-1}"
export NEXUS_VM_KERNEL="$VM_KERNEL"
export NEXUS_VM_ROOTFS="$VM_ROOTFS"

if ! command -v limactl >/dev/null 2>&1; then
  echo "cli-lima-test: limactl is required" >&2
  exit 1
fi

if ! limactl list 2>/dev/null | grep -q "${LIMA_INSTANCE}"; then
  limactl start --name="${LIMA_INSTANCE}" -y template://ubuntu >/dev/null
fi

if ! limactl list 2>/dev/null | grep "${LIMA_INSTANCE}" | grep -q "Running"; then
  limactl start --name="${LIMA_INSTANCE}" -y >/dev/null
fi

"${ROOT}/scripts/e2e/cli-lima-preflight.sh"

limactl shell "${LIMA_INSTANCE}" -- bash -lc '
  set -euo pipefail
  if ! command -v go >/dev/null 2>&1; then
    sudo apt-get update >/dev/null
    sudo apt-get install -y golang-go >/dev/null
  fi
  cd /Users/newman/magic/nexus
  source scripts/ci/e2e-ssh-bootstrap.sh
  cd packages/nexus
  if command -v mise >/dev/null 2>&1; then
    mise exec --     go test ./test/e2e/cli -tags=e2e -count=1 -v -timeout 30m
  else
    go test ./test/e2e/cli -tags=e2e -count=1 -v -timeout 30m
  fi
'
