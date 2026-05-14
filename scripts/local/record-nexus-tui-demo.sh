#!/usr/bin/env bash
# Record a clean Nexus TUI asciicast. Run from repo root:
#   ./scripts/local/record-nexus-tui-demo.sh [output.cast]
# Requires: asciinema, python3, pexpect (no `uv run` — avoids resolver UI in the cast).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

CAST="${1:-docs/assets/nexus-tui-demo.cast}"

export COLORTERM="${COLORTERM:-truecolor}"
export TERM="${TERM:-xterm-256color}"
export COLUMNS="${COLUMNS:-180}"
export LINES="${LINES:-45}"

# asciinema runs -c under a minimal PATH where `python3` may be Nix/mise without
# site-packages; use the distro interpreter that ships pexpect (Linux).
PY="${NEXUS_RECORD_PYTHON:-/usr/bin/python3}"
if ! "$PY" -c "import pexpect" 2>/dev/null; then
  echo "missing pexpect for $PY (apt: python3-pexpect)" >&2
  exit 1
fi

exec asciinema rec --quiet --overwrite \
  --cols "$COLUMNS" --rows "$LINES" \
  -e SHELL,TERM,COLORTERM,COLUMNS,LINES \
  -t "Nexus TUI demo" \
  "$CAST" \
  -c "$PY scripts/local/record-nexus-tui-demo.py"
