#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${RUNNER_TEMP:-}" ]]; then
  echo "RUNNER_TEMP is required"
  exit 1
fi
if [[ -z "${GITHUB_ENV:-}" ]]; then
  echo "GITHUB_ENV is required"
  exit 1
fi

mkdir -p "$RUNNER_TEMP/libkrun-baked-cache"
echo "LIBKRUN_BAKED_CACHE_DIR=$RUNNER_TEMP/libkrun-baked-cache" >> "$GITHUB_ENV"
