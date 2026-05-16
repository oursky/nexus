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
#   SMOLVM_VERSION      when NEXUS_INSTALL_PREFETCH_VM_RUNTIME=1 only: smolvm release tag (default v0.5.19; align with CI build-nexus-libkrun.sh)
#   NEXUS_INSTALL_PREFETCH_VM_RUNTIME  set to 1 to download smolvm libkrun tarball and passt before first daemon (default: skip; linux/amd64 release binary embeds them)
#   NEXUS_XFS_SIZE_GB   sparse backing size when creating an XFS loop volume (default 20)
#   NEXUS_INSTALL_VERBOSE  set to 1 to print full sudo command lines
#   NEXUS_VM_ROOTFS_OPERATIONAL_GIB     after decompressing a shrunk release rootfs, grow to this GiB (default 8)
#   NEXUS_VM_ROOTFS_OPERATIONAL_BYTES   overrides GiB when set (exact byte target for truncate + resize2fs)
#   NEXUS_SKIP_HOMEBREW_INSTALL           on macOS: if 1, do not auto-install Homebrew when guest-disk tools are missing
#   NEXUS_SKIP_LINUX_ROOTFS_PKGS          on Linux: if 1, do not sudo-install e2fsprogs/coreutils when guest-disk tools are missing
#
# Production daemon VM state defaults to /data/nexus/default (install.sh prepares /data/nexus).
# Other daemon instances should use their own directory, e.g. nexus daemon start --workdir-root /data/nexus/e2e.
#
# Requires: curl, sha256sum or shasum -a 256 for release checksums, gzip when installing a prebaked rootfs,
# (linux/amd64 host or darwin/arm64).
# Shrunk libkrun guest disks need e2fsck + resize2fs + truncate when growing after decompress.
#   Linux: sudo-install e2fsprogs + coreutils via apt/dnf/yum/zypper/pacman/apk/etc. when missing (opt-out: NEXUS_SKIP_LINUX_ROOTFS_PKGS=1).
#   macOS: this script can install Homebrew non-interactively and brew-install e2fsprogs + coreutils when needed.
# When NEXUS_VERSION is unset, the latest tag is inferred from GitHub’s /releases/latest redirect (curl only).
# Optional: nc with OpenBSD-style -U (unix) for a best-effort daemon-socket warning.
# Optional: pv for ETA/progress bars during gzip/tar steps (or NEXUS_INSTALL_PREFETCH_VM_RUNTIME smolvm tarball).
# Linux libkrun: when auto-creating an XFS loop volume, mkfs.xfs (package xfsprogs) is required.
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

sudo_run() {
  if [[ "${NEXUS_INSTALL_VERBOSE:-}" == "1" ]]; then
    local parts="" a q
    for a in "$@"; do
      printf -v q '%q' "$a"
      parts+=" ${q}"
    done
    echo "nexus-install: \$ sudo${parts}" >&2
  fi
  sudo "$@"
}

# Fetch URL → file. Uses curl's progress bar on stderr when it is a TTY; otherwise silent.
# Optional extra curl flags after DEST (e.g. --retry 3).
curl_download_progress() {
  local url="$1" dest="$2"
  shift 2
  if [ -t 2 ]; then
    curl -fSL --progress-bar -L "$@" "$url" -o "$dest"
  else
    curl -fsSL -L "$@" "$url" -o "$dest"
  fi
}

# Stream FILE to stdout: cat, or pv with byte-accurate progress when pv is installed (-s = compressed/read size).
pv_through_file() {
  local file="$1"
  local label="${2:-}"
  local sz=""
  [ -f "${file}" ] || die "pv_through_file: not a regular file: ${file}"
  sz="$(wc -c < "${file}" | awk '{print $1}')"
  if command -v pv >/dev/null 2>&1 && [ -n "${sz}" ] && [ "${sz}" -gt 0 ]; then
    if [ -n "${label}" ]; then
      pv -s "${sz}" -N "${label}" "${file}"
    else
      pv -s "${sz}" "${file}"
    fi
  elif command -v pv >/dev/null 2>&1; then
    pv "${file}"
  else
    cat "${file}"
  fi
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

# ── Linux libkrun extras (optional) ────────────────────────────────────────────
# Release nexus-linux-amd64 embeds libkrun libs and passt; `nexus daemon start` extracts them.
# Set NEXUS_INSTALL_PREFETCH_VM_RUNTIME=1 to pre-download smolvm + passt like older installers.

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
  echo "nexus-install: smolvm — downloading checksums (${SMOLVM_VERSION}) …"
  curl_download_progress "${base_smol}/checksums.sha256" "${sums_smol}"
  echo "nexus-install: smolvm — downloading ${tarball} …"
  curl_download_progress "${base_smol}/${tarball}" "${tball}"
  echo "nexus-install: smolvm — verifying tarball checksum …"
  (
    cd "${td}"
    verify_checksum_file "checksums.sha256" "${tarball}"
  )
  ex="${td}/extract"
  mkdir -p "${ex}"
  echo "nexus-install: smolvm — extracting libkrun libraries (${tarball})"
  pv_through_file "${tball}" "${tarball}" | tar -xzf - -C "${ex}" --strip-components=2 "smolvm-${ver_plain}-linux-x86_64/lib"
  mkdir -p "${libdir}"
  cp -a "${ex}/." "${libdir}/"
  cleanup
  echo "nexus-install: smolvm — installed libkrun + libkrunfw (${SMOLVM_VERSION}) → ${libdir}"
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

guest_rootfs_grow_tools_present() {
  command -v e2fsck >/dev/null 2>&1 || return 1
  command -v resize2fs >/dev/null 2>&1 || return 1
  command -v truncate >/dev/null 2>&1 || command -v gtruncate >/dev/null 2>&1 || return 1
  return 0
}

# Prepend Homebrew keg paths so e2fsck/resize2fs and GNU truncate are on PATH (macOS).
prepend_brew_guest_rootfs_paths() {
  command -v brew >/dev/null 2>&1 || return 0
  local ep cu
  ep="$(brew --prefix e2fsprogs 2>/dev/null)/sbin"
  cu="$(brew --prefix coreutils 2>/dev/null)/libexec/gnubin"
  [ -d "$ep" ] && PATH="${ep}:${PATH}"
  [ -d "$cu" ] && PATH="${cu}:${PATH}"
  export PATH
}

ensure_homebrew_installed_macos() {
  [ "$(uname -s)" = "Darwin" ] || return 0

  if command -v brew >/dev/null 2>&1; then
    eval "$(brew shellenv)"
    return 0
  fi
  if [ -x /opt/homebrew/bin/brew ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
    return 0
  fi
  if [ -x /usr/local/bin/brew ]; then
    eval "$(/usr/local/bin/brew shellenv)"
    return 0
  fi

  if [ "${NEXUS_SKIP_HOMEBREW_INSTALL:-}" = "1" ]; then
    die "macOS needs Homebrew for e2fsck/resize2fs/truncate — install https://brew.sh or unset NEXUS_SKIP_HOMEBREW_INSTALL"
  fi

  echo "nexus-install: macOS — installing Homebrew (NONINTERACTIVE=1; guest VM disk tools) …" >&2
  need_cmd curl bash

  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" ||
    die "Homebrew installation failed — see https://docs.brew.sh/Installation"

  if [ -x /opt/homebrew/bin/brew ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
  elif [ -x /usr/local/bin/brew ]; then
    eval "$(/usr/local/bin/brew shellenv)"
  else
    die "Homebrew installed but brew not found — add Homebrew to PATH per the installer output"
  fi
}

ensure_macos_guest_rootfs_tools() {
  [ "$(uname -s)" = "Darwin" ] || return 0

  prepend_brew_guest_rootfs_paths
  if guest_rootfs_grow_tools_present; then
    return 0
  fi

  ensure_homebrew_installed_macos

  echo "nexus-install: macOS — brew install e2fsprogs coreutils (guest VM disk grow) …" >&2
  brew install e2fsprogs coreutils

  prepend_brew_guest_rootfs_paths

  if ! guest_rootfs_grow_tools_present; then
    die "guest rootfs tools still missing after brew install (e2fsck resize2fs truncate)"
  fi
}

# Install e2fsprogs + coreutils on Linux when shrinking guest rootfs needs grow (truncate/resize2fs).
ensure_linux_guest_rootfs_tools() {
  [ "$(uname -s)" = "Linux" ] || return 0

  if guest_rootfs_grow_tools_present; then
    return 0
  fi

  if [ "${NEXUS_SKIP_LINUX_ROOTFS_PKGS:-}" = "1" ]; then
    die "Linux needs e2fsprogs + coreutils for guest rootfs grow — install them or unset NEXUS_SKIP_LINUX_ROOTFS_PKGS"
  fi

  echo "nexus-install: Linux — installing e2fsprogs + coreutils (guest VM disk grow; may prompt for sudo) …" >&2

  if command -v apt-get >/dev/null 2>&1; then
    sudo_run env DEBIAN_FRONTEND=noninteractive apt-get update -qq
    sudo_run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq e2fsprogs coreutils
  elif command -v apt >/dev/null 2>&1; then
    sudo_run env DEBIAN_FRONTEND=noninteractive apt update -qq
    sudo_run env DEBIAN_FRONTEND=noninteractive apt install -y -qq e2fsprogs coreutils
  elif command -v dnf >/dev/null 2>&1; then
    sudo_run dnf install -y e2fsprogs coreutils
  elif command -v yum >/dev/null 2>&1; then
    sudo_run yum install -y e2fsprogs coreutils
  elif command -v microdnf >/dev/null 2>&1; then
    sudo_run microdnf install -y e2fsprogs coreutils
  elif command -v zypper >/dev/null 2>&1; then
    sudo_run zypper --non-interactive install -y e2fsprogs coreutils
  elif command -v pacman >/dev/null 2>&1; then
    sudo_run pacman -Sy --needed --noconfirm e2fsprogs coreutils
  elif command -v apk >/dev/null 2>&1; then
    sudo_run apk add --no-cache e2fsprogs coreutils
  elif command -v xbps-install >/dev/null 2>&1; then
    sudo_run xbps-install -Sy e2fsprogs coreutils
  else
    die "cannot auto-install e2fsprogs/coreutils — install them with your distro package manager"
  fi

  hash -r 2>/dev/null || true

  if ! guest_rootfs_grow_tools_present; then
    die "guest rootfs tools still missing after distro package install (e2fsck resize2fs truncate)"
  fi
}

pick_rootfs_release_asset() {
  local sums="$1" zst_asset="$2" gz_asset="$3"
  local want_zst="" want_gz=""
  grep -qF " ${zst_asset}" "$sums" 2>/dev/null && want_zst=1
  grep -qF " ${gz_asset}" "$sums" 2>/dev/null && want_gz=1

  if [ -n "${want_zst}" ] && command -v zstd >/dev/null 2>&1; then
    echo "${zst_asset}"
    return 0
  fi
  if [ -n "${want_gz}" ]; then
    echo "${gz_asset}"
    return 0
  fi
  if [ -n "${want_zst}" ] && [ -z "${want_gz}" ]; then
    die "release ships ${zst_asset} only — install zstd, or use a release that includes ${gz_asset}"
  fi
  echo ""
}

ensure_zstd_cli_linux() {
  command -v zstd >/dev/null 2>&1 && return 0
  echo "nexus-install: Linux — installing zstd (guest VM disk download) …" >&2
  if command -v apt-get >/dev/null 2>&1; then
    sudo_run env DEBIAN_FRONTEND=noninteractive apt-get update -qq
    sudo_run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq zstd
  elif command -v apt >/dev/null 2>&1; then
    sudo_run env DEBIAN_FRONTEND=noninteractive apt update -qq
    sudo_run env DEBIAN_FRONTEND=noninteractive apt install -y -qq zstd
  elif command -v dnf >/dev/null 2>&1; then
    sudo_run dnf install -y zstd
  elif command -v yum >/dev/null 2>&1; then
    sudo_run yum install -y zstd
  elif command -v microdnf >/dev/null 2>&1; then
    sudo_run microdnf install -y zstd
  elif command -v zypper >/dev/null 2>&1; then
    sudo_run zypper --non-interactive install -y zstd
  elif command -v pacman >/dev/null 2>&1; then
    sudo_run pacman -Sy --needed --noconfirm zstd
  elif command -v apk >/dev/null 2>&1; then
    sudo_run apk add --no-cache zstd
  elif command -v xbps-install >/dev/null 2>&1; then
    sudo_run xbps-install -Sy zstd
  else
    die "cannot install zstd — use gzip rootfs artifact or install zstd manually"
  fi
  hash -r 2>/dev/null || true
}

ensure_zstd_cli_macos() {
  command -v zstd >/dev/null 2>&1 && return 0
  ensure_homebrew_installed_macos
  echo "nexus-install: macOS — brew install zstd (guest VM disk download) …" >&2
  brew install zstd
}

ensure_zstd_cli_for_vm_disk() {
  command -v zstd >/dev/null 2>&1 && return 0
  case "$(uname -s)" in
    Darwin) ensure_zstd_cli_macos ;;
    Linux) ensure_zstd_cli_linux ;;
    *) die "install zstd for .zst guest VM disk or pick gzip checksum asset" ;;
  esac
}

# Grow shrunk release rootfs (minimal ext4 inside zstd/gzip) to operational headroom; mirrors guestrootfs.EnsureOperationalHeadroom.
grow_guest_rootfs_operational_headroom() {
  local dest="$1"
  local target cur
  if [ -n "${NEXUS_VM_ROOTFS_OPERATIONAL_BYTES:-}" ]; then
    target="${NEXUS_VM_ROOTFS_OPERATIONAL_BYTES}"
  else
    local gib="${NEXUS_VM_ROOTFS_OPERATIONAL_GIB:-8}"
    target=$((gib * 1024 * 1024 * 1024))
  fi
  cur=$(wc -c <"$dest" | tr -d ' ')
  if [ "$cur" -ge "$target" ]; then
    return 0
  fi
  echo "nexus-install: VM rootfs — expanding shrunk disk (${cur} → ${target} bytes)"

  case "$(uname -s)" in
    Darwin)
      ensure_macos_guest_rootfs_tools
      prepend_brew_guest_rootfs_paths
      ;;
    Linux)
      ensure_linux_guest_rootfs_tools
      ;;
    *)
      if ! guest_rootfs_grow_tools_present; then
        die "guest rootfs grow unsupported on OS $(uname -s) — need e2fsck resize2fs truncate"
      fi
      ;;
  esac

  local trunc_bin=""
  trunc_bin="$(command -v truncate 2>/dev/null || true)"
  [ -n "$trunc_bin" ] || trunc_bin="$(command -v gtruncate 2>/dev/null || true)"
  [ -n "$trunc_bin" ] || die "missing truncate (GNU coreutils)"
  need_cmd e2fsck resize2fs

  set +e
  e2fsck -f -y "$dest"
  local ecc=$?
  set -e
  [ "$ecc" -le 2 ] || die "e2fsck failed growing guest rootfs (exit $ecc)"
  "$trunc_bin" -s "$target" "$dest"
  resize2fs "$dest"
}

# Install at most one release rootfs artifact: linux/amd64 → host VM disk; darwin/arm64 → macOS guest cache.
install_prebaked_rootfs_for_platform() {
  local tmp="$1" sums="$2" base="$3"
  local zst_asset="" gz_asset="" asset="" dest="" vm_dir="" share label_miss=""

  if [ "${GOOS:-}" = "linux" ] && [ "${GOARCH}" = "amd64" ]; then
    zst_asset="rootfs-linux-amd64.ext4.zst"
    gz_asset="rootfs-linux-amd64.ext4.gz"
    share="$(nexus_data_share_dir)"
    vm_dir="${share}/vm"
    dest="${vm_dir}/rootfs.ext4"
    label_miss="daemon may bake rootfs on first start"
  elif [ "${GOOS:-}" = "darwin" ] && [ "${GOARCH}" = "arm64" ]; then
    zst_asset="rootfs-darwin-arm64.ext4.zst"
    gz_asset="rootfs-darwin-arm64.ext4.gz"
    dest="$(nexus_macvm_guest_rootfs_path)"
    vm_dir="$(dirname "${dest}")"
    label_miss="will bake locally after install (~1–2 min)"
  else
    echo "nexus-install: no prebaked rootfs for ${GOOS:-unknown}/${GOARCH:-unknown} (skipped)"
    return 0
  fi

  asset="$(pick_rootfs_release_asset "${sums}" "${zst_asset}" "${gz_asset}")"
  if [ -z "${asset}" ]; then
    echo "nexus-install: no ${zst_asset} or ${gz_asset} in release checksums; ${label_miss}"
    return 0
  fi

  mkdir -p "${vm_dir}"
  if [ -f "${dest}" ]; then
    echo "nexus-install: ${dest} already exists; skipping ${asset}"
    return 0
  fi

  case "${asset}" in
    *.zst)
      ensure_zstd_cli_for_vm_disk
      ;;
    *.gz)
      need_cmd gzip
      ;;
    *)
      die "unsupported VM disk archive: ${asset}"
      ;;
  esac

  echo "nexus-install: VM rootfs — downloading ${asset} (${NEXUS_VERSION:-release}) …"
  curl_download_progress "${base}/${asset}" "${tmp}/${asset}"

  echo "nexus-install: VM rootfs — verifying checksum for ${asset}"
  (
    cd "${tmp}"
    verify_checksum_file "${sums}" "${asset}"
  )

  echo "nexus-install: VM rootfs — decompressing ${asset} → ${dest}"
  case "${asset}" in
    *.zst)
      pv_through_file "${tmp}/${asset}" "${asset}" | zstd -dc >"${dest}.tmp"
      ;;
    *.gz)
      pv_through_file "${tmp}/${asset}" "${asset}" | gzip -dc >"${dest}.tmp"
      ;;
  esac

  mv "${dest}.tmp" "${dest}"
  grow_guest_rootfs_operational_headroom "${dest}"
  echo "nexus-install: VM rootfs — installed prebaked disk at ${dest}"
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
    echo "nexus-install: passt — installed at ${dest} (copied from ${sys})"
    return 0
  fi
  if [ "${GOARCH}" = "amd64" ]; then
    echo "nexus-install: passt — downloading static binary for amd64 (passt.top) …"
    curl_download_progress "https://passt.top/builds/latest/x86_64/passt" "${dest}" --retry 3
    chmod 0755 "${dest}"
    echo "nexus-install: passt — installed static build at ${dest}"
    return 0
  fi
  return 1
}

install_linux_vm_runtime() {
  local tmp="$1" sums="$2" base="$3"
  [ "${GOOS:-}" = linux ] || return 0
  [ "${GOARCH}" = "amd64" ] || return 0

  local share libdir
  share="$(nexus_data_share_dir)"
  libdir="${share}/lib"
  mkdir -p "${libdir}"

  if [[ "${NEXUS_INSTALL_PREFETCH_VM_RUNTIME:-}" != "1" ]]; then
    return 0
  fi

  if [ ! -e "${libdir}/libkrun.so.1" ]; then
    install_smolvm_libs_amd64 "${libdir}"
  else
    echo "nexus-install: libkrun already present at ${libdir}/libkrun.so.1"
  fi

  local passt_dest="${HOME}/.local/bin/passt"
  if ! install_passt_user_local "${passt_dest}"; then
    die "passt is required at ${passt_dest} — install passt from your distro or use a release nexus binary with embedded passt"
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

  if [[ "${GOOS:-}" == "linux" && "${GOARCH}" == "arm64" ]]; then
    die "Linux on ARM64 is not supported; use Linux x86_64 or macOS/Apple Silicon (darwin/arm64)"
  fi

  if [[ "${GOOS:-}" == "darwin" && "${GOARCH}" == "amd64" ]]; then
    die "Intel Mac (darwin/amd64) is not supported — use Apple Silicon (darwin/arm64) or Linux amd64"
  fi
}

linux_reflink_probe_sudo() {
  local mp="$1"
  sudo_run bash -ceu 'mp="$1"; s="$mp/.nexus-reflink-probe-src-$$"; d="$mp/.nexus-reflink-probe-dst-$$"; touch "$s" && cp --reflink=always "$s" "$d" 2>/dev/null; rc=$?; rm -f "$s" "$d"; exit "$rc"' _ "${mp}"
}

# libkrun requires reflink (XFS with reflink=1 or btrfs). If /data/nexus is not on such a fs,
# create or reuse a sparse XFS loopback image (same approach as scripts/ci/setup-xfs-reflink.sh).
# Prefers mounting the whole tree when /data/nexus is empty; otherwise mounts XFS at
# /data/nexus/default (the daemon workdir) when that directory is empty so unrelated paths
# under /data/nexus can remain on the host filesystem.
# Does not rm -rf or umount existing mounts (unlike CI).
ensure_linux_libkrun_reflink_volume() {
  [ "${GOOS:-}" = linux ] || return 0

  local mount_point="/data/nexus"
  local workdir="${mount_point}/default"
  local backing_file="/var/lib/nexus-xfs-backing.img"
  local size_gb="${NEXUS_XFS_SIZE_GB:-20}"
  local uid gid loop_target any_top any_def

  uid="$(id -u)"
  gid="$(id -g)"

  if ! mkdir -p "${mount_point}" 2>/dev/null; then
    sudo_run mkdir -p "${mount_point}"
  fi

  if linux_reflink_probe_sudo "${mount_point}"; then
    echo "nexus-install: ${mount_point} supports reflink clones (libkrun workdir)"
    if ! mkdir -p "${workdir}" 2>/dev/null; then
      sudo_run mkdir -p "${workdir}"
    fi
    sudo_run chown "${uid}:${gid}" "${mount_point}" "${workdir}"
    return 0
  fi

  if [ -d "${workdir}" ] && linux_reflink_probe_sudo "${workdir}"; then
    echo "nexus-install: ${workdir} supports reflink clones (libkrun workdir)"
    sudo_run chown "${uid}:${gid}" "${mount_point}" "${workdir}"
    return 0
  fi

  loop_target="${mount_point}"
  any_top="$(sudo_run find "${mount_point}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null || true)"
  if [ -n "${any_top}" ]; then
    loop_target="${workdir}"
    if ! mkdir -p "${loop_target}" 2>/dev/null; then
      sudo_run mkdir -p "${loop_target}"
    fi
    any_def="$(sudo_run find "${loop_target}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null || true)"
    if [ -n "${any_def}" ]; then
      die "${mount_point} does not support reflink and ${loop_target} is not empty — move data out of ${loop_target}, use XFS/btrfs with reflink there, or see scripts/ci/setup-xfs-reflink.sh on a dev checkout"
    fi
  fi

  echo "nexus-install: host fs has no reflink at ${loop_target}; creating XFS loop volume (${size_gb} GB sparse image at ${backing_file})"

  need_cmd truncate
  command -v mkfs.xfs >/dev/null 2>&1 || die "mkfs.xfs not found — install xfsprogs (e.g. apt install xfsprogs)"

  if [ -f "${backing_file}" ]; then
    echo "nexus-install: reusing existing backing file ${backing_file}"
  else
    sudo_run mkdir -p "$(dirname "${backing_file}")"
    sudo_run truncate -s "${size_gb}G" "${backing_file}"
    sudo_run mkfs.xfs -f -m reflink=1 "${backing_file}"
  fi

  if sudo_run mount -o loop "${backing_file}" "${loop_target}"; then
    echo "nexus-install: mounted XFS with reflink=1 at ${loop_target}"
    xfs_info "${loop_target}" 2>/dev/null | grep reflink || true
    if ! mkdir -p "${workdir}" 2>/dev/null; then
      sudo_run mkdir -p "${workdir}"
    fi
    sudo_run chown "${uid}:${gid}" "${mount_point}" "${workdir}"
    return 0
  fi

  die "could not loop-mount XFS at ${loop_target} — ensure loop devices are available and try scripts/ci/setup-xfs-reflink.sh from a repo checkout"
}

# On Linux: unconditionally provision /data/nexus and /data/nexus/default (required VM store paths).
ensure_linux_daemon_data_dir() {
  [ "${GOOS:-}" = "linux" ] || return 0

  local uid gid
  uid="$(id -u)"
  gid="$(id -g)"

  echo "nexus-install: ensuring /data/nexus/default (libkrun workspace store)"

  if ! mkdir -p /data/nexus /data/nexus/default 2>/dev/null; then
    sudo_run mkdir -p /data/nexus /data/nexus/default
  fi

  if [ -O /data/nexus ] && [ -w /data/nexus ] && [ -w /data/nexus/default ] 2>/dev/null; then
    return 0
  fi
  sudo_run chown "${uid}:${gid}" /data/nexus /data/nexus/default
}

# Best-effort host fixes for libkrun VMs: KVM access (/dev/kvm) and user namespaces (passt).
# Does not fail the install; prints warnings when something cannot be auto-fixed.
persist_nexus_libkrun_sysctl_dropin() {
  # Snapshot kernel state after any runtime sysctl -w above (only persist values that are already in effect).
  local lines="" v=""
  if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then
    v="$(cat /proc/sys/kernel/unprivileged_userns_clone 2>/dev/null || echo "")"
    if [ "$v" = "1" ]; then
      lines+="kernel.unprivileged_userns_clone=1"$'\n'
    fi
  fi
  if [ -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
    v="$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo "")"
    if [ "$v" = "0" ]; then
      lines+="kernel.apparmor_restrict_unprivileged_userns=0"$'\n'
    fi
  fi
  [ -n "${lines}" ] || return 0
  printf '%s' "${lines}" | sudo_run tee /etc/sysctl.d/99-nexus-libkrun.conf >/dev/null
  echo "nexus-install: persisted sysctl settings in /etc/sysctl.d/99-nexus-libkrun.conf"
}

ensure_linux_libkrun_host_prereqs() {
  [ "${GOOS:-}" = "linux" ] || return 0
  [ "${GOARCH}" = "amd64" ] || return 0

  local v did_sysctl=""

  # --- /dev/kvm — libkrun: Error creating the Kvm object: Error(13) (EACCES when opening /dev/kvm)
  if [ ! -e /dev/kvm ]; then
    echo "nexus-install: warning: /dev/kvm not found — enable CPU virtualization in firmware," >&2
    echo "nexus-install: warning:   or enable nested KVM if this machine is already a VM." >&2
  elif [ ! -r /dev/kvm ] || [ ! -w /dev/kvm ]; then
    if getent group kvm >/dev/null 2>&1; then
      if ! id -nG | tr ' ' '\n' | grep -qx kvm; then
        echo "nexus-install: granting /dev/kvm access via group 'kvm' (requires sudo) …"
        if sudo_run usermod -aG kvm "$(id -un)"; then
          echo "nexus-install: added user $(id -un) to group kvm — log out and back in (or reboot) for it to take effect."
          echo "nexus-install: (quick test in this shell: newgrp kvm)"
        fi
      else
        echo "nexus-install: warning: user is in group kvm but /dev/kvm is still not accessible — try a new login session." >&2
      fi
    else
      echo "nexus-install: warning: no 'kvm' group on this system; ensure this user can open /dev/kvm (ls -l /dev/kvm)" >&2
    fi
  fi

  # --- Unprivileged user namespaces — passt can log: unshare: Operation not permitted
  if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then
    v="$(cat /proc/sys/kernel/unprivileged_userns_clone 2>/dev/null || echo "")"
    if [ "$v" = "0" ]; then
      echo "nexus-install: enabling kernel.unprivileged_userns_clone=1 (passt needs user namespaces) …"
      if sudo_run sysctl -w kernel.unprivileged_userns_clone=1; then
        did_sysctl=1
      else
        echo "nexus-install: warning: could not set kernel.unprivileged_userns_clone (passt may fail with unshare EPERM)" >&2
      fi
    fi
  fi

  # --- Ubuntu / AppArmor: some releases default to blocking unprivileged userns
  if [ -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
    v="$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo "")"
    if [ "$v" = "1" ]; then
      echo "nexus-install: setting kernel.apparmor_restrict_unprivileged_userns=0 (passt / user namespaces) …"
      if sudo_run sysctl -w kernel.apparmor_restrict_unprivileged_userns=0; then
        did_sysctl=1
      else
        echo "nexus-install: warning: could not relax apparmor userns restriction (passt may fail with unshare EPERM)" >&2
      fi
    fi
  fi

  if [ -n "${did_sysctl}" ] || [ ! -f /etc/sysctl.d/99-nexus-libkrun.conf ]; then
    persist_nexus_libkrun_sysctl_dropin
  fi

  if [ -r /proc/sys/user/max_user_namespaces ]; then
    v="$(cat /proc/sys/user/max_user_namespaces 2>/dev/null || echo "")"
    if [ -n "$v" ] && [ "$v" -eq 0 ] 2>/dev/null; then
      echo "nexus-install: warning: user.max_user_namespaces is 0 — increase it or passt cannot create namespaces" >&2
    fi
  fi
}

unix_socket_accepts_connections() {
  local sock_path="$1"
  [ -S "${sock_path}" ] || return 1
  command -v nc >/dev/null 2>&1 || return 1
  # OpenBSD-style nc (macOS, Debian netcat-openbsd): unix stream connect check.
  nc -h 2>&1 | grep -q -- '-U ' || return 1
  nc -z -w 1 -U "${sock_path}" 2>/dev/null
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
    sudo_run mkdir -p "${INSTALL_DIR}"
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
    sudo_run install -m 0755 "${src}" "${dest}"
  fi
}

resolve_release_tag() {
  if [ -n "${NEXUS_VERSION}" ]; then
    echo "${NEXUS_VERSION}"
    return
  fi
  need_cmd curl
  # HTML redirect → https://github.com/OWNER/REPO/releases/tag/<TAG> (no JSON / jq / python).
  local effective api="https://github.com/${GITHUB_REPOSITORY}/releases/latest"
  effective="$(curl -fsSL --compressed -o /dev/null -w '%{url_effective}' -L "$api")" || \
    die "could not resolve latest release (curl ${api})"
  case "$effective" in
    */releases/tag/*)
      NEXUS_VERSION="${effective##*/releases/tag/}"
      NEXUS_VERSION="${NEXUS_VERSION%%\?*}"
      NEXUS_VERSION="${NEXUS_VERSION%%#*}"
      NEXUS_VERSION="${NEXUS_VERSION%/}"
      ;;
    *)
      die "could not parse release tag from ${effective} (set NEXUS_VERSION explicitly)"
      ;;
  esac
  [ -n "${NEXUS_VERSION}" ] || die "could not resolve latest release tag"
  echo "${NEXUS_VERSION}"
}

main() {
  need_cmd curl
  detect_platform
  ensure_linux_libkrun_reflink_volume
  ensure_linux_daemon_data_dir
  ensure_linux_libkrun_host_prereqs
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

  # macOS: if no prebaked rootfs was in the release, bake it locally now.
  # The release binary is already signed with Hypervisor.framework entitlements.
  if [ "${GOOS:-}" = "darwin" ] && [ "${GOARCH}" = "arm64" ]; then
    local macvm_rootfs
    macvm_rootfs="$(nexus_macvm_guest_rootfs_path)"
    if [ ! -f "${macvm_rootfs}" ]; then
      echo "nexus-install: macOS — no prebaked guest rootfs in release; baking locally (one-time, ~1–2 min) …"
      if "${INSTALL_DIR}/nexus" vm bake --timeout 10m; then
        echo "nexus-install: macOS — guest rootfs baked at ${macvm_rootfs}"
      else
        echo "nexus-install: warning: macOS guest rootfs bake failed — run 'nexus vm bake' manually after ensuring ${INSTALL_DIR} is on PATH" >&2
      fi
    fi
  fi
}

main "$@"
