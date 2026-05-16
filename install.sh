#!/usr/bin/env bash
# Install the Nexus CLI (`nexus`) from GitHub Releases.
# Minimal bootstrap: downloads + verifies the binary, then delegates all host
# setup (tools, rootfs, bake) to `nexus install`.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/oursky/nexus/main/install.sh | bash
#
# Environment:
#   GITHUB_REPOSITORY   default oursky/nexus
#   NEXUS_VERSION       release tag (e.g. v0.31.0); when unset, uses GitHub "latest" stable release
#   INSTALL_DIR         default ~/.local/bin
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

# ── Platform detection ──────────────────────────────────────────────────────
detect_platform() {
  local uname_os uncpu
  uname_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  uncpu="$(uname -m)"
  case "${uname_os}" in
    linux)   GOOS="linux" ;;
    darwin)  GOOS="darwin" ;;
    *)       die "unsupported OS: ${uname_os} (expected Linux or Darwin)" ;;
  esac
  case "${uncpu}" in
    x86_64|amd64)   GOARCH="amd64" ;;
    aarch64|arm64)  GOARCH="arm64" ;;
    *)              die "unsupported CPU: ${uncpu} (expected x86_64, aarch64, or arm64)" ;;
  esac
  if [[ "${GOOS}" == "linux" && "${GOARCH}" == "arm64" ]]; then
    die "Linux on ARM64 is not supported; use Linux x86_64 or macOS/Apple Silicon (darwin/arm64)"
  fi
  if [[ "${GOOS}" == "darwin" && "${GOARCH}" == "amd64" ]]; then
    die "Intel Mac (darwin/amd64) is not supported — use Apple Silicon (darwin/arm64) or Linux amd64"
  fi
}

# ── Release resolution ─────────────────────────────────────────────────────
resolve_release_tag() {
  if [ -n "${NEXUS_VERSION}" ]; then
    echo "${NEXUS_VERSION}"
    return
  fi
  need_cmd curl
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

# ── Main ────────────────────────────────────────────────────────────────────
main() {
  need_cmd curl
  detect_platform

  NEXUS_VERSION="$(resolve_release_tag)"
  local base="https://github.com/${GITHUB_REPOSITORY}/releases/download/${NEXUS_VERSION}"
  local tmp nexus_asset
  nexus_asset="nexus-${GOOS}-${GOARCH}"

  tmp="$(mktemp -d "${TMPDIR:-/tmp}/nexus-install.XXXXXX")"
  trap '[[ -n "${tmp:-}" ]] && rm -rf "${tmp}"' EXIT

  echo "nexus-install: release ${NEXUS_VERSION} (${GOOS}/${GOARCH})"

  # Download checksums + binary
  local sums="${tmp}/checksums.txt"
  if ! curl -fsSL -L "${base}/checksums.txt" -o "${sums}" 2>/dev/null; then
    curl -fsSL -L "${base}/sha256sums.txt" -o "${sums}"
  fi
  curl -fsSL -L "${base}/${nexus_asset}" -o "${tmp}/${nexus_asset}"

  # Verify checksum
  (
    cd "${tmp}"
    local line expected
    line="$(grep -F " ${nexus_asset}" checksums.txt || true)"
    [ -n "$line" ] || die "no checksum line for ${nexus_asset} in checksums.txt"
    expected="$(echo "$line" | awk '{print $1}')"
    if command -v sha256sum >/dev/null 2>&1; then
      echo "${expected}  ${nexus_asset}" | sha256sum -c - >/dev/null
    elif command -v shasum >/dev/null 2>&1; then
      local actual
      actual="$(shasum -a 256 "${nexus_asset}" | awk '{print $1}')"
      [ "$actual" = "$expected" ] || die "checksum mismatch for ${nexus_asset}"
    else
      die "need sha256sum or shasum -a 256 to verify downloads"
    fi
  )

  # Install binary
  mkdir -p "${INSTALL_DIR}"
  install -m 0755 "${tmp}/${nexus_asset}" "${INSTALL_DIR}/nexus"

  echo "nexus-install: installed ${INSTALL_DIR}/nexus"
  echo "nexus-install: ensure ${INSTALL_DIR} is on your PATH"

  # Delegate remaining setup to the nexus binary
  echo "nexus-install: running 'nexus install' for host setup..."
  exec "${INSTALL_DIR}/nexus" install "$@"
}

main "$@"
