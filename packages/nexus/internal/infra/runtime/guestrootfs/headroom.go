// Package guestrootfs adjusts pre-built libkrun/macvm guest ext4 images after download:
// release pipelines ship filesystems shrunk to minimum size; hosts grow them back to
// operational headroom with truncate(2) + resize2fs(8).
package guestrootfs

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultOperationalBytes is the on-disk size we restore after downloading a shrunk
// artifact (matches bootstrap_install_rootfs_build.go “8 GB toolchain room”).
const DefaultOperationalBytes int64 = 8 << 30

// OperationalTargetBytes returns the configured operational disk size for the guest rootfs.
func OperationalTargetBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_VM_ROOTFS_OPERATIONAL_BYTES")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if gib := strings.TrimSpace(os.Getenv("NEXUS_VM_ROOTFS_OPERATIONAL_GIB")); gib != "" {
		if n, err := strconv.ParseInt(gib, 10, 64); err == nil && n > 0 {
			return n << 30
		}
	}
	return DefaultOperationalBytes
}

// EnsureOperationalHeadroom grows imagePath to OperationalTargetBytes when the file is
// smaller (shrunk release artifact). Idempotent when already large enough.
func EnsureOperationalHeadroom(imagePath string) error {
	target := OperationalTargetBytes()
	fi, err := os.Stat(imagePath)
	if err != nil {
		return err
	}
	if fi.Size() >= target {
		return nil
	}

	resize2fs, err := exec.LookPath("resize2fs")
	if err != nil {
		return fmt.Errorf("resize2fs not on PATH (install e2fsprogs): %w", err)
	}

	e2fsckPath, err := exec.LookPath("e2fsck")
	if err != nil {
		return fmt.Errorf("e2fsck not on PATH (install e2fsprogs): %w", err)
	}

	if out, err := exec.Command(e2fsckPath, "-f", "-p", imagePath).CombinedOutput(); err != nil {
		return fmt.Errorf("e2fsck before grow: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Truncate(imagePath, target); err != nil {
		return fmt.Errorf("truncate guest rootfs to %d bytes: %w", target, err)
	}
	if out, err := exec.Command(resize2fs, imagePath).CombinedOutput(); err != nil {
		return fmt.Errorf("resize2fs grow: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
