#!/usr/bin/env bash
# Dump VM serial (hvc0) and gvproxy logs on CI failure.
# Searches both the XDG cache and legacy data-home locations.
set +e  # Continue even if individual commands fail; this is a diagnostic script.

MACVM_CANDIDATES=(
  "${XDG_CACHE_HOME:-$HOME/.cache}/nexus/macvm-workspaces"
  "${XDG_DATA_HOME:-$HOME/.local/share}/nexus/macvm-workspaces"
)

for MACVM_DIR in "${MACVM_CANDIDATES[@]}"; do
  echo "=== macvm workspaces: $MACVM_DIR ==="
  ls -la "$MACVM_DIR" 2>/dev/null || echo "(missing or empty)"
  echo ""
done

for MACVM_DIR in "${MACVM_CANDIDATES[@]}"; do
  find "$MACVM_DIR" -name "hvc0.log" 2>/dev/null | sort | while read -r f; do
    echo "=== $f ($(wc -l < "$f" 2>/dev/null || echo 0) lines) ==="
    cat "$f" 2>/dev/null || echo "(unreadable)"
    echo ""
  done
done

for MACVM_DIR in "${MACVM_CANDIDATES[@]}"; do
  find "$MACVM_DIR" \( -name "gvproxy.log" -o -name "*.sock.log" \) 2>/dev/null | sort -u | while read -r f; do
    echo "=== $f (last 80 lines) ==="
    tail -80 "$f" 2>/dev/null || echo "(unreadable)"
    echo ""
  done
done
