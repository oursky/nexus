#!/usr/bin/env bash
set -euo pipefail

: "${GHCR_REF:?GHCR_REF is required}"

ASSET_TGZ="prebuilt-libkrun-vm-linux-amd64.tar.gz"
ASSET_SHA="${ASSET_TGZ}.sha256"
if [[ ! -f "$ASSET_TGZ" || ! -f "$ASSET_SHA" ]]; then
  echo "missing packaged assets ($ASSET_TGZ / $ASSET_SHA)"
  exit 1
fi

oras push "$GHCR_REF" \
  --artifact-type "application/vnd.oursky.nexus.prebuilt-vm.v1" \
  "${ASSET_TGZ}:application/gzip" \
  "${ASSET_SHA}:text/plain"
