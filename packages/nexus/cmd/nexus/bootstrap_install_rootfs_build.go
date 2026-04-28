//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func buildRootlessRootfs(w io.Writer, dest string) error {
	// Rootless rootfs build sequence:
	// 1. Download squashfs (or use cached)
	// 2. unsquashfs to temp dir (no root)
	// 3. genext2fs to create ext4 (no root) OR mke2fs+debugfs
	tmpDir, err := os.MkdirTemp("", "nexus-rootfs-build-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Rootfs archive cache under ~/.local/share/nexus/vm/.
	// Prefer Ubuntu minimal tar.xz; support legacy squashfs cache for upgrades.
	archivePath := filepath.Join(rootlessVMDir(), "ubuntu.squashfs")
	archiveIsSquashfs := false
	if _, err := os.Stat(archivePath); err == nil && vmRootfsIsSquashfs(archivePath) {
		archiveIsSquashfs = true
		fmt.Fprintf(w, "  using cached squashfs rootfs from %s\n", archivePath)
	} else {
		tarxzCache := filepath.Join(rootlessVMDir(), "ubuntu-minimal.tar.xz")
		archivePath = tarxzCache
		if _, err := os.Stat(tarxzCache); err != nil {
			fmt.Fprintf(w, "  downloading Ubuntu minimal rootfs (tar.xz)...\n")
			url := vmSquashfsURL()
			data, err := httpDownload(url)
			if err != nil {
				return fmt.Errorf("download rootfs from %s: %w", url, err)
			}
			if err := atomicWriteFile(tarxzCache, data, 0o644); err != nil {
				return fmt.Errorf("save rootfs tar.xz: %w", err)
			}
		} else {
			fmt.Fprintf(w, "  using cached tar.xz rootfs from %s\n", tarxzCache)
		}
	}

	rootfsDir := filepath.Join(tmpDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir rootfs dir: %w", err)
	}
	fmt.Fprintf(w, "  extracting rootfs archive...\n")

	// Wrap with fakeroot if available so unsquashfs can preserve the original
	// UIDs/GIDs from the squashfs (e.g. root-owned dirs like /root, /etc/ssh).
	// Without fakeroot, everything is owned by the calling user (uid ≠ 0),
	// which breaks sshd authorised_keys checks and setuid binaries.
	fakerootDBPath := filepath.Join(tmpDir, "fakeroot.db")
	hasFakeroot := func() bool {
		_, err := exec.LookPath("fakeroot")
		return err == nil
	}()

	runCmd := func(name string, args ...string) *exec.Cmd {
		if hasFakeroot {
			allArgs := append([]string{"-s", fakerootDBPath, "-i", fakerootDBPath, name}, args...)
			return exec.Command("fakeroot", allArgs...)
		}
		return exec.Command(name, args...)
	}

	if archiveIsSquashfs {
		if _, err := exec.LookPath("unsquashfs"); err != nil {
			return fmt.Errorf(
				"bootstrap_error.asset_install: unsquashfs not found\n\n" +
					"Remediation:\n" +
					"  sudo apt install squashfs-tools",
			)
		}
		unsqCmd := runCmd("unsquashfs", "-d", rootfsDir, archivePath)
		unsqCmd.Stdout = w
		unsqCmd.Stderr = w
		if err := unsqCmd.Run(); err != nil {
			return fmt.Errorf("unsquashfs: %w", err)
		}
	} else {
		tarCmd := runCmd("tar", "-xJf", archivePath, "-C", rootfsDir)
		tarCmd.Stdout = w
		tarCmd.Stderr = w
		if err := tarCmd.Run(); err != nil {
			return fmt.Errorf("tar -xJf: %w", err)
		}
	}

	// Require at least one ext4 builder.
	hasGenext2fs := func() bool { _, err := exec.LookPath("genext2fs"); return err == nil }()
	hasMke2fs := func() bool { _, err := exec.LookPath("mke2fs"); return err == nil }()
	if !hasGenext2fs && !hasMke2fs {
		return fmt.Errorf(
			"bootstrap_error.asset_install: neither genext2fs nor mke2fs found\n\n" +
				"Remediation (choose one):\n" +
				"  sudo apt install genext2fs   # preferred\n" +
				"  sudo apt install e2fsprogs   # provides mke2fs",
		)
	}

	// Build ext4 image from directory tree — try genext2fs first (preferred,
	// faster), fall back to mke2fs -d (slower but always correct).
	fmt.Fprintf(w, "  building ext4 rootfs image...\n")
	// 8 GB gives the guest agent room to apt-get install the full developer
	// toolchain (docker.io, nodejs, build-essential, etc.) on first boot.
	const rootfsSize = "8g"
	const rootfsSizeBlocks = "8388608" // 8 GiB in 1 KiB blocks for genext2fs

	var ext4Err error
	if hasGenext2fs {
		buildCmd := runCmd("genext2fs",
			"-b", rootfsSizeBlocks,
			"-N", "524288",
			"-d", rootfsDir,
			"-m", "5",
			dest,
		)
		buildCmd.Stdout = w
		buildCmd.Stderr = w
		ext4Err = buildCmd.Run()
		if ext4Err != nil {
			ext4Err = fmt.Errorf("genext2fs: %w", ext4Err)
		}
	} else {
		// mke2fs -d path (fakeroot-aware, same runCmd wrapper)
		mke2fsCmd := runCmd("mke2fs", "-F", "-t", "ext4", "-d", rootfsDir, "-q", dest, rootfsSize)
		mke2fsCmd.Stdout = w
		mke2fsCmd.Stderr = w
		if err := mke2fsCmd.Run(); err != nil {
			ext4Err = fmt.Errorf("mke2fs -d: %w", err)
		}
	}
	if ext4Err != nil {
		return ext4Err
	}

	// Save the rootfs as a directory for libkrun container mode.
	// krun_set_root() requires a host directory; libkrunfw's virtiofs kernel
	// maps host uid→0 inside the VM so ownership is transparent to the guest.
	permDir := rootlessRootfsDirPath()
	if _, statErr := os.Stat(permDir); statErr != nil {
		fmt.Fprintf(w, "  saving rootfs directory for libkrun container mode...\n")
		if mkErr := os.MkdirAll(permDir, 0o755); mkErr != nil {
			fmt.Fprintf(w, "  warning: mkdir rootfs-dir: %v\n", mkErr)
		} else {
			// cp -a src/. dest copies the tree into the pre-created dest dir.
			cpCmd := exec.Command("cp", "-a", rootfsDir+"/.", permDir)
			if out, cpErr := cpCmd.CombinedOutput(); cpErr != nil {
				fmt.Fprintf(w, "  warning: failed to save rootfs-dir: %v: %s\n", cpErr, strings.TrimSpace(string(out)))
				_ = os.RemoveAll(permDir) // clean up partial copy
			} else {
				fmt.Fprintf(w, "  rootfs directory saved at %s\n", permDir)
			}
		}
	}
	return nil
}
