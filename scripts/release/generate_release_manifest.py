#!/usr/bin/env python3
import argparse
import base64
import hashlib
import json
import os
import pathlib
import re
import sys
from datetime import datetime, timezone

try:
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
except ImportError:
    Ed25519PrivateKey = None


ARTIFACT_RE = re.compile(r"^(nexus|nexus-daemon)-(linux|darwin)-(amd64|arm64)$")


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as f:
        while True:
            block = f.read(1024 * 1024)
            if not block:
                break
            digest.update(block)
    return digest.hexdigest()


def build_manifest(version: str, artifacts_dir: pathlib.Path, base_url: str) -> dict:
    minimum_updater = os.getenv("NEXUS_MINIMUM_UPDATER_VERSION", "0.0.0").strip() or "0.0.0"
    artifacts = {}
    for child in sorted(artifacts_dir.iterdir()):
        if not child.is_file():
            continue
        if child.name.endswith(".sha256"):
            continue
        match = ARTIFACT_RE.match(child.name)
        if not match:
            continue
        binary_name, os_name, arch = match.groups()
        key = f"{os_name}-{arch}"
        entry = artifacts.setdefault(key, {})
        checksum = sha256_file(child)
        entry["urlBase"] = base_url
        if binary_name == "nexus":
            entry["cli"] = {"name": child.name, "sha256": checksum}
        else:
            entry["daemon"] = {"name": child.name, "sha256": checksum}

    for target, entry in artifacts.items():
        if "cli" not in entry or "daemon" not in entry:
            raise RuntimeError(f"missing CLI or daemon artifact for {target}")

    manifest = {
        "schemaVersion": 1,
        "version": version,
        "publishedAt": datetime.now(tz=timezone.utc).isoformat(),
        "minimumUpdaterVersion": minimum_updater,
        "artifacts": artifacts,
    }
    return manifest


def write_checksums(artifacts_dir: pathlib.Path) -> None:
    for child in sorted(artifacts_dir.iterdir()):
        if not child.is_file():
            continue
        if child.name.endswith(".sha256"):
            continue
        digest = sha256_file(child)
        checksum_path = artifacts_dir / f"{child.name}.sha256"
        checksum_path.write_text(f"{digest}  {child.name}\n", encoding="utf-8")


def sign_manifest(manifest_bytes: bytes, output_path: pathlib.Path) -> None:
    key_b64 = os.getenv("NEXUS_UPDATE_MANIFEST_PRIVATE_KEY_B64", "").strip()
    if not key_b64:
        raise RuntimeError("NEXUS_UPDATE_MANIFEST_PRIVATE_KEY_B64 is required")
    if Ed25519PrivateKey is None:
        raise RuntimeError("cryptography package is required for manifest signing")
    private_key_raw = base64.b64decode(key_b64)
    private_key = Ed25519PrivateKey.from_private_bytes(private_key_raw)
    signature = private_key.sign(manifest_bytes)
    output_path.write_text(base64.b64encode(signature).decode("ascii"), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--artifacts-dir", required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--manifest-out", required=True)
    parser.add_argument("--signature-out", required=True)
    args = parser.parse_args()

    artifacts_dir = pathlib.Path(args.artifacts_dir)
    manifest_out = pathlib.Path(args.manifest_out)
    signature_out = pathlib.Path(args.signature_out)

    write_checksums(artifacts_dir)
    manifest = build_manifest(args.version, artifacts_dir, args.base_url)
    manifest_bytes = json.dumps(manifest, indent=2, sort_keys=True).encode("utf-8") + b"\n"
    manifest_out.write_bytes(manifest_bytes)
    sign_manifest(manifest_bytes, signature_out)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"generate_release_manifest failed: {exc}", file=sys.stderr)
        raise
