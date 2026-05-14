#!/usr/bin/env bash
# Install e2fsprogs via Homebrew and add mke2fs to PATH.
# HOMEBREW_NO_AUTO_UPDATE skips the 1-2 min tap refresh on every run.
set -euo pipefail

export HOMEBREW_NO_AUTO_UPDATE="${HOMEBREW_NO_AUTO_UPDATE:-1}"
export HOMEBREW_NO_INSTALL_CLEANUP="${HOMEBREW_NO_INSTALL_CLEANUP:-1}"

brew install --quiet e2fsprogs

# e2fsprogs is keg-only; add its sbin to PATH for mke2fs.
E2FS_SBIN="$(brew --prefix e2fsprogs)/sbin"
if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$E2FS_SBIN" >> "$GITHUB_PATH"
else
  echo "Add to PATH manually: $E2FS_SBIN"
fi
