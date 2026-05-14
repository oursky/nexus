#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

git clone --depth 1 https://passt.top/passt "$TMPDIR/passt"
cd "$TMPDIR/passt"

python3 "$SCRIPT_DIR/patch-passt-isolation.py"

make clean
make CFLAGS="-static -DGLIBC_NO_STATIC_NSS" passt
chmod +x passt
cp passt /tmp/passt-patched
/tmp/passt-patched --help >/dev/null

echo "Built patched passt → /tmp/passt-patched"

if [ -n "${GITHUB_ENV:-}" ]; then
    echo "NEXUS_PASST_PATH=/tmp/passt-patched" >> "$GITHUB_ENV"
fi
