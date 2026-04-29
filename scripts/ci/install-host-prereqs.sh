#!/usr/bin/env bash
set -euo pipefail

for attempt in 1 2 3; do
  if sudo apt-get -o Acquire::Retries=5 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 update -qq \
    && sudo apt-get -o Acquire::Retries=5 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 install -y squashfs-tools e2fsprogs rsync xfsprogs build-essential; then
    # Try to install passt from repos; ignore failure (build script will fall back to static download)
    sudo apt-get -o Acquire::Retries=3 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 install -y passt || true
    exit 0
  fi
  if [ "$attempt" -eq 3 ]; then
    echo "host prerequisite install failed after 3 attempts"
    exit 1
  fi
  echo "host prerequisite install failed (attempt $attempt/3), retrying..."
  sleep $((attempt * 5))
done
