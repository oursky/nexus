#!/usr/bin/env bash
# Record a Nexus CLI asciicast (asciinema). Run from repo root:
#   ./scripts/local/record-nexus-cli-demo.sh [output.cast]
#
# Requires: asciinema, openssl. Builds packages/nexus/cmd/nexus with the same
# Linux embed staging as task build (see scripts/ci/with-linux-nexus-cgo.sh).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

CAST="${1:-docs/assets/nexus-cli-demo.cast}"
COLS="${COLS:-120}"
ROWS="${ROWS:-35}"
export COLORTERM="${COLORTERM:-truecolor}"
export TERM="${TERM:-xterm-256color}"

TMP_BIN="$(mktemp /tmp/nexus-cli-demo-bin.XXXXXX)"
DEMO_ROOT="$(mktemp -d /tmp/nexus-cli-demo.XXXXXX)"
cleanup() {
  rm -f "$TMP_BIN"
  rm -rf "$DEMO_ROOT"
}
trap cleanup EXIT

echo "==> Building nexus CLI -> $TMP_BIN"
( cd packages/nexus && bash ../../scripts/ci/with-linux-nexus-cgo.sh go build -o "$TMP_BIN" ./cmd/nexus )

export XDG_STATE_HOME="$DEMO_ROOT/state"
export XDG_DATA_HOME="$DEMO_ROOT/data"
mkdir -p "$XDG_STATE_HOME" "$XDG_DATA_HOME"

DEMO_PORT="$(( 47000 + RANDOM % 2500 ))"
DEMO_TOKEN="$(openssl rand -hex 16)"

export NEXUS_BIN="$TMP_BIN"
export NEXUS_DAEMON_PORT="$DEMO_PORT"
export NEXUS_DAEMON_TOKEN="$DEMO_TOKEN"
export DEMO_REPO="${DEMO_REPO:-$ROOT}"

exec asciinema rec --quiet --overwrite \
  --cols "$COLS" --rows "$ROWS" \
  -e SHELL,TERM,COLORTERM,NEXUS_BIN,NEXUS_DAEMON_PORT,NEXUS_DAEMON_TOKEN,DEMO_REPO,XDG_STATE_HOME,XDG_DATA_HOME \
  -t "Nexus CLI demo" \
  "$CAST" \
  -c "bash $(pwd)/scripts/local/cli-demo-commands.sh"
