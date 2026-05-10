#!/usr/bin/env bash
set -euo pipefail

go_files="$(git ls-files '*.go')"
if [ -z "${go_files}" ]; then
  echo "No Go files found; skipping go fix check"
  exit 0
fi

go_fix_version="go$(go env GOVERSION | sed 's/^go//' | cut -d. -f1,2)"
tmp_diff="$(mktemp)"
# shellcheck disable=SC2086
go tool fix -go="${go_fix_version}" -diff ${go_files} >"${tmp_diff}"

if [ -s "${tmp_diff}" ]; then
  echo "go tool fix reported changes. Please run: go tool fix -go=${go_fix_version} <files>"
  cat "${tmp_diff}"
  exit 1
fi
