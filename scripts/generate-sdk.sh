#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCHEMA_TMP="$(mktemp /tmp/nexus-schema.XXXXXX.json)"
OUT="$REPO_ROOT/packages/nexus-swift/Sources/NexusCore/Generated/NexusRPC.swift"

cleanup() { rm -f "$SCHEMA_TMP"; }
trap cleanup EXIT

echo "→ Generating schema from Go daemon types..."
(cd "$REPO_ROOT/packages/nexus" && go run ./cmd/schema/... --out "$SCHEMA_TMP")

echo "→ Generating Swift SDK from schema..."
python3 "$REPO_ROOT/scripts/swift-codegen.py" "$SCHEMA_TMP" "$OUT"

echo "✓ Generated: $OUT"
echo "  $(wc -l < "$OUT") lines"
