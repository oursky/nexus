#!/usr/bin/env bash
# Install the Nexus CLI (`nexus`) from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/oursky/nexus/main/install.sh | bash
#
# Environment:
#   GITHUB_REPOSITORY   default oursky/nexus
#   NEXUS_VERSION       release tag (e.g. v0.31.0); when unset, uses GitHub "latest" stable release
#   INSTALL_DIR         default ~/.local/bin
#   SMOLVM_VERSION      libkrun vendor tarball tag for Linux x86_64 (default v0.5.19; must match CI build-nexus-libkrun.sh)
#
# Requires: curl, sha256sum or shasum -a 256 for release checksums, gzip when installing a prebaked rootfs
# (linux/amd64 host or darwin/arm64 guest), and python3 or jq to read the releases API when NEXUS_VERSION is unset.
set -euo pipefail

GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-oursky/nexus}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
NEXUS_VERSION="${NEXUS_VERSION:-}"
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"

case "${INSTALL_DIR}" in
  ~|"~/"*) INSTALL_DIR="${INSTALL_DIR/#\~/$HOME}" ;;
esac
INSTALL_DIR="${INSTALL_DIR%/}"

die() {
  echo "nexus-install: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

verify_checksum_file() {
  local sums="$1" asset="$2"
  local line expected
  line="$(grep -F " ${asset}" "$sums" || true)"
  [ -n "$line" ] || die "no checksum line for ${asset} in checksums.txt"
  expected="$(echo "$line" | awk '{print $1}')"
  if command -v sha256sum >/dev/null 2>&1; then
    echo "$expected  ${asset}" | sha256sum -c - >/dev/null
  elif command -v shasum >/dev/null 2>&1; then
    local actual
    actual="$(shasum -a 256 "$asset" | awk '{print $1}')"
    [ "$actual" = "$expected" ] || die "checksum mismatch for ${asset}"
  else
    die "need sha256sum or shasum -a 256 to verify downloads"
  fi
}

# ── Linux libkrun runtime (smolvm) + VM helper + passt ───────────────────────
# Linux/amd64 release binaries embed libkrun + passt and extract on first boot.
# This path still provisions host dirs and fills gaps for arm64, slim, or older builds.

nexus_data_share_dir() {
  local xdg="${XDG_DATA_HOME:-}"
  if [ -z "${xdg}" ]; then
    xdg="${HOME}/.local/share"
  fi
  echo "${xdg}/nexus"
}

install_smolvm_libs_amd64() {
  local libdir="$1"
  need_cmd tar
  local ver_plain="${SMOLVM_VERSION#v}"
  local tarball="smolvm-${ver_plain}-linux-x86_64.tar.gz"
  local td ex base_smol sums_smol tball
  td="$(mktemp -d "${TMPDIR:-/tmp}/nexus-smolvm.XXXXXX")"
  cleanup() { rm -rf "${td}"; }
  base_smol="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}"
  sums_smol="${td}/checksums.sha256"
  tball="${td}/${tarball}"
  curl -fsSL -L "${base_smol}/checksums.sha256" -o "${sums_smol}"
  curl -fsSL -L "${base_smol}/${tarball}" -o "${tball}"
  (
    cd "${td}"
    verify_checksum_file "checksums.sha256" "${tarball}"
  )
  ex="${td}/extract"
  mkdir -p "${ex}"
  tar -xzf "${tball}" -C "${ex}" --strip-components=2 "smolvm-${ver_plain}-linux-x86_64/lib"
  mkdir -p "${libdir}"
  cp -a "${ex}/." "${libdir}/"
  cleanup
  echo "nexus-install: installed libkrun + libkrunfw from smolvm ${SMOLVM_VERSION} → ${libdir}"
}

install_libkrun_libs_from_host_arm64() {
  local libdir="$1"
  local krun="" fw="" kdir="" f
  if command -v ldconfig >/dev/null 2>&1; then
    krun="$(PATH="/sbin:/usr/sbin:$PATH" ldconfig -p 2>/dev/null | awk '/libkrun\.so\.1(\s|$)/ {print $NF; exit}')"
  fi
  if [ -z "${krun}" ]; then
    for f in \
      /usr/lib/aarch64-linux-gnu/libkrun.so.1 \
      /usr/lib64/libkrun.so.1 \
      /lib/aarch64-linux-gnu/libkrun.so.1; do
      if [ -e "$f" ]; then
        krun="$f"
        break
      fi
    done
  fi
  if [ -z "${krun}" ] && [ -d /nix/store ]; then
    krun="$(find /nix/store -maxdepth 5 -name 'libkrun.so.1' -type f 2>/dev/null | head -1 || true)"
  fi
  [ -n "${krun}" ] || return 1
  kdir="$(dirname "${krun}")"
  fw="$(ls -1 "${kdir}"/libkrunfw.so.* 2>/dev/null | grep -E '\.[0-9]+\.[0-9]+\.[0-9]+$' || true)" 
  fw="$(echo "${fw}" | sort -V | tail -1)"
  [ -n "${fw}" ] || return 1
  mkdir -p "${libdir}"
  cp -L "${krun}" "${libdir}/libkrun.so"
  rm -f "${libdir}/libkrun.so.1"
  ln -sf libkrun.so "${libdir}/libkrun.so.1"
  local fwbase
  fwbase="$(basename "${fw}")"
  cp -L "${fw}" "${libdir}/${fwbase}"
  rm -f "${libdir}/libkrunfw.so.5" "${libdir}/libkrunfw.so"
  ln -sf "${fwbase}" "${libdir}/libkrunfw.so.5"
  ln -sf libkrunfw.so.5 "${libdir}/libkrunfw.so"
  echo "nexus-install: staged libkrun from host (${krun}) → ${libdir}"
  return 0
}

# Default matches internal/infra/runtime/macvm/defaults.go (RootFSCachePath).
nexus_macvm_guest_rootfs_path() {
  local cache_base
  if [ -n "${XDG_CACHE_HOME:-}" ]; then
    cache_base="${XDG_CACHE_HOME}/nexus"
  else
    cache_base="${HOME}/.cache/nexus"
  fi
  echo "${cache_base}/vm/rootfs.ext4"
}

# Install at most one release rootfs artifact: linux/amd64 → host VM disk; darwin/arm64 → macOS guest cache.
install_prebaked_rootfs_for_platform() {
  local tmp="$1" sums="$2" base="$3"
  local asset="" dest="" vm_dir="" share label_miss=""

  if [ "${GOOS:-}" = "linux" ] && [ "${GOARCH}" = "amd64" ]; then
    asset="rootfs-linux-amd64.ext4.gz"
    share="$(nexus_data_share_dir)"
    vm_dir="${share}/vm"
    dest="${vm_dir}/rootfs.ext4"
    label_miss="daemon may bake rootfs on first start"
  elif [ "${GOOS:-}" = "darwin" ] && [ "${GOARCH}" = "arm64" ]; then
    asset="rootfs-darwin-arm64.ext4.gz"
    dest="$(nexus_macvm_guest_rootfs_path)"
    vm_dir="$(dirname "${dest}")"
    label_miss="macOS VM may build/download guest disk on first use"
  else
    echo "nexus-install: no prebaked rootfs for ${GOOS:-unknown}/${GOARCH:-unknown} (skipped)"
    return 0
  fi

  if ! grep -qF " ${asset}" "${sums}" 2>/dev/null; then
    echo "nexus-install: no ${asset} in release checksums; ${label_miss}"
    return 0
  fi

  need_cmd gzip
  mkdir -p "${vm_dir}"
  if [ -f "${dest}" ]; then
    echo "nexus-install: ${dest} already exists; skipping ${asset}"
    return 0
  fi

  curl -fsSL -L "${base}/${asset}" -o "${tmp}/${asset}"
  (
    cd "${tmp}"
    verify_checksum_file "${sums}" "${asset}"
  )
  echo "nexus-install: extracting ${asset} → ${dest} (large file; may take a minute)"
  gzip -dc "${tmp}/${asset}" > "${dest}.tmp"
  mv "${dest}.tmp" "${dest}"
  echo "nexus-install: installed prebaked rootfs at ${dest}"
}

install_nexus_libkrun_vm() {
  local tmp="$1" sums="$2" base="$3" bindir="$4"
  local asset="nexus-libkrun-vm-linux-${GOARCH}"
  mkdir -p "${bindir}"
  if grep -qF " ${asset}" "${sums}" 2>/dev/null; then
    curl -fsSL -L "${base}/${asset}" -o "${tmp}/${asset}"
    (
      cd "${tmp}"
      verify_checksum_file "${sums}" "${asset}"
    )
    install -m 0755 "${tmp}/${asset}" "${bindir}/nexus-libkrun-vm"
    echo "nexus-install: installed ${bindir}/nexus-libkrun-vm (${asset})"
    return
  fi
  if [ "${GOARCH}" != "amd64" ]; then
    die "this Nexus release has no checksum entry for ${asset}; use Linux amd64 or build nexus-libkrun-vm from source"
  fi
  local raw="https://raw.githubusercontent.com/${GITHUB_REPOSITORY}/${NEXUS_VERSION}/packages/nexus/cmd/nexus/nexus-libkrun-vm"
  echo "nexus-install: release checksums omit ${asset}; fetching VM helper from ${raw}"
  curl -fsSL -L "${raw}" -o "${tmp}/nexus-libkrun-vm"
  [ -s "${tmp}/nexus-libkrun-vm" ] || die "failed to download nexus-libkrun-vm (empty file)"
  install -m 0755 "${tmp}/nexus-libkrun-vm" "${bindir}/nexus-libkrun-vm"
}

install_passt_user_local() {
  local dest="$1"
  mkdir -p "$(dirname "$dest")"
  if [ -x "${dest}" ]; then
    return 0
  fi
  if command -v passt >/dev/null 2>&1; then
    local sys
    sys="$(command -v passt)"
    install -m 0755 "${sys}" "${dest}"
    echo "nexus-install: installed passt at ${dest} (copied from ${sys})"
    return 0
  fi
  if [ "${GOARCH}" = "amd64" ]; then
    echo "nexus-install: downloading static passt for amd64 ..."
    curl -fsSL --retry 3 -o "${dest}" "https://passt.top/builds/latest/x86_64/passt"
    chmod 0755 "${dest}"
    echo "nexus-install: installed ${dest} (passt.top static build)"
    return 0
  fi
  return 1
}

install_linux_vm_runtime() {
  local tmp="$1" sums="$2" base="$3"
  [ "${GOOS:-}" = linux ] || return 0

  local share libdir bindir
  share="$(nexus_data_share_dir)"
  libdir="${share}/lib"
  bindir="${share}/bin"
  mkdir -p "${libdir}" "${bindir}"

  if [ ! -e "${libdir}/libkrun.so.1" ]; then
    case "${GOARCH}" in
      amd64)
        install_smolvm_libs_amd64 "${libdir}"
        ;;
      arm64)
        if ! install_libkrun_libs_from_host_arm64 "${libdir}"; then
          die "could not find libkrun.so.1 for Linux arm64 (try Nix/libkrun packages, or use --driver sandbox)"
        fi
        ;;
      *)
        die "automatic libkrun provisioning is not implemented for Linux ${GOARCH}"
        ;;
    esac
  else
    echo "nexus-install: libkrun already present at ${libdir}/libkrun.so.1"
  fi

  if [ ! -x "${bindir}/nexus-libkrun-vm" ]; then
    install_nexus_libkrun_vm "${tmp}" "${sums}" "${base}" "${bindir}"
  else
    echo "nexus-install: VM helper already at ${bindir}/nexus-libkrun-vm"
  fi

  local passt_dest="${HOME}/.local/bin/passt"
  if ! install_passt_user_local "${passt_dest}"; then
    die "passt is required at ${passt_dest} for libkrun networking — install passt (e.g. apt install passt on Debian/Ubuntu arm64)"
  fi
}

detect_platform() {
  local uname_os uncpu
  uname_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  uncpu="$(uname -m)"
  case "${uname_os}" in
    linux)
      GOOS="linux" ;;
    darwin)
      GOOS="darwin" ;;
    *)
      die "unsupported OS: ${uname_os} (expected Linux or Darwin)" ;;
  esac
  case "${uncpu}" in
    x86_64|amd64)
      GOARCH="amd64" ;;
    aarch64|arm64)
      GOARCH="arm64" ;;
    *)
      die "unsupported CPU: ${uncpu} (expected x86_64, aarch64, or arm64)" ;;
  esac
}

# On Linux: unconditionally provision /data/nexus and /data/nexus/default (required VM store paths).
ensure_linux_daemon_data_dir() {
  [ "${GOOS:-}" = "linux" ] || return 0

  local uid gid
  uid="$(id -u)"
  gid="$(id -g)"

  echo "nexus-install: ensuring /data/nexus and /data/nexus/default (required for VM driver; mount XFS with reflink on /data in production)"

  if ! mkdir -p /data/nexus /data/nexus/default 2>/dev/null; then
    sudo mkdir -p /data/nexus /data/nexus/default
  fi

  if [ -O /data/nexus ] && [ -w /data/nexus ] && [ -w /data/nexus/default ] 2>/dev/null; then
    return 0
  fi
  sudo chown "${uid}:${gid}" /data/nexus /data/nexus/default
}

unix_socket_accepts_connections() {
  local sock_path="$1"
  [ -S "${sock_path}" ] || return 1
  command -v python3 >/dev/null 2>&1 || return 1
  python3 -c "
import socket, sys
path = sys.argv[1]
try:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(0.4)
    s.connect(path)
    s.close()
except OSError:
    sys.exit(1)
sys.exit(0)
" "${sock_path}" 2>/dev/null
}

summarize_host_setup() {
  local nexus_on_path="" state_base sock sock_dev

  if command -v nexus >/dev/null 2>&1; then
    nexus_on_path="$(command -v nexus)"
  fi
  if [ -x "${INSTALL_DIR}/nexus" ]; then
    echo "nexus-install: ${INSTALL_DIR}/nexus already exists (this run will replace it)."
  elif [ -n "${nexus_on_path}" ] && [ "${nexus_on_path}" != "${INSTALL_DIR}/nexus" ]; then
    echo "nexus-install: note: a different nexus is already on PATH at ${nexus_on_path}"
  fi

  state_base="${XDG_STATE_HOME:-$HOME/.local/state}"
  sock="${state_base}/nexus/nexusd.sock"
  sock_dev="${HOME}/.local/state-dev/nexus/nexusd.sock"
  if unix_socket_accepts_connections "${sock}" || unix_socket_accepts_connections "${sock_dev}"; then
    echo "nexus-install: warning: a Nexus daemon socket is accepting connections; stop it first to avoid conflicts (nexus daemon stop / nexus-dev daemon stop)."
  fi
}

prepare_install() {
  USE_SUDO=""
  if ! mkdir -p "${INSTALL_DIR}" 2>/dev/null; then
    echo "nexus-install: mkdir ${INSTALL_DIR} requires elevation; using sudo ..."
    sudo mkdir -p "${INSTALL_DIR}"
  fi
  local probe
  probe="$(mktemp "${INSTALL_DIR}/.nexus-write-probe.XXXXXX" 2>/dev/null || true)"
  if [ -n "${probe}" ] && [ -f "${probe}" ]; then
    rm -f "${probe}"
    return
  fi
  USE_SUDO="sudo"
  echo "nexus-install: ${INSTALL_DIR} is not writable as this user; using sudo for file installs."
}

run_install() {
  local src="$1" dest="$2"
  if [ -z "${USE_SUDO}" ]; then
    install -m 0755 "${src}" "${dest}"
  else
    sudo install -m 0755 "${src}" "${dest}"
  fi
}

resolve_release_tag() {
  if [ -n "${NEXUS_VERSION}" ]; then
    echo "${NEXUS_VERSION}"
    return
  fi
  need_cmd curl
  local json api="https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/latest"
  json="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$api")"
  if command -v python3 >/dev/null 2>&1; then
    NEXUS_VERSION="$(printf '%s' "$json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"])')"
  elif command -v jq >/dev/null 2>&1; then
    NEXUS_VERSION="$(printf '%s' "$json" | jq -r .tag_name)"
  else
    die "NEXUS_VERSION is unset and neither python3 nor jq is available to read ${api}"
  fi
  [ -n "${NEXUS_VERSION}" ] && [ "${NEXUS_VERSION}" != "null" ] || die "could not resolve latest release tag"
  echo "${NEXUS_VERSION}"
}

main() {
  need_cmd curl
  detect_platform
  ensure_linux_daemon_data_dir
  summarize_host_setup

  NEXUS_VERSION="$(resolve_release_tag)"
  local base="https://github.com/${GITHUB_REPOSITORY}/releases/download/${NEXUS_VERSION}"
  local tmp nexus_asset
  nexus_asset="nexus-${GOOS}-${GOARCH}"

  tmp="$(mktemp -d "${TMPDIR:-/tmp}/nexus-install.XXXXXX")"
  trap '[[ -n "${tmp:-}" ]] && rm -rf "${tmp}"' EXIT

  echo "nexus-install: release ${NEXUS_VERSION} (${GOOS}/${GOARCH})"
  local sums="${tmp}/checksums.txt"
  if ! curl -fsSL -L "${base}/checksums.txt" -o "${sums}" 2>/dev/null; then
    curl -fsSL -L "${base}/sha256sums.txt" -o "${sums}"
  fi
  curl -fsSL -L "${base}/${nexus_asset}" -o "${tmp}/${nexus_asset}"

  (
    cd "${tmp}"
    verify_checksum_file checksums.txt "${nexus_asset}"
  )

  prepare_install
  install_linux_vm_runtime "${tmp}" "${sums}" "${base}"
  install_prebaked_rootfs_for_platform "${tmp}" "${sums}" "${base}"

  run_install "${tmp}/${nexus_asset}" "${INSTALL_DIR}/nexus"

  echo "nexus-install: installed ${INSTALL_DIR}/nexus"
  echo "nexus-install: ensure ${INSTALL_DIR} is on your PATH"
}

main "$@"
