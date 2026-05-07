#!/usr/bin/env bash
# Run the full (or filtered) e2e test suite locally on a Linux machine.
# Mirrors what GitHub Actions CI does: SSH bootstrap + host config fixtures +
# sudo -E go test.
#
# Usage:
#   ./scripts/local/e2e-local.sh
#   E2E_RUN=TestVMProof_HostConfigDrive ./scripts/local/e2e-local.sh
#   E2E_PKG=vmproof E2E_TIMEOUT=20m ./scripts/local/e2e-local.sh
#
# Environment variables:
#   E2E_P         — -p value (package parallelism, default: 2)
#   E2E_PARALLEL  — -parallel value (test-level parallelism, default: 2)
#   E2E_TIMEOUT   — -timeout value (default: 60m)
#   E2E_RUN       — -run regex filter (default: all)
#   E2E_PKG       — package path suffix under test/e2e/ (default: ... = all)
#                   e.g. "vmproof", "workspace", "cli"
#   SKIP_FIXTURES — set to 1 to skip setup-host-config-fixtures.sh
#   SKIP_BOOTSTRAP— set to 1 to skip e2e-ssh-bootstrap.sh (loopback SSH)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT/packages/nexus"

E2E_P="${E2E_P:-2}"
E2E_PARALLEL="${E2E_PARALLEL:-2}"
E2E_TIMEOUT="${E2E_TIMEOUT:-60m}"
E2E_RUN="${E2E_RUN:-}"
E2E_PKG="${E2E_PKG:-...}"
SKIP_FIXTURES="${SKIP_FIXTURES:-0}"
SKIP_BOOTSTRAP="${SKIP_BOOTSTRAP:-0}"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "e2e-local: must be run on Linux (libkrun VMs require KVM)" >&2
  exit 1
fi

# ── SSH bootstrap (loopback tunnel for NEXUS_E2E_REMOTE_PROFILE) ──────────────
if [[ "$SKIP_BOOTSTRAP" == "1" ]]; then
  echo "e2e-local: skipping SSH bootstrap (SKIP_BOOTSTRAP=1)"
else
  echo "==> e2e-ssh-bootstrap"
  # shellcheck source=/dev/null
  source "$ROOT/scripts/ci/e2e-ssh-bootstrap.sh"
fi

# ── Host config fixtures (gitconfig, SSH keys, API key env) ───────────────────
if [[ "$SKIP_FIXTURES" == "1" ]]; then
  echo "e2e-local: skipping host config fixtures (SKIP_FIXTURES=1)"
else
  echo "==> setup-host-config-fixtures"
  "$ROOT/scripts/ci/setup-host-config-fixtures.sh"
fi

# ── Locate VM assets ──────────────────────────────────────────────────────────
detect_file() {
  local p
  for p in "$@"; do
    [[ -f "$p" ]] && { echo "$p"; return 0; }
  done
  return 1
}

kernel="${NEXUS_VM_KERNEL:-}"
rootfs="${NEXUS_VM_ROOTFS:-}"

if [[ -z "$kernel" ]]; then
  kernel=$(detect_file \
    /data/nexus/vm/vmlinux.bin \
    /var/lib/nexus/vmlinux.bin \
    "$HOME/.local/share/nexus/vm/vmlinux.bin" \
    "$ROOT/packages/nexus/cmd/nexus/assets/vmlinux" || true)
fi

if [[ -z "$rootfs" ]]; then
  rootfs=$(detect_file \
    /data/nexus/vm/rootfs.ext4 \
    /var/lib/nexus/rootfs.ext4 \
    "$HOME/.local/share/nexus/vm/rootfs.ext4" || true)
fi

if [[ -z "$kernel" || -z "$rootfs" ]]; then
  echo "==> VM assets not found; running 'nexus setup'..."
  go run ./cmd/nexus setup
  kernel=$(detect_file \
    /data/nexus/vm/vmlinux.bin \
    /var/lib/nexus/vmlinux.bin \
    "$HOME/.local/share/nexus/vm/vmlinux.bin" \
    "$ROOT/packages/nexus/cmd/nexus/assets/vmlinux" || true)
  rootfs=$(detect_file \
    /data/nexus/vm/rootfs.ext4 \
    /var/lib/nexus/rootfs.ext4 \
    "$HOME/.local/share/nexus/vm/rootfs.ext4" || true)
fi

if [[ -z "$kernel" || -z "$rootfs" ]]; then
  echo "e2e-local: VM assets still missing after setup" >&2
  exit 3
fi

export NEXUS_VM_KERNEL="$kernel"
export NEXUS_VM_ROOTFS="$rootfs"

# ── Build run args ─────────────────────────────────────────────────────────────
pkg="./test/e2e/$E2E_PKG"
run_args=()
if [[ -n "$E2E_RUN" ]]; then
  run_args=(-run "$E2E_RUN")
fi

echo "==> running e2e tests"
echo "    pkg=$pkg p=$E2E_P parallel=$E2E_PARALLEL timeout=$E2E_TIMEOUT${E2E_RUN:+ run=$E2E_RUN}"
echo "    kernel=$kernel"
echo "    rootfs=$rootfs"
echo ""

# Mirror CI: sudo -E with explicit env forwarding so the daemon gets the same
# HOME (and API keys, SSH identity, etc.) as the current user.
sudo -E env \
  "PATH=$PATH" \
  "HOME=$HOME" \
  "NEXUS_VM_KERNEL=$NEXUS_VM_KERNEL" \
  "NEXUS_VM_ROOTFS=$NEXUS_VM_ROOTFS" \
  "NEXUS_E2E_REMOTE_PROFILE=${NEXUS_E2E_REMOTE_PROFILE:-1}" \
  "NEXUS_E2E_SSH_IDENTITY=${NEXUS_E2E_SSH_IDENTITY:-}" \
  "NEXUS_E2E_SSH_HOST=${NEXUS_E2E_SSH_HOST:-${USER:-runner}@127.0.0.1}" \
  "CI=true" \
  go test -tags e2e -count=1 \
    -timeout="$E2E_TIMEOUT" \
    -p "$E2E_P" -parallel "$E2E_PARALLEL" \
    -v \
    "${run_args[@]}" \
    "$pkg"
