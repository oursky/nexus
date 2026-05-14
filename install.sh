#!/usr/bin/env bash
# Install the Nexus CLI (`nexus`) and co-located `pty-host` from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/oursky/nexus/main/install.sh | bash
#
# Environment:
#   GITHUB_REPOSITORY   default oursky/nexus
#   NEXUS_VERSION       release tag (e.g. v0.31.0); when unset, uses GitHub "latest" stable release
#   INSTALL_DIR         default ~/.local/bin
#
# Requires: curl, sha256sum or shasum -a 256 for release checksums, and python3 or jq to read the releases API
# when NEXUS_VERSION is unset. If a release predates pty-host artifacts, falls back to:
#   go install github.com/oursky/nexus/packages/nexus/cmd/pty-host@<tag>
set -euo pipefail

GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-oursky/nexus}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
NEXUS_VERSION="${NEXUS_VERSION:-}"

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

install_pty_host_via_go() {
  local tag="$1"
  need_cmd go
  echo "nexus-install: installing pty-host with go install (${tag}) ..."
  if [ -z "${USE_SUDO}" ]; then
    env CGO_ENABLED="0" GOBIN="${INSTALL_DIR}" go install "github.com/oursky/nexus/packages/nexus/cmd/pty-host@${tag}"
  else
    sudo env CGO_ENABLED="0" GOBIN="${INSTALL_DIR}" go install "github.com/oursky/nexus/packages/nexus/cmd/pty-host@${tag}"
  fi
}

main() {
  need_cmd curl
  detect_platform
  ensure_linux_daemon_data_dir
  summarize_host_setup

  NEXUS_VERSION="$(resolve_release_tag)"
  local base="https://github.com/${GITHUB_REPOSITORY}/releases/download/${NEXUS_VERSION}"
  local tmp nexus_asset pty_asset
  nexus_asset="nexus-${GOOS}-${GOARCH}"
  pty_asset="pty-host-${GOOS}-${GOARCH}"

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

  if grep -qF " ${pty_asset}" "${sums}"; then
    curl -fsSL -L "${base}/${pty_asset}" -o "${tmp}/${pty_asset}"
    (
      cd "${tmp}"
      verify_checksum_file checksums.txt "${pty_asset}"
    )
  else
    echo "nexus-install: release has no ${pty_asset} bundle; using Go toolchain for pty-host."
    install_pty_host_via_go "${NEXUS_VERSION}"
  fi

  run_install "${tmp}/${nexus_asset}" "${INSTALL_DIR}/nexus"

  if [ -f "${tmp}/${pty_asset}" ]; then
    run_install "${tmp}/${pty_asset}" "${INSTALL_DIR}/pty-host"
  fi

  echo "nexus-install: installed ${INSTALL_DIR}/nexus"
  if [ -x "${INSTALL_DIR}/pty-host" ]; then
    echo "nexus-install: installed ${INSTALL_DIR}/pty-host"
  fi
  echo "nexus-install: ensure ${INSTALL_DIR} is on your PATH"
}

main "$@"
