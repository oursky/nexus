#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET_DIR="$ROOT/packages/nexus/internal/infra/cli/mutagenbin"

mkdir -p "$TARGET_DIR"

# Skip download if all four mutagen binaries are already present (idempotent in CI).
if [[ -f "$TARGET_DIR/mutagen-darwin-arm64" && -f "$TARGET_DIR/mutagen-darwin-amd64" \
   && -f "$TARGET_DIR/mutagen-linux-amd64" && -f "$TARGET_DIR/mutagen-linux-arm64" ]]; then
  echo "Mutagen binaries already present; skipping download."
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
api_url = "https://api.github.com/repos/mutagen-io/mutagen/releases/latest"

with urllib.request.urlopen(api_url) as resp:
    release = json.load(resp)

tag = release["tag_name"]
assets = {asset["name"]: asset["browser_download_url"] for asset in release.get("assets", [])}

targets = [
    ("mutagen_darwin_arm64_", "mutagen-darwin-arm64"),
    ("mutagen_darwin_amd64_", "mutagen-darwin-amd64"),
    ("mutagen_linux_amd64_", "mutagen-linux-amd64"),
    ("mutagen_linux_arm64_", "mutagen-linux-arm64"),
]

def find_asset_url(prefix: str) -> str:
    for name, url in assets.items():
        if name.startswith(prefix) and name.endswith(".tar.gz"):
            return url
    raise RuntimeError(f"missing release asset starting with {prefix!r}")

agents_written = False

for prefix, output_name in targets:
    url = find_asset_url(prefix)
    print(f"Downloading {url}")
    with urllib.request.urlopen(url) as resp:
        payload = resp.read()

    with tempfile.NamedTemporaryFile(delete=False, suffix=".tar.gz") as tmp:
        tmp.write(payload)
        archive_path = tmp.name

    output_path = os.path.join(target_dir, output_name)
    try:
        with tarfile.open(archive_path, "r:gz") as tgz:
            # Extract mutagen binary
            member_name = None
            agents_member = None
            for member in tgz.getmembers():
                if member.isdir():
                    continue
                base = os.path.basename(member.name)
                if base == "mutagen":
                    member_name = member.name
                elif base == "mutagen-agents.tar.gz":
                    agents_member = member.name

            if member_name is None:
                raise RuntimeError(f"mutagen binary not found in archive for {prefix}")

            src = tgz.extractfile(member_name)
            if src is None:
                raise RuntimeError(f"unable to extract mutagen binary for {prefix}")

            data = src.read()
            with open(output_path, "wb") as out:
                out.write(data)
            os.chmod(
                output_path,
                stat.S_IRUSR | stat.S_IWUSR | stat.S_IXUSR | stat.S_IRGRP | stat.S_IXGRP | stat.S_IROTH | stat.S_IXOTH,
            )
            print(f"Wrote {output_path}")

            # Extract mutagen-agents.tar.gz once (it's identical across platforms).
            if not agents_written and agents_member is not None:
                agents_src = tgz.extractfile(agents_member)
                if agents_src is not None:
                    agents_path = os.path.join(target_dir, "mutagen-agents.tar.gz")
                    with open(agents_path, "wb") as out:
                        out.write(agents_src.read())
                    print(f"Wrote {agents_path}")
                    agents_written = True

    finally:
        try:
            os.remove(archive_path)
        except OSError:
            pass

if not agents_written:
    print("WARNING: mutagen-agents.tar.gz not found in any release archive", file=sys.stderr)

print(f"Updated embedded Mutagen binaries to {tag}")
PY
