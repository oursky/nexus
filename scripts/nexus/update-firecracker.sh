#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET_DIR="$ROOT/packages/nexus/cmd/nexus"

mkdir -p "$TARGET_DIR"

# Skip download if both binaries are already present (idempotent in CI).
if [[ -f "$TARGET_DIR/firecracker-linux-amd64" && -f "$TARGET_DIR/firecracker-linux-arm64" ]]; then
  echo "Firecracker binaries already present; skipping download."
  exit 0
fi

python3 - "$TARGET_DIR" <<'PY'
import json
import os
import stat
import sys
import tarfile
import tempfile
import urllib.request

target_dir = sys.argv[1]
api_url = "https://api.github.com/repos/firecracker-microvm/firecracker/releases/latest"

with urllib.request.urlopen(api_url) as resp:
    release = json.load(resp)

tag = release["tag_name"]
assets = {asset["name"]: asset["browser_download_url"] for asset in release.get("assets", [])}

targets = [
    ("x86_64", "firecracker-linux-amd64"),
    ("aarch64", "firecracker-linux-arm64"),
]

def find_asset_url(arch: str) -> str:
    suffix = f"-{arch}.tgz"
    for name, url in assets.items():
        if name.endswith(suffix):
            return url
    raise RuntimeError(f"missing release asset for architecture {arch}")

for arch, output_name in targets:
    url = find_asset_url(arch)
    print(f"Downloading {url}")
    with urllib.request.urlopen(url) as resp:
        payload = resp.read()

    with tempfile.NamedTemporaryFile(delete=False, suffix=".tgz") as tmp:
        tmp.write(payload)
        archive_path = tmp.name

    output_path = os.path.join(target_dir, output_name)
    try:
        with tarfile.open(archive_path, "r:gz") as tgz:
            member_name = None
            needle = f"firecracker-{tag}-{arch}"
            for member in tgz.getmembers():
                base = os.path.basename(member.name)
                if base == needle:
                    member_name = member.name
                    break
            if member_name is None:
                raise RuntimeError(f"firecracker binary not found in archive for {arch}")

            src = tgz.extractfile(member_name)
            if src is None:
                raise RuntimeError(f"unable to extract firecracker binary for {arch}")

            data = src.read()
            with open(output_path, "wb") as out:
                out.write(data)

        os.chmod(output_path, stat.S_IRUSR | stat.S_IWUSR | stat.S_IXUSR | stat.S_IRGRP | stat.S_IXGRP | stat.S_IROTH | stat.S_IXOTH)
        print(f"Wrote {output_path}")
    finally:
        try:
            os.remove(archive_path)
        except OSError:
            pass

print(f"Updated embedded Firecracker binaries to {tag}")
PY
