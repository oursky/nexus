#!/usr/bin/env bash
set -euo pipefail

# Run the full e2e test suite (all packages) on a remote Linux host.
# Uses the same environment wiring as e2e-cli.sh but runs ./test/e2e/...
# with configurable parallelism.
#
# Usage:
#   REMOTE_HOST=user@host ./scripts/remote/e2e-full.sh
#   E2E_P=1 E2E_PARALLEL=1 REMOTE_HOST=user@host ./scripts/remote/e2e-full.sh
#
# Environment variables:
#   REMOTE_HOST       — required: SSH target (user@host)
#   REMOTE_REPO_ROOT  — optional: path to nexus repo on remote (default: ~/magic/nexus)
#   REMOTE_BIN        — optional: pre-built nexus binary on remote (default: ~/.local/bin/nexus)
#   E2E_P             — optional: -p value (package parallelism, default: 2)
#   E2E_PARALLEL      — optional: -parallel value (test parallelism, default: 2)
#   E2E_TIMEOUT       — optional: -timeout value (default: 60m)
#   E2E_RUN           — optional: -run regex to filter tests (default: all)
#   E2E_PKG           — optional: package path under test/e2e/ (default: ... = all)

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set}"
REMOTE_REPO_ROOT="${REMOTE_REPO_ROOT:-~/magic/nexus}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"
E2E_P="${E2E_P:-2}"
E2E_PARALLEL="${E2E_PARALLEL:-2}"
E2E_TIMEOUT="${E2E_TIMEOUT:-60m}"
E2E_RUN="${E2E_RUN:-}"
E2E_PKG="${E2E_PKG:-...}"

ssh -A -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${REMOTE_HOST}" "bash -lc '
  set -euo pipefail
  cd ${REMOTE_REPO_ROOT}/packages/nexus

  # The CI bootstrap installs openssh-server via apt-get. On developer machines
  # sshd is already present; skip the install and set vars manually below.
  export NEXUS_E2E_SKIP_SSH_BOOTSTRAP=1
  if [[ -f ../../scripts/ci/e2e-ssh-bootstrap.sh ]]; then
    NEXUS_E2E_SKIP_SSH_BOOTSTRAP=1 source ../../scripts/ci/e2e-ssh-bootstrap.sh
  fi
  # NEXUS_E2E_REMOTE_PROFILE and NEXUS_E2E_SSH_IDENTITY are exported by
  # e2e-ssh-bootstrap.sh when it succeeds. Only fall back to remote-profile
  # mode if the bootstrap did not set it (non-Linux or skipped).
  export NEXUS_E2E_REMOTE_PROFILE=\${NEXUS_E2E_REMOTE_PROFILE:-1}
  # On a developer machine the bootstrap may be skipped (e.g. no passwordless
  # sudo). Fall back to the pre-existing loopback key if present.
  if [[ -z "\${NEXUS_E2E_SSH_IDENTITY:-}" ]]; then
    for _k in \$HOME/.ssh/id_nexus_e2e /tmp/nexus-e2e-ci-ed25519; do
      if [[ -f "\$_k" ]]; then
        export NEXUS_E2E_SSH_IDENTITY="\$_k"
        break
      fi
    done
  fi
  if [[ -z "\${NEXUS_E2E_SSH_IDENTITY:-}" ]]; then
    echo "remote e2e: no SSH identity for loopback tunnel found" >&2
    exit 4
  fi
  # Ensure the profile tunnel targets loopback.
  export NEXUS_E2E_SSH_HOST="\${NEXUS_E2E_SSH_HOST:-\${USER:-runner}@127.0.0.1}"

  run_go() {
    if command -v mise >/dev/null 2>&1; then
      mise exec -- go "\$@"
      return
    fi
    if command -v go >/dev/null 2>&1; then
      go "\$@"
      return
    fi
    echo remote e2e: go toolchain not found need mise_or_go_in_path >&2
    exit 2
  }

  detect_file() {
    local p
    for p in "\$@"; do
      [[ -f "\$p" ]] && { echo "\$p"; return 0; }
    done
    return 1
  }

  kernel=\${NEXUS_VM_KERNEL:-}
  rootfs=\${NEXUS_VM_ROOTFS:-}
  if [[ -z "\$kernel" ]]; then
    kernel=\$(detect_file /data/nexus/vm/vmlinux.bin /var/lib/nexus/vmlinux.bin \$HOME/.local/share/nexus/vm/vmlinux.bin ${REMOTE_REPO_ROOT}/packages/nexus/cmd/nexus/assets/vmlinux || true)
  fi
  if [[ -z "\$rootfs" ]]; then
    rootfs=\$(detect_file /data/nexus/vm/rootfs.ext4 /var/lib/nexus/rootfs.ext4 \$HOME/.local/share/nexus/vm/rootfs.ext4 || true)
  fi

  if [[ -z "\$rootfs" || -z "\$kernel" ]]; then
    run_go run ./cmd/nexus setup
    kernel=\$(detect_file /data/nexus/vm/vmlinux.bin /var/lib/nexus/vmlinux.bin \$HOME/.local/share/nexus/vm/vmlinux.bin ${REMOTE_REPO_ROOT}/packages/nexus/cmd/nexus/assets/vmlinux || true)
    rootfs=\$(detect_file /data/nexus/vm/rootfs.ext4 /var/lib/nexus/rootfs.ext4 \$HOME/.local/share/nexus/vm/rootfs.ext4 || true)
  fi

  if [[ -z "\$kernel" || -z "\$rootfs" ]]; then
    echo remote e2e: VM assets still missing after setup >&2
    exit 3
  fi

  export NEXUS_VM_KERNEL="\$kernel"
  export NEXUS_VM_ROOTFS="\$rootfs"
  if [[ -x ${REMOTE_BIN} ]]; then
    export NEXUS_E2E_BINARY=${REMOTE_BIN}
  fi
  # CI=true tells tunnel.go to add BatchMode=yes + StrictHostKeyChecking=no so
  # the loopback SSH tunnel works without a TTY (no passphrase prompt).
  export CI=true

  # Build run filter flag if E2E_RUN is set.
  run_flag=""
  if [[ -n "${E2E_RUN}" ]]; then
    run_flag="-run ${E2E_RUN}"
  fi

  pkg="./test/e2e/${E2E_PKG}"

  echo "==> running: go test \$pkg -tags=e2e -count=1 -p ${E2E_P} -parallel ${E2E_PARALLEL} -timeout ${E2E_TIMEOUT} -v \$run_flag"
  run_go test "\$pkg" -tags=e2e -count=1 \
    -p ${E2E_P} -parallel ${E2E_PARALLEL} \
    -timeout ${E2E_TIMEOUT} \
    -v \${run_flag}
'"
