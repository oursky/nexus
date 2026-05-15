#!/usr/bin/env bash
# Dump libkrun VM, passt, and host-fixture diagnostics on CI failure.
set +e  # Continue even if individual commands fail; this is a diagnostic script.

echo "=== Disk space ==="
df -h
du -sh /tmp/nexus-* 2>/dev/null || true
echo ""

echo "=== ldd nexus-libkrun-vm ==="
ldd "$HOME/.local/share/nexus/bin/nexus-libkrun-vm" 2>&1 || true

echo "=== libs in $HOME/.local/share/nexus/lib/ ==="
ls -la "$HOME/.local/share/nexus/lib/" 2>&1 || true

echo "=== libkrun.log files ==="
for root in /data/nexus/e2e /data/nexus/libkrun-vms-e2e; do
  find "$root" -name "libkrun.log" 2>/dev/null | while read -r f; do
    echo "--- $f ---"
    cat "$f" 2>/dev/null || echo "(empty or unreadable)"
  done
done

echo "=== passt.log files ==="
for root in /data/nexus/e2e /data/nexus/libkrun-vms-e2e; do
  find "$root" -name "passt.log" 2>/dev/null | while read -r f; do
    echo "--- $f ---"
    cat "$f" 2>/dev/null || echo "(empty or unreadable)"
  done
done

echo "=== /data/nexus e2e workdir listings ==="
for root in /data/nexus/e2e /data/nexus/libkrun-vms-e2e; do
  echo "--- $root ---"
  find "$root" -maxdepth 2 2>/dev/null | head -60 || true
done

echo "=== host-config dir contents ==="
for root in /data/nexus/e2e /data/nexus/libkrun-vms-e2e; do
  find "$root" -name "host-config" -type d 2>/dev/null | while read -r d; do
    echo "--- $d ---"
    find "$d" -maxdepth 3 2>/dev/null || echo "(empty or unreadable)"
  done
done

echo "=== HOME fixtures ==="
ls -la "$HOME/.gitconfig" "$HOME/.ssh/" "$HOME/.config/nexus/" 2>/dev/null || true
