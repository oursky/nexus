#!/usr/bin/env bash
# Back-compat wrapper: full Go e2e suite (replaces legacy Python RPC script).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
exec "$ROOT/scripts/ci/nexus-e2e.sh"
