#!/usr/bin/env bash
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set}"
REMOTE_REPO_ROOT="${REMOTE_REPO_ROOT:-~/magic/nexus}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"

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
  # NEXUS_E2E_SSH_IDENTITY is set by e2e-ssh-bootstrap.sh (sourced above).
  # CI=true tells tunnel.go to add BatchMode=yes + StrictHostKeyChecking=no so
  # the loopback SSH tunnel works without a TTY (no passphrase prompt).
  export CI=true

  run_go test ./test/e2e/cli -tags=e2e -count=1 -v -timeout 30m
'"
