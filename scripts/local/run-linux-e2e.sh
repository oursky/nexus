#!/usr/bin/env bash
# Run Linux libkrun E2E on this machine (SSH to a remote host or work directly
# on a Linux dev box). Mirrors .github/workflows/ci.yml `e2e` job env; always
# cleans up daemons, passt, and workspace dirs on EXIT.
#
# Prerequisites (typical):
#   KVM + e2fsprogs + tools from scripts/ci/install-host-prereqs.sh
#   XFS/reflink for /data/nexus (see scripts/ci/setup-xfs-reflink.sh) or host
#   kernel/rootfs under /var/lib/nexus or paths detected below
#
# Usage:
#   ./scripts/local/run-linux-e2e.sh
#   E2E_PREPROVISION=1 ./scripts/local/run-linux-e2e.sh   # provision kernel+rootfs first
#
# Environment:
#   E2E_PREPROVISION     — set to 1 to run scripts/ci/provision-libkrun-host.sh
#   E2E_SKIP_BOOTSTRAP   — set to 1 to skip SSH loopback bootstrap
#   E2E_SKIP_FIXTURES    — set to 1 to skip host-config fixtures
#   NEXUS_E2E_LINUX_PACKAGES — space-separated test packages (default: ./test/e2e/...)
#   GO_RUN, GO_P, GO_PARALLEL, GO_TIMEOUT — optional test flags
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PKG_ROOT="$ROOT/packages/nexus"
cd "$PKG_ROOT"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "run-linux-e2e: must run on Linux (libkrun / KVM)." >&2
  exit 1
fi

disk_report() {
  local phase="$1"
  echo ""
  echo "=== disk ($phase) ==="
  df -h
  echo ""
}

cleanup() {
  echo ""
  echo "==> run-linux-e2e cleanup (EXIT trap)"
  ssh-agent -k 2>/dev/null || true

  pkill -TERM -f '[n]exus-libkrun-vm' 2>/dev/null || true
  pkill -TERM -f '[n]exusd' 2>/dev/null || true
  pkill -TERM -f 'nexus daemon' 2>/dev/null || true
  pkill -TERM -f '[p]asst' 2>/dev/null || true
  sleep 1
  pkill -KILL -f '[n]exus-libkrun-vm' 2>/dev/null || true
  pkill -KILL -f '[n]exusd' 2>/dev/null || true
  pkill -KILL -f '[p]asst' 2>/dev/null || true

  shopt -s nullglob
  sudo rm -rf /tmp/nexus-* 2>/dev/null || rm -rf /tmp/nexus-* 2>/dev/null || true
  shopt -u nullglob

  for d in /data/nexus/e2e /data/nexus/e2e-tui \
    /data/nexus/libkrun-vms /data/nexus/libkrun-vms-e2e \
    /data/nexus/libkrun-vms-provision /data/nexus/libkrun-vms-prewarm \
    /data/nexus/libkrun-vms-tui-e2e \
    "${HOME}/.local/share/nexus/libkrun-vms"; do
    if [[ -d "$d" ]] && [[ -w "$d" ]]; then
      rm -rf "${d:?}"/* 2>/dev/null || true
    elif [[ -d "$d" ]]; then
      sudo rm -rf "${d:?}"/* 2>/dev/null || true
    fi
  done

  disk_report "after cleanup"
}

trap cleanup EXIT

disk_report "before"

# ── Mirror CI e2e job env (.github/workflows/ci.yml) ──────────────────────────
export CI="${CI:-true}"
export NEXUS_LIBKRUN_SKIP_BAKE="${NEXUS_LIBKRUN_SKIP_BAKE:-1}"
export NEXUS_LIBKRUN_BAKE_TIMEOUT="${NEXUS_LIBKRUN_BAKE_TIMEOUT:-120s}"
export NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS="${NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS:-1}"
export NEXUS_VM_KERNEL="${NEXUS_VM_KERNEL:-/var/lib/nexus/vmlinux.bin}"
export NEXUS_VM_ROOTFS="${NEXUS_VM_ROOTFS:-/var/lib/nexus/rootfs.ext4}"
export NEXUS_WORKSPACE_IMAGE_MIN_MIB="${NEXUS_WORKSPACE_IMAGE_MIN_MIB:-128}"
export NEXUS_RUNNER_ROOTFS_MIN_MIB="${NEXUS_RUNNER_ROOTFS_MIN_MIB:-128}"
export NEXUS_LIBKRUN_MEM_MIB="${NEXUS_LIBKRUN_MEM_MIB:-256}"
export NEXUS_PASST_PATH="${NEXUS_PASST_PATH:-/tmp/passt-patched}"

detect_file() {
  local p
  for p in "$@"; do
    [[ -f "$p" ]] && { echo "$p"; return 0; }
  done
  return 1
}

if [[ ! -f "$NEXUS_VM_KERNEL" ]]; then
  k="$(detect_file \
    /data/nexus/vm/vmlinux.bin \
    /var/lib/nexus/vmlinux.bin \
    "$HOME/.local/share/nexus/vm/vmlinux.bin" \
    "$PKG_ROOT/cmd/nexus/assets/vmlinux" || true)"
  [[ -n "$k" ]] && NEXUS_VM_KERNEL="$k"
fi
if [[ ! -f "$NEXUS_VM_ROOTFS" ]]; then
  r="$(detect_file \
    /data/nexus/vm/rootfs.ext4 \
    /var/lib/nexus/rootfs.ext4 \
    "$HOME/.local/share/nexus/vm/rootfs.ext4" || true)"
  [[ -n "$r" ]] && NEXUS_VM_ROOTFS="$r"
fi

export NEXUS_VM_KERNEL NEXUS_VM_ROOTFS

if [[ "${E2E_PREPROVISION:-0}" == "1" ]]; then
  echo "==> provision-libkrun-host"
  bash "$ROOT/scripts/ci/provision-libkrun-host.sh"
fi

if [[ ! -f "$NEXUS_VM_KERNEL" || ! -f "$NEXUS_VM_ROOTFS" ]]; then
  echo "==> VM assets missing; trying 'go run ./cmd/nexus setup'"
  go run ./cmd/nexus setup
  if [[ ! -f "$NEXUS_VM_KERNEL" ]]; then
    k="$(detect_file \
      /data/nexus/vm/vmlinux.bin \
      /var/lib/nexus/vmlinux.bin \
      "$HOME/.local/share/nexus/vm/vmlinux.bin" \
      "$PKG_ROOT/cmd/nexus/assets/vmlinux" || true)"
    [[ -n "$k" ]] && NEXUS_VM_KERNEL="$k" && export NEXUS_VM_KERNEL
  fi
  if [[ ! -f "$NEXUS_VM_ROOTFS" ]]; then
    r="$(detect_file \
      /data/nexus/vm/rootfs.ext4 \
      /var/lib/nexus/rootfs.ext4 \
      "$HOME/.local/share/nexus/vm/rootfs.ext4" || true)"
    [[ -n "$r" ]] && NEXUS_VM_ROOTFS="$r" && export NEXUS_VM_ROOTFS
  fi
fi

if [[ ! -f "$NEXUS_VM_KERNEL" || ! -f "$NEXUS_VM_ROOTFS" ]]; then
  echo "run-linux-e2e: kernel or rootfs not found; set NEXUS_VM_KERNEL / NEXUS_VM_ROOTFS or run E2E_PREPROVISION=1" >&2
  exit 3
fi

if [[ ! -x "$NEXUS_PASST_PATH" ]]; then
  echo "==> building patched passt → $NEXUS_PASST_PATH"
  bash "$ROOT/scripts/ci/build-passt.sh"
fi

if [[ "${E2E_SKIP_BOOTSTRAP:-0}" != "1" ]]; then
  echo "==> e2e-ssh-bootstrap"
  # shellcheck source=/dev/null
  source "$ROOT/scripts/ci/e2e-ssh-bootstrap.sh"
fi

if [[ "${E2E_SKIP_FIXTURES:-0}" != "1" ]]; then
  echo "==> setup-host-config-fixtures"
  bash "$ROOT/scripts/ci/setup-host-config-fixtures.sh"
fi

eval "$(ssh-agent -s)"
KEY="${NEXUS_E2E_SSH_IDENTITY:-/tmp/nexus-e2e-ci-ed25519}"
if [[ -f "$KEY" ]]; then
  ssh-add "$KEY"
fi

GO_P="${GO_P:-2}"
GO_PARALLEL="${GO_PARALLEL:-2}"
GO_TIMEOUT="${GO_TIMEOUT:-60m}"
go_run_args=()
if [[ -n "${GO_RUN:-}" ]]; then
  go_run_args=(-run "$GO_RUN")
fi

if [[ -n "${NEXUS_E2E_LINUX_PACKAGES:-}" ]]; then
  read -ra PACKAGES <<< "$NEXUS_E2E_LINUX_PACKAGES"
else
  PACKAGES=(./test/e2e/...)
fi

echo "==> go test e2e"
echo "    kernel=$NEXUS_VM_KERNEL rootfs=$NEXUS_VM_ROOTFS passt=$NEXUS_PASST_PATH"
echo "    packages=${PACKAGES[*]} p=$GO_P parallel=$GO_PARALLEL timeout=$GO_TIMEOUT"
echo ""

echo "==> go generate ./cmd/nexus/"
go generate ./cmd/nexus/

sudo -E env \
  "PATH=$PATH" \
  "HOME=$HOME" \
  "NEXUS_VM_KERNEL=$NEXUS_VM_KERNEL" \
  "NEXUS_VM_ROOTFS=$NEXUS_VM_ROOTFS" \
  "NEXUS_PASST_PATH=$NEXUS_PASST_PATH" \
  "NEXUS_LIBKRUN_SKIP_BAKE=$NEXUS_LIBKRUN_SKIP_BAKE" \
  "NEXUS_LIBKRUN_BAKE_TIMEOUT=$NEXUS_LIBKRUN_BAKE_TIMEOUT" \
  "NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=$NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS" \
  "NEXUS_WORKSPACE_IMAGE_MIN_MIB=$NEXUS_WORKSPACE_IMAGE_MIN_MIB" \
  "NEXUS_RUNNER_ROOTFS_MIN_MIB=$NEXUS_RUNNER_ROOTFS_MIN_MIB" \
  "NEXUS_LIBKRUN_MEM_MIB=$NEXUS_LIBKRUN_MEM_MIB" \
  "NEXUS_CROSS_BINARY_DIR=${NEXUS_CROSS_BINARY_DIR:-}" \
  "NEXUS_E2E_BINARY=${NEXUS_E2E_BINARY:-}" \
  "NEXUS_E2E_REMOTE_PROFILE=${NEXUS_E2E_REMOTE_PROFILE:-1}" \
  "NEXUS_E2E_SSH_IDENTITY=${NEXUS_E2E_SSH_IDENTITY:-}" \
  "SSH_AUTH_SOCK=${SSH_AUTH_SOCK:-}" \
  "CI=$CI" \
  go test -tags e2e -count=1 \
  -timeout="$GO_TIMEOUT" -v \
  -p "$GO_P" -parallel "$GO_PARALLEL" \
  "${go_run_args[@]}" \
  "${PACKAGES[@]}"
