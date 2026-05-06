#!/usr/bin/env bash
# scripts/local/rootfs-compress-bench.sh
# Benchmark different rootfs compression strategies.
# Run on the Linux daemon where rootfs.ext4 lives.
#
# Usage:
#   sudo bash scripts/local/rootfs-compress-bench.sh /path/to/rootfs.ext4
#   sudo NEXUS_COMPRESS_LEVEL=19 bash scripts/local/rootfs-bench.sh /data/nexus/libkrun-vms/<ws-id>/rootfs.ext4
set -euo pipefail

ROOTFS="${1:?Usage: $0 <rootfs.ext4>}"
LEVEL="${NEXUS_COMPRESS_LEVEL:-19}"
TMPDIR="${TMPDIR:-/tmp}"
MNT="$TMPDIR/rootfs-bench-mnt"

if [ ! -f "$ROOTFS" ]; then
    echo "ERROR: $ROOTFS not found"
    exit 1
fi

ORIGINAL_SIZE=$(stat --format=%s "$ROOTFS")
DISK_USAGE=$(stat --format=%b --dereference "$ROOTFS")
BLKSIZE=$(stat --format=%B "$ROOTFS")
APPARENT_SIZE=$((DISK_USAGE * BLKSIZE))

echo "=== Rootfs Compression Benchmark ==="
echo "File:       $ROOTFS"
echo "File size:  $(numfmt --to=iec $ORIGINAL_SIZE)"
echo "Disk usage: $(numfmt --to=iec $APPARENT_SIZE) (actual blocks on disk)"
echo "Sparse gap: $(numfmt --to=iec $((ORIGINAL_SIZE - APPARENT_SIZE))) (holes)"
echo "zstd level: $LEVEL"
echo ""

cleanup() {
    sudo umount "$MNT" 2>/dev/null || true
    rm -rf "$MNT"
}
trap cleanup EXIT
mkdir -p "$MNT"

# ── Strategy A: tar → zstd ──────────────────────────────────────
echo "── Strategy A: mount → tar → zstd-$LEVEL ──"
sudo mount -o loop,ro "$ROOTFS" "$MNT"
USED_BYTES=$(sudo du -sb "$MNT" | cut -f1)
echo "Used space in rootfs: $(numfmt --to=iec $USED_BYTES)"

OUT_A="$TMPDIR/rootfs-bench-strategy-a.tar.zst"
time sudo tar -cf - -C "$MNT" . | zstd -$LEVEL -o "$OUT_A" --force
SIZE_A=$(stat --format=%s "$OUT_A")
echo "Result: $(numfmt --to=iec $SIZE_A)"
echo "Ratio:  $(echo "scale=2; $SIZE_A * 100 / $ORIGINAL_SIZE" | bc)% of original"
echo ""

sudo umount "$MNT"

# ── Strategy B: resize2fs -M → e2image -r → zstd ────────────────
echo "── Strategy B: resize2fs -M + e2image -r + zstd-$LEVEL ──"
# Work on a copy to avoid modifying the original
CP="$TMPDIR/rootfs-bench-copy.ext4"
cp --sparse=always "$ROOTFS" "$CP"

sudo e2fsck -fy "$CP" 2>/dev/null || true
sudo resize2fs -M "$CP"
MINIMAL_SIZE=$(stat --format=%s "$CP")
echo "After resize2fs -M: $(numfmt --to=iec $MINIMAL_SIZE)"

OUT_B="$TMPDIR/rootfs-bench-strategy-b.raw.zst"
# e2image -r creates raw image with zeroed free blocks
sudo e2image -r "$CP" "${OUT_B%.zst}" 2>/dev/null
time zstd -$LEVEL -o "$OUT_B" --force "${OUT_B%.zst}"
SIZE_B=$(stat --format=%s "$OUT_B")
echo "Result: $(numfmt --to=iec $SIZE_B)"
echo "Ratio:  $(echo "scale=2; $SIZE_B * 100 / $ORIGINAL_SIZE" | bc)% of original"
rm -f "$CP" "${OUT_B%.zst}"
echo ""

# ── Strategy C: resize2fs -M → mount → tar → zstd ──────────────
echo "── Strategy C: resize2fs -M + mount → tar → zstd-$LEVEL ──"
CP="$TMPDIR/rootfs-bench-copy.ext4"
cp --sparse=always "$ROOTFS" "$CP"
sudo e2fsck -fy "$CP" 2>/dev/null || true
sudo resize2fs -M "$CP"

sudo mount -o loop,ro "$CP" "$MNT"
OUT_C="$TMPDIR/rootfs-bench-strategy-c.tar.zst"
time sudo tar -cf - -C "$MNT" . | zstd -$LEVEL -o "$OUT_C" --force
SIZE_C=$(stat --format=%s "$OUT_C")
echo "Result: $(numfmt --to=iec $SIZE_C)"
echo "Ratio:  $(echo "scale=2; $SIZE_C * 100 / $ORIGINAL_SIZE" | bc)% of original"
sudo umount "$MNT"
rm -f "$CP"
echo ""

# ── Strategy D: direct zstd (baseline) ─────────────────────────
echo "── Baseline: raw zstd-$LEVEL (no optimization) ──"
OUT_D="$TMPDIR/rootfs-bench-strategy-d.ext4.zst"
time zstd -$LEVEL -o "$OUT_D" --force "$ROOTFS"
SIZE_D=$(stat --format=%s "$OUT_D")
echo "Result: $(numfmt --to=iec $SIZE_D)"
echo "Ratio:  $(echo "scale=2; $SIZE_D * 100 / $ORIGINAL_SIZE" | bc)% of original"
echo ""

# ── Summary ─────────────────────────────────────────────────────
echo "=== Summary ==="
printf "%-40s %12s %8s\n" "Strategy" "Size" "Ratio"
printf "%-40s %12s %8s\n" "Original ext4" "$(numfmt --to=iec $ORIGINAL_SIZE)" "100%"
printf "%-40s %12s %8s\n" "A: tar+zstd" "$(numfmt --to=iec $SIZE_A)" "$(echo "scale=1; $SIZE_A * 100 / $ORIGINAL_SIZE" | bc)%"
printf "%-40s %12s %8s\n" "B: resize2fs+e2image+zstd" "$(numfmt --to=iec $SIZE_B)" "$(echo "scale=1; $SIZE_B * 100 / $ORIGINAL_SIZE" | bc)%"
printf "%-40s %12s %8s\n" "C: resize2fs+tar+zstd" "$(numfmt --to=iec $SIZE_C)" "$(echo "scale=1; $SIZE_C * 100 / $ORIGINAL_SIZE" | bc)%"
printf "%-40s %12s %8s\n" "D: raw zstd (baseline)" "$(numfmt --to=iec $SIZE_D)" "$(echo "scale=1; $SIZE_D * 100 / $ORIGINAL_SIZE" | bc)%"

WINNER=$(echo "$SIZE_A $SIZE_B $SIZE_C $SIZE_D" | tr ' ' '\n' | sort -n | head -1)
echo ""
echo "Best: $(numfmt --to=iec $WINNER)"

# Cleanup
rm -f "$OUT_A" "$OUT_B" "$OUT_C" "$OUT_D"
