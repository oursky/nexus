//go:build linux

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ── libkrun library + VM binary installation ──────────────────────────────────

// installLibkrunLibsRootless extracts the embedded libkrun shared libraries and
// nexus-libkrun-vm binary into ~/.local/share/nexus/{lib,bin}/.
// Non-libkrun builds skip this step (embedded stubs are empty).
func installLibkrunLibsRootless(w io.Writer, emitJSON bool) error {
	// Non-libkrun builds don't need libkrun.
	if len(embeddedLibkrunVM) == 0 {
		return nil
	}

	libDir := rootlessLibkrunLibDir()
	binDir := rootlessLibkrunBinDir()

	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return fmt.Errorf("create libkrun lib dir %s: %w", libDir, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create libkrun bin dir %s: %w", binDir, err)
	}

	vmBin := filepath.Join(binDir, "nexus-libkrun-vm")
	libkrunSo := filepath.Join(libDir, "libkrun.so")
	libkrunfwSo := filepath.Join(libDir, "libkrunfw.so.5.3.0")
	if needsInstall(vmBin, embeddedLibkrunVM) || needsInstall(libkrunSo, embeddedLibkrun) || needsInstall(libkrunfwSo, embeddedLibkrunfw) {
		fmt.Fprintf(w, "  extracting embedded libkrun libraries and VM binary...\n")
		if err := extractLibkrunEmbeds(libDir, binDir); err != nil {
			return fmt.Errorf("extract libkrun embeds: %w", err)
		}
		fmt.Fprintf(w, "  libkrun installed from embedded build artifacts\n")
	}
	return nil
}

// extractLibkrunEmbeds writes the embedded .so files and nexus-libkrun-vm binary
// to the canonical nexus directories.
func extractLibkrunEmbeds(libDir, binDir string) error {
	// libkrun.so — actual library file; create versioned name + symlinks.
	libkrunSo := filepath.Join(libDir, "libkrun.so")
	if err := writeFileAtomic(libkrunSo, embeddedLibkrun, 0o755); err != nil {
		return fmt.Errorf("write libkrun.so: %w", err)
	}
	// libkrun.so.1 → libkrun.so
	ensureSymlink(libDir, "libkrun.so.1", "libkrun.so")

	// libkrunfw — extract, then create version symlink chain.
	libkrunfwSo := filepath.Join(libDir, "libkrunfw.so.5.3.0")
	if err := writeFileAtomic(libkrunfwSo, embeddedLibkrunfw, 0o755); err != nil {
		return fmt.Errorf("write libkrunfw.so.5.3.0: %w", err)
	}
	ensureSymlink(libDir, "libkrunfw.so.5", "libkrunfw.so.5.3.0")
	ensureSymlink(libDir, "libkrunfw.so", "libkrunfw.so.5")

	// nexus-libkrun-vm binary.
	vmBin := filepath.Join(binDir, "nexus-libkrun-vm")
	if err := writeFileAtomic(vmBin, embeddedLibkrunVM, 0o755); err != nil {
		return fmt.Errorf("write nexus-libkrun-vm: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path atomically (write to .tmp, then rename).
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmpFile, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// ensureSymlink creates (or replaces) a symlink name→target in dir.
func ensureSymlink(dir, name, target string) {
	dst := filepath.Join(dir, name)
	_ = os.Remove(dst)
	_ = os.Symlink(target, dst)
}

func needsInstall(dest string, newContent []byte) bool {
	existing, err := os.ReadFile(dest)
	if err != nil {
		return true
	}
	return !bytes.Equal(existing, newContent)
}

// ── passt installation ────────────────────────────────────────────────────────

// installPasstRootless extracts the embedded passt binary into ~/.local/bin/passt.
// Non-libkrun builds skip this step (embedded stub is empty).
func installPasstRootless(w io.Writer, emitJSON bool) error {
	// Non-libkrun builds don't need passt.
	if len(embeddedPasst) == 0 {
		return nil
	}

	dest := rootlessPasstPath()
	if needsInstall(dest, embeddedPasst) {
		fmt.Fprintf(w, "  extracting embedded passt (%d bytes)...\n", len(embeddedPasst))
		if err := atomicWriteExec(dest, embeddedPasst); err != nil {
			return fmt.Errorf("extract embedded passt: %w", err)
		}
		fmt.Fprintf(w, "  passt installed at %s (embedded)\n", dest)
	}
	return nil
}

// ── VM kernel installation ────────────────────────────────────────────────────

// installVMKernelRootless extracts the embedded vmlinux ELF kernel into
// ~/.local/share/nexus/vm/vmlinux.bin.
// Non-libkrun builds skip this step (embedded stub is empty).
func installVMKernelRootless(w io.Writer, emitJSON bool) error {
	// Non-libkrun builds don't need a VM kernel.
	if len(embeddedKernel) == 0 {
		return nil
	}

	dest := rootlessKernelPath()
	if _, err := os.Stat(dest); err == nil {
		return nil // already present
	}

	// Sanity check: must be a real ELF, not the placeholder text file.
	if len(embeddedKernel) > 4 && embeddedKernel[0] == 0x7f && embeddedKernel[1] == 'E' && embeddedKernel[2] == 'L' && embeddedKernel[3] == 'F' {
		fmt.Fprintf(w, "  extracting embedded kernel (%d bytes)...\n", len(embeddedKernel))
		if err := atomicWriteFile(dest, embeddedKernel, 0o644); err != nil {
			return fmt.Errorf("extract embedded kernel: %w", err)
		}
		fmt.Fprintf(w, "  kernel installed at %s (embedded)\n", dest)
		return nil
	}

	return fmt.Errorf("embedded kernel is not a valid ELF — rebuild with embedded assets (run scripts/ci/build-nexus-libkrun.sh)")
}

// ── Utility helpers ───────────────────────────────────────────────────────────

func atomicWriteFile(dest string, data []byte, mode os.FileMode) error {
	return writeFileAtomic(dest, data, mode)
}

func atomicWriteExec(dest string, data []byte) error {
	return atomicWriteFile(dest, data, 0o755)
}
