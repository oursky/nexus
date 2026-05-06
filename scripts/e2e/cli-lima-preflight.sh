#!/usr/bin/env bash
set -euo pipefail

LIMA_INSTANCE="${NEXUS_LIMA_INSTANCE:-nexus-libkrun}"
GUEST_ROOT="${NEXUS_GUEST_REPO_ROOT:-/Users/newman/magic/nexus}"
DEFAULT_VM_KERNEL="${GUEST_ROOT}/packages/nexus/cmd/nexus/assets/vmlinux"
DEFAULT_VM_ROOTFS="/var/lib/nexus/rootfs.ext4"
VM_KERNEL="${NEXUS_VM_KERNEL:-$DEFAULT_VM_KERNEL}"
VM_ROOTFS="${NEXUS_VM_ROOTFS:-$DEFAULT_VM_ROOTFS}"

status="READY"
declare -a failures=()

fail_check() {
  local code="$1"
  local observed="$2"
  local expected="$3"
  local remediation="$4"

  if [[ "$status" == "READY" ]]; then
    status="$code"
  elif [[ "$status" != "$code" ]]; then
    status="MISCONFIGURED"
  fi

  failures+=("check=${code} observed=${observed} expected=${expected} remediation=${remediation}")
}

if ! command -v limactl >/dev/null 2>&1; then
  echo "STATUS:MISCONFIGURED"
  echo "check=limactl observed=missing expected=limactl_on_host remediation=Install Lima and ensure limactl is in PATH"
  exit 1
fi

if ! limactl list 2>/dev/null | grep -q "${LIMA_INSTANCE}"; then
  echo "STATUS:MISCONFIGURED"
  echo "check=lima_instance observed=missing expected=instance:${LIMA_INSTANCE} remediation=Run 'limactl start --name=${LIMA_INSTANCE} -y template://ubuntu'"
  exit 1
fi

if ! limactl list 2>/dev/null | grep "${LIMA_INSTANCE}" | grep -q "Running"; then
  echo "STATUS:BOOTSTRAP_FAILED"
  echo "check=lima_instance_state observed=stopped expected=running remediation=Run 'limactl start --name=${LIMA_INSTANCE} -y'"
  exit 1
fi

if [[ "${NEXUS_E2E_REMOTE_PROFILE:-}" != "1" ]]; then
  fail_check "MISCONFIGURED" "NEXUS_E2E_REMOTE_PROFILE=${NEXUS_E2E_REMOTE_PROFILE:-unset}" "NEXUS_E2E_REMOTE_PROFILE=1" "Export NEXUS_E2E_REMOTE_PROFILE=1 to enable full-stack CLI E2E path"
fi

if [[ -z "${NEXUS_VM_KERNEL:-}" ]]; then
  echo "info: NEXUS_VM_KERNEL unset, probing default ${DEFAULT_VM_KERNEL}" >&2
fi

if [[ -z "${NEXUS_VM_ROOTFS:-}" ]]; then
  echo "info: NEXUS_VM_ROOTFS unset, probing default ${DEFAULT_VM_ROOTFS}" >&2
fi

guest_script='set -euo pipefail; missing=0; command -v go >/dev/null 2>&1 || { echo "guest:go=missing"; missing=1; }; test -d "'"${GUEST_ROOT}"'"/packages/nexus || { echo "guest:repo=missing:'"${GUEST_ROOT}"'/packages/nexus"; missing=1; }; if [[ ! -f "'"${VM_KERNEL}"'" ]]; then echo "guest:kernel=missing:'"${VM_KERNEL}"'"; missing=1; fi; if [[ ! -f "'"${VM_ROOTFS}"'" ]]; then echo "guest:rootfs=missing:'"${VM_ROOTFS}"'"; missing=1; fi; if [[ "$missing" -ne 0 ]]; then exit 7; fi'

set +e
guest_out="$(limactl shell "${LIMA_INSTANCE}" -- bash -lc "${guest_script}" 2>&1)"
guest_rc=$?
set -e

if [[ $guest_rc -ne 0 ]]; then
  if [[ "$guest_out" == *"guest:kernel=missing"* || "$guest_out" == *"guest:rootfs=missing"* || "$guest_out" == *"guest:repo=missing"* || "$guest_out" == *"guest:go=missing"* ]]; then
    fail_check "MISCONFIGURED" "${guest_out//$'\n'/; }" "guest_paths_and_tools_present" "Install guest dependencies and ensure paths are guest-visible; if rootfs is missing run scripts/ci/seed-rootfs-dir-for-runner.sh"
  else
    fail_check "BOOTSTRAP_FAILED" "lima_shell_exit=${guest_rc}" "guest_shell_access_ok" "Verify limactl shell access and guest health"
  fi
fi

guest_os="$(limactl shell "${LIMA_INSTANCE}" -- bash -lc 'uname -s' 2>/dev/null || true)"
if [[ "$guest_os" != "Linux" ]]; then
  fail_check "UNSUPPORTED_HOST" "guest_os=${guest_os:-unknown}" "guest_os=Linux" "Use a Linux Lima guest for VM e2e tests"
fi

if [[ "$status" == "READY" ]]; then
  echo "STATUS:READY"
  echo "NEXUS_VM_KERNEL=${VM_KERNEL}"
  echo "NEXUS_VM_ROOTFS=${VM_ROOTFS}"
  exit 0
fi

echo "STATUS:${status}"
for f in "${failures[@]}"; do
  echo "$f"
done
exit 1
