//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ── XDG user-scoped paths ─────────────────────────────────────────────────────

func xdgShareNexus() string {
	if s := os.Getenv("XDG_DATA_HOME"); s != "" {
		return filepath.Join(s, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}

func xdgStateNexus() string {
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(s, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "nexus")
}

func xdgLocalBin() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

func rootlessVMDir() string {
	return filepath.Join(xdgShareNexus(), "vm")
}

func rootlessRootlessDirState() string {
	return filepath.Join(xdgShareNexus(), "rootless")
}

// ── Canonical rootless asset paths ───────────────────────────────────────────

func rootlessKernelPath() string {
	return filepath.Join(rootlessVMDir(), "vmlinux.bin")
}

func rootlessRootfsPath() string {
	return filepath.Join(rootlessVMDir(), "rootfs.ext4")
}

// rootlessRootfsDirPath returns the directory-form of the rootfs used by
// libkrun container mode (krun_set_root). Created alongside rootfs.ext4.
func rootlessRootfsDirPath() string {
	return filepath.Join(rootlessVMDir(), "rootfs-dir")
}

func rootlessPasstPath() string {
	return filepath.Join(xdgLocalBin(), "passt")
}

// rootlessLibkrunLibDir returns the canonical location for libkrun shared
// libraries installed by the rootless bootstrap.
func rootlessLibkrunLibDir() string {
	return filepath.Join(xdgShareNexus(), "lib")
}

func rootlessStateFilePath() string {
	return filepath.Join(rootlessRootlessDirState(), "state.json")
}

func prebuiltVMBundleURL() string {
	return strings.TrimSpace(os.Getenv("NEXUS_PREBUILT_VM_ASSET_URL"))
}

func prebuiltVMBundleSHA256() string {
	return strings.TrimSpace(os.Getenv("NEXUS_PREBUILT_VM_ASSET_SHA256"))
}

func rootlessSubnetFilePath() string {
	return filepath.Join(xdgShareNexus(), "rootless", "bridge-subnet")
}

// ── State model ───────────────────────────────────────────────────────────────

type rootlessState struct {
	SchemaVersion int    `json:"schema_version"`
	InstalledAt   string `json:"installed_at"`
	NexusVersion  string `json:"nexus_version"`
	// Driver is the runtime driver that was bootstrapped (currently always "libkrun").
	Driver            string `json:"driver,omitempty"`
	NetworkBackend    string `json:"network_backend"`
	NetworkBackendBin string `json:"network_backend_bin"`
	KernelPath        string `json:"kernel_path"`
	RootfsPath        string `json:"rootfs_path"`
	KVMAccess         string `json:"kvm_access"`
	LibkrunLibDir     string `json:"libkrun_lib_dir,omitempty"`
}

// ── Phase event (for --json output) ──────────────────────────────────────────

type bootstrapPhaseEvent struct {
	Phase   string `json:"phase"`
	Status  string `json:"status"` // "start" | "ok" | "error"
	Message string `json:"message,omitempty"`
}

func emitPhase(w io.Writer, emitJSON bool, phase, status, msg string) {
	if emitJSON {
		ev := bootstrapPhaseEvent{Phase: phase, Status: status, Message: msg}
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "%s\n", data)
	} else if msg != "" {
		fmt.Fprintf(w, "[%s] %s: %s\n", phase, status, msg)
	} else {
		fmt.Fprintf(w, "[%s] %s\n", phase, status)
	}
}

// ── Download URLs ─────────────────────────────────────────────────────────────

const (
	passtVersion = "20250501.0"
)

// vmKernelURL returns the URL for the Linux kernel image (vmlinux ELF).
// We use the Firecracker vmlinux ELF because it is a known-good format for
// libkrun on x86_64 (ELF, not the PE bzImage that Ubuntu ships).  The Ubuntu
// vmlinuz is a bzImage that libkrun's x86_64 loader does not handle when
// misdetected as RAW, so we avoid it as a primary source.
func vmKernelURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/aarch64/vmlinux-5.10.239"
	default:
		return "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/vmlinux-5.10.239"
	}
}

func vmKernelFallbackURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return "https://cloud-images.ubuntu.com/minimal/releases/noble/release/unpacked/ubuntu-24.04-minimal-cloudimg-arm64-vmlinuz-generic"
	default:
		return "https://cloud-images.ubuntu.com/minimal/releases/noble/release/unpacked/ubuntu-24.04-minimal-cloudimg-amd64-vmlinuz-generic"
	}
}

func vmSquashfsURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return "https://cloud-images.ubuntu.com/minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-arm64-root.tar.xz"
	default:
		return "https://cloud-images.ubuntu.com/minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-amd64-root.tar.xz"
	}
}

// vmRootfsIsSquashfs reports whether the cached rootfs archive is squashfs
// (legacy squashfs CI format) rather than a tar.xz (Ubuntu CDN format).
func vmRootfsIsSquashfs(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := f.Read(magic[:]); err != nil {
		return false
	}
	// squashfs magic: 'sqsh' (0x73717368) or 'hsqs' (0x68737173)
	return magic == [4]byte{0x73, 0x71, 0x73, 0x68} || magic == [4]byte{0x68, 0x73, 0x71, 0x73}
}

func passtDownloadURL() string {
	// passt static binary from upstream builds
	switch runtime.GOARCH {
	case "arm64":
		return "https://passt.top/builds/latest/aarch64/passt.avx2"
	default:
		return "https://passt.top/builds/latest/x86_64/passt.avx2"
	}
}

// smolvmSourceDir returns the default smolvm installation directory.
// smolvm (https://github.com/smol-machines/smolvm) ships libkrun + libkrunfw.
func smolvmSourceDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".smolvm", "lib")
}

// rootlessLibkrunBinDir returns the canonical location for the nexus-libkrun-vm
// helper binary extracted at bootstrap.
func rootlessLibkrunBinDir() string {
	return filepath.Join(xdgShareNexus(), "bin")
}

// LibkrunVMBinPath returns the path where the nexus-libkrun-vm binary is
// extracted during bootstrap. The manager uses this to spawn VMs.
func LibkrunVMBinPath() string {
	return filepath.Join(rootlessLibkrunBinDir(), "nexus-libkrun-vm")
}

// ── libkrun library + VM binary installation ──────────────────────────────────

// installLibkrunLibsRootless ensures libkrun.so, libkrunfw.so, and the
// nexus-libkrun-vm helper binary are present in ~/.local/share/nexus/{lib,bin}/.
//
// Source priority:
//  1. Already installed (idempotent — checks for the sentinel libkrun.so.1).
//  2. Embedded bytes (build was done with -tags libkrun and the artifacts were
//     bundled into the binary at compile time) — preferred, fully offline.
//  3. Copy from ~/.smolvm/lib/ if smolvm is installed — fallback for dev builds
//     that don't have the embed artifacts (e.g. `go run` or unpackaged builds).
func installLibkrunLibsRootless(w io.Writer, emitJSON bool) error {
	libDir := rootlessLibkrunLibDir()
	binDir := rootlessLibkrunBinDir()

	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return fmt.Errorf("create libkrun lib dir %s: %w", libDir, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create libkrun bin dir %s: %w", binDir, err)
	}

	// ── Path 1: extract from embedded bytes ───────────────────────────────────
	if len(embeddedLibkrunVM) > 0 && len(embeddedLibkrun) > 0 && len(embeddedLibkrunfw) > 0 {
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

	// Idempotent fallback path: already installed if the main library and VM helper exist.
	if _, err := os.Stat(filepath.Join(libDir, "libkrun.so.1")); err == nil {
		if _, err2 := os.Stat(LibkrunVMBinPath()); err2 == nil {
			return nil
		}
	}

	// ── Path 2: copy from smolvm installation (dev fallback) ────────────────
	srcDir := smolvmSourceDir()
	if _, err := os.Stat(filepath.Join(srcDir, "libkrun.so.1")); err != nil {
		return fmt.Errorf(
			"prerequisite_error.libkrun_missing: libkrun shared library not found.\n\n" +
				"This binary was not built with embedded libkrun artifacts.\n" +
				"Remediation:\n" +
				"  Re-deploy using the deploy script (downloads libkrun automatically):\n" +
				"    REMOTE_HOST=user@host scripts/remote/deploy-libkrun.sh\n\n" +
				"  Or install smolvm for local dev (pre-built libkrun.so included):\n" +
				"    SMOLVM_VER=v0.5.19\n" +
				"    curl -fsSL https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VER}/smolvm-${SMOLVM_VER#v}-linux-x86_64.tar.gz \\\n" +
				"      | tar -xz --strip-components=2 -C ~/.smolvm/lib smolvm-${SMOLVM_VER#v}-linux-x86_64/lib\n",
		)
	}

	fmt.Fprintf(w, "  installing libkrun libraries from %s (smolvm fallback)...\n", srcDir)
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read smolvm lib dir: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "libkrun") {
			continue
		}
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(libDir, name)
		fi, err := os.Lstat(src)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(src)
			if err != nil {
				continue
			}
			_ = os.Remove(dst)
			if err := os.Symlink(target, dst); err != nil {
				return fmt.Errorf("symlink %s: %w", name, err)
			}
		} else {
			if err := copyFileRootless(src, dst); err != nil {
				return fmt.Errorf("copy %s: %w", name, err)
			}
		}
	}

	// The smolvm fallback has no nexus-libkrun-vm binary — it expects the
	// main nexus binary to be used with the `libkrun-vm` subcommand.
	// That only works for unpackaged dev builds; the manager handles this
	// via NexusBin fallback in that case.

	if _, err := os.Stat(filepath.Join(libDir, "libkrun.so.1")); err != nil {
		return fmt.Errorf("libkrun.so.1 not found after installation attempt")
	}
	fmt.Fprintf(w, "  libkrun libraries installed at %s\n", libDir)
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

// ── Prerequisite checks ───────────────────────────────────────────────────────

func checkKVMAccess() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf(
			"prerequisite_error.kvm_access: /dev/kvm is not accessible (O_RDWR): %w\n\n"+
				"Remediation:\n"+
				"  sudo usermod -aG kvm $USER\n"+
				"  # Then log out and log back in (or run: newgrp kvm)",
			err,
		)
	}
	f.Close()
	return nil
}

func checkNetworkBackend() error {
	path := rootlessPasstPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if p, err := exec.LookPath("passt"); err == nil {
		_ = p
		return nil
	}
	return fmt.Errorf(
		"prerequisite_error.network_backend_missing: passt not found at %s or in PATH\n\n"+
			"Remediation: nexus daemon start will auto-install passt during asset-install phase",
		path,
	)
}

// ── Main entry point ──────────────────────────────────────────────────────────

// RunRootlessBootstrap runs preflight + asset-install + runtime-verify in sequence.
// On success, the daemon-launch phase can proceed.
// emitJSON controls whether phase events are emitted as JSON lines (for --json flag).
func RunRootlessBootstrap(w io.Writer, emitJSON bool, driver string) error {
	// ── Phase: preflight ───────────────────────────────────────────────────────
	emitPhase(w, emitJSON, "preflight", "start", "checking prerequisites")

	if err := checkKVMAccess(); err != nil {
		emitPhase(w, emitJSON, "preflight", "error", err.Error())
		return err
	}
	emitPhase(w, emitJSON, "preflight", "ok", "KVM accessible")

	// ── Phase: asset-install (idempotent) ─────────────────────────────────────
	emitPhase(w, emitJSON, "asset-install", "start", "installing runtime assets")

	if err := ensureRootlessDirs(); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: create dirs: %w", err)
	}

	// libkrun shared libraries.
	if err := installLibkrunLibsRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: libkrun: %w", err)
	}

	// passt is required for both drivers in VM mode networking.
	if err := installPasstRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: passt: %w", err)
	}

	// Optional fast path: hydrate kernel/rootfs from a prebuilt VM bundle
	// published by CI/release. This avoids repeated local rootfs baking.
	if err := installPrebuiltVMBundleRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: prebuilt-vm-bundle: %w", err)
	}

	if err := installVMKernelRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: kernel: %w", err)
	}

	// libkrun requires a block-backed rootfs image so guest
	// root behaves like a real VM filesystem (root can install arbitrary packages).
	if err := installVMRootfsRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: rootfs: %w", err)
	}

	emitPhase(w, emitJSON, "asset-install", "ok", "all assets present")

	// ── Phase: runtime-verify ─────────────────────────────────────────────────
	emitPhase(w, emitJSON, "runtime-verify", "start", "verifying runtime")

	if err := verifyRootlessRuntime(driver); err != nil {
		emitPhase(w, emitJSON, "runtime-verify", "error", err.Error())
		return fmt.Errorf("runtime-verify: %w", err)
	}

	if err := persistRootlessState(driver); err != nil {
		// Non-fatal: log but don't fail startup
		emitPhase(w, emitJSON, "runtime-verify", "ok", fmt.Sprintf("verified (state persist skipped: %v)", err))
	} else {
		emitPhase(w, emitJSON, "runtime-verify", "ok", "runtime verified and state persisted")
	}

	// daemon-launch phase is emitted by the caller after this returns.
	return nil
}

func installPrebuiltVMBundleRootless(w io.Writer, emitJSON bool) error {
	url := prebuiltVMBundleURL()
	if url == "" {
		return nil
	}

	// Skip if both core artifacts already exist.
	if _, kerr := os.Stat(rootlessKernelPath()); kerr == nil {
		if _, rerr := os.Stat(rootlessRootfsPath()); rerr == nil {
			fmt.Fprintf(w, "  prebuilt VM bundle configured but assets already present; skipping download\n")
			return nil
		}
	}

	fmt.Fprintf(w, "  downloading prebuilt VM bundle from %s ...\n", url)
	emitPhase(w, emitJSON, "asset-install", "start", "downloading prebuilt VM bundle")
	data, err := httpDownload(url)
	if err != nil {
		return fmt.Errorf("download prebuilt vm bundle: %w", err)
	}

	if want := prebuiltVMBundleSHA256(); want != "" {
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, want) {
			return fmt.Errorf("prebuilt vm bundle checksum mismatch: want %s got %s", want, got)
		}
	}

	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open bundle gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var kernelData []byte
	var rootfsData []byte
	var agentHashData []byte
	var bakedStampData []byte

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read bundle tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Base(strings.TrimSpace(hdr.Name))
		switch name {
		case "vmlinux.bin":
			kernelData, err = io.ReadAll(tr)
		case "rootfs.ext4":
			rootfsData, err = io.ReadAll(tr)
		case "rootfs-agent.sha256":
			agentHashData, err = io.ReadAll(tr)
		case "rootfs-baked-v4":
			bakedStampData, err = io.ReadAll(tr)
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s from bundle: %w", name, err)
		}
	}

	if len(kernelData) == 0 || len(rootfsData) == 0 {
		return fmt.Errorf("prebuilt vm bundle missing required files (vmlinux.bin, rootfs.ext4)")
	}

	if err := atomicWriteFile(rootlessKernelPath(), kernelData, 0o644); err != nil {
		return fmt.Errorf("install bundled kernel: %w", err)
	}
	if err := atomicWriteFile(rootlessRootfsPath(), rootfsData, 0o600); err != nil {
		return fmt.Errorf("install bundled rootfs: %w", err)
	}

	if len(agentHashData) > 0 {
		_ = atomicWriteFile(filepath.Join(xdgStateNexus(), "rootfs-agent.sha256"), bytes.TrimSpace(agentHashData), 0o644)
	}
	if len(bakedStampData) > 0 {
		_ = atomicWriteFile(filepath.Join(xdgStateNexus(), "rootfs-baked-v4"), bytes.TrimSpace(bakedStampData), 0o644)
	}

	fmt.Fprintf(w, "  installed prebuilt VM assets (kernel + rootfs)\n")
	return nil
}

// ── Directory setup ───────────────────────────────────────────────────────────

func ensureRootlessDirs() error {
	dirs := []string{
		xdgLocalBin(),
		rootlessVMDir(),
		rootlessRootlessDirState(),
		xdgStateNexus(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

func needsInstall(dest string, newContent []byte) bool {
	existing, err := os.ReadFile(dest)
	if err != nil {
		return true
	}
	return !bytes.Equal(existing, newContent)
}

// ── passt installation ────────────────────────────────────────────────────────

func installPasstRootless(w io.Writer, emitJSON bool) error {
	dest := rootlessPasstPath()

	// Already installed (check by existence; version upgrade is manual)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}

	// Try system passt first
	if p, err := exec.LookPath("passt"); err == nil {
		// Symlink or copy to user bin
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read system passt: %w", err)
		}
		if err := atomicWriteExec(dest, data); err != nil {
			return fmt.Errorf("install passt from system: %w", err)
		}
		fmt.Fprintf(w, "  passt installed from system at %s\n", dest)
		return nil
	}

	// Download static passt binary
	fmt.Fprintf(w, "  downloading passt...\n")
	url := passtDownloadURL()
	data, err := httpDownload(url)
	if err != nil {
		return fmt.Errorf(
			"bootstrap_error.asset_install: download passt from %s: %w\n\n"+
				"Alternative: install passt via your package manager:\n"+
				"  sudo apt install passt       # Ubuntu/Debian\n"+
				"  sudo dnf install passt       # Fedora/RHEL",
			url, err,
		)
	}
	if err := atomicWriteExec(dest, data); err != nil {
		return fmt.Errorf("install passt: %w", err)
	}
	fmt.Fprintf(w, "  passt installed at %s\n", dest)
	return nil
}

// ── VM kernel installation ────────────────────────────────────────────────────

func installVMKernelRootless(w io.Writer, emitJSON bool) error {
	dest := rootlessKernelPath()
	if _, err := os.Stat(dest); err == nil {
		return nil // already present
	}

	// Priority 1: embedded kernel (release builds ship with a compiled ELF).
	if len(embeddedKernel) > 0 {
		// Sanity check: must be a real ELF, not the placeholder text file.
		if len(embeddedKernel) > 4 && embeddedKernel[0] == 0x7f && embeddedKernel[1] == 'E' && embeddedKernel[2] == 'L' && embeddedKernel[3] == 'F' {
			fmt.Fprintf(w, "  extracting embedded kernel (%d bytes)...\n", len(embeddedKernel))
			if err := atomicWriteFile(dest, embeddedKernel, 0o644); err == nil {
				fmt.Fprintf(w, "  kernel installed at %s (embedded)\n", dest)
				return nil
			}
			fmt.Fprintf(w, "  warning: embedded kernel extract failed: %v\n", err)
		}
	}

	// Priority 2: temp cache, then legacy /var/lib/nexus, then download.
	legacyKernel := "/var/lib/nexus/vmlinux.bin"
	for _, src := range []string{
		filepath.Join(os.TempDir(), "nexus-vmlinux.bin"),
		legacyKernel,
	} {
		if fi, err := os.Stat(src); err == nil && fi.Size() > 1024*1024 {
			fmt.Fprintf(w, "  using kernel from %s\n", src)
			if err := copyFileRootless(src, dest); err == nil {
				fmt.Fprintf(w, "  kernel installed at %s\n", dest)
				return nil
			}
		}
	}

	// Priority 3: download fallback (for dev builds without embedded kernel).
	fmt.Fprintf(w, "  downloading VM kernel...\n")
	urls := []string{vmKernelURL(), vmKernelFallbackURL()}
	var data []byte
	var lastErr error
	for i, url := range urls {
		fmt.Fprintf(w, "  kernel source %d/%d: %s\n", i+1, len(urls), url)
		data, lastErr = httpDownloadWithRetry(url, 3, 75*time.Second)
		if lastErr == nil {
			break
		}
		fmt.Fprintf(w, "  kernel download failed from source %d/%d: %v\n", i+1, len(urls), lastErr)
	}
	if lastErr != nil {
		return fmt.Errorf("download kernel failed: %w", lastErr)
	}
	if err := atomicWriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("install kernel: %w", err)
	}
	fmt.Fprintf(w, "  kernel installed at %s (%d bytes)\n", dest, len(data))
	return nil
}

// ── VM rootfs installation ────────────────────────────────────────────────────

func installVMRootfsRootless(w io.Writer, emitJSON bool) error {
	dest := rootlessRootfsPath()
	if _, err := os.Stat(dest); err == nil {
		return nil // already present (agent injection handled separately)
	}

	// Migration only: copy from legacy privileged-setup rootfs if present and
	// readable.  We never look in /tmp — that path is world-writable and may be
	// owned by root from old implode-based setups.
	legacyRootfs := "/var/lib/nexus/rootfs.ext4"
	if fi, err := os.Stat(legacyRootfs); err == nil && fi.Size() > 1024*1024 {
		fmt.Fprintf(w, "  migrating rootfs from legacy /var/lib/nexus...\n")
		if err := copyFileRootless(legacyRootfs, dest); err == nil {
			_ = os.Chmod(dest, 0o600)
			fmt.Fprintf(w, "  rootfs installed at %s\n", dest)
			return nil
		}
		fmt.Fprintf(w, "  warning: legacy migration failed, building from squashfs\n")
	}

	if err := buildRootlessRootfs(w, dest); err != nil {
		return err
	}
	// A freshly-built rootfs does NOT have the agent injected yet.
	// Remove the cached agent hash so ensureGuestAgent knows to
	// re-inject rather than skip because the old hash still matches.
	_ = os.Remove(filepath.Join(xdgStateNexus(), "rootfs-agent.sha256"))
	// Also remove the bake stamp so the next daemon start re-bakes the rootfs
	// with developer tools baked into the fresh image.
	_ = os.Remove(filepath.Join(xdgStateNexus(), "rootfs-baked-v3"))
	return nil
}

// installRootfsDirForLibkrun downloads the Ubuntu minimal root archive and
// extracts it directly to rootfs-dir/ — the only rootfs artifact libkrun needs.
// It deliberately skips building rootfs.ext4, saving 5–10 min of ext4 work.
//
// Download format: Ubuntu 24.04 minimal cloud image tar.xz (~30 MB compressed).
// Served from Ubuntu's CDN (Akamai).
//
// Legacy upgrade path: if the old ubuntu.squashfs cache file is present it is
// extracted with unsquashfs so users who already downloaded it don't re-download.
func installRootfsDirForLibkrun(w io.Writer, emitJSON bool) error {
	permDir := rootlessRootfsDirPath()
	if _, err := os.Stat(permDir); err == nil {
		return nil // already present
	}

	emitPhase(w, emitJSON, "asset-install", "start", "installing rootfs directory for libkrun")

	tmpDir, err := os.MkdirTemp("", "nexus-rootfs-dir-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// ── Step 1: obtain the archive (tar.xz preferred; squashfs fallback) ──────
	archivePath := filepath.Join(rootlessVMDir(), "ubuntu.squashfs") // legacy cache path
	archiveIsSquashfs := false

	if _, err := os.Stat(archivePath); err == nil && vmRootfsIsSquashfs(archivePath) {
		archiveIsSquashfs = true
		fmt.Fprintf(w, "  using cached squashfs (legacy) from %s\n", archivePath)
	} else {
		// Download the Ubuntu minimal tar.xz — CDN-served, typically ~30 MB.
		tarxzCache := filepath.Join(rootlessVMDir(), "ubuntu-minimal.tar.xz")
		if _, err := os.Stat(tarxzCache); err != nil {
			url := vmSquashfsURL()
			fmt.Fprintf(w, "  downloading Ubuntu minimal rootfs from %s ...\n", url)
			emitPhase(w, emitJSON, "asset-install", "start", "downloading Ubuntu minimal rootfs (~30 MB)")
			data, dlErr := httpDownload(url)
			if dlErr != nil {
				return fmt.Errorf("download rootfs tar.xz: %w", dlErr)
			}
			if err := atomicWriteFile(tarxzCache, data, 0o644); err != nil {
				return fmt.Errorf("save rootfs tar.xz: %w", err)
			}
		} else {
			fmt.Fprintf(w, "  using cached tar.xz from %s\n", tarxzCache)
		}
		archivePath = tarxzCache
	}

	// ── Step 2: extract the archive into a temp dir ───────────────────────────
	extractDir := filepath.Join(tmpDir, "rootfs")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("mkdir extract dir: %w", err)
	}

	fakerootDBPath := filepath.Join(tmpDir, "fakeroot.db")
	hasFakeroot := func() bool { _, err := exec.LookPath("fakeroot"); return err == nil }()
	runCmd := func(name string, args ...string) *exec.Cmd {
		if hasFakeroot {
			allArgs := append([]string{"-s", fakerootDBPath, "-i", fakerootDBPath, name}, args...)
			return exec.Command("fakeroot", allArgs...)
		}
		return exec.Command(name, args...)
	}

	fmt.Fprintf(w, "  extracting rootfs archive...\n")
	emitPhase(w, emitJSON, "asset-install", "start", "extracting rootfs archive")

	if archiveIsSquashfs {
		if _, err := exec.LookPath("unsquashfs"); err != nil {
			return fmt.Errorf("unsquashfs not found — install squashfs-tools")
		}
		unsqCmd := runCmd("unsquashfs", "-d", extractDir, archivePath)
		unsqCmd.Stdout = w
		unsqCmd.Stderr = w
		if err := unsqCmd.Run(); err != nil {
			return fmt.Errorf("unsquashfs: %w", err)
		}
	} else {
		// tar.xz: extract directly into extractDir; -J = xz decompression.
		tarCmd := runCmd("tar", "-xJf", archivePath, "-C", extractDir)
		tarCmd.Stdout = w
		tarCmd.Stderr = w
		if err := tarCmd.Run(); err != nil {
			return fmt.Errorf("tar -xJf: %w", err)
		}
	}

	// ── Step 3: patch and install to permanent location ───────────────────────
	fmt.Fprintf(w, "  patching rootfs for root environment...\n")
	if err := patchRootfsForRoot(extractDir); err != nil {
		fmt.Fprintf(w, "  warning: rootfs patch incomplete (non-fatal): %v\n", err)
	}

	if err := os.MkdirAll(permDir, 0o755); err != nil {
		return fmt.Errorf("mkdir rootfs-dir: %w", err)
	}
	cpCmd := exec.Command("cp", "-a", extractDir+"/.", permDir)
	if out, cpErr := cpCmd.CombinedOutput(); cpErr != nil {
		_ = os.RemoveAll(permDir)
		return fmt.Errorf("cp -a to rootfs-dir: %w: %s", cpErr, strings.TrimSpace(string(out)))
	}

	// ── Step 4: inject the nexus agent into the rootfs directory ─────────────
	// For libkrun container mode, krun_set_exec runs the agent from inside the
	// rootfs-dir. Inject the same agent binary that's embedded in the nexus binary.
	if len(embeddedAgent) > 0 {
		agentDst := filepath.Join(permDir, "usr", "local", "bin", "nexus-guest-agent")
		if mkErr := os.MkdirAll(filepath.Dir(agentDst), 0o755); mkErr == nil {
			if writeErr := os.WriteFile(agentDst, embeddedAgent, 0o755); writeErr == nil {
				fmt.Fprintf(w, "  agent injected into rootfs-dir at %s\n", agentDst)
			} else {
				fmt.Fprintf(w, "  warning: agent inject failed (non-fatal): %v\n", writeErr)
			}
		}
	}

	fmt.Fprintf(w, "  rootfs directory ready at %s\n", permDir)
	emitPhase(w, emitJSON, "asset-install", "ok", "rootfs directory installed")
	return nil
}

// copyFileRootless copies a file in userspace (no root needed).
func copyFileRootless(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	dir := filepath.Dir(dst)
	base := filepath.Base(dst)
	out, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// patchRootfsForRoot writes OS-level configuration files into the extracted
// rootfs directory so that root has full, unrestricted control of the VM
// environment from first boot — no per-operation workarounds needed.
func patchRootfsForRoot(rootfsDir string) error {
	type fileSpec struct {
		path    string
		content string
		mode    os.FileMode
	}

	// Ubuntu minimal cloud image ships /etc/resolv.conf as a symlink to
	// ../run/systemd/resolve/stub-resolv.conf — a path that doesn't exist
	// inside the libkrun guest. Remove the symlink so the agent can write
	// a real file; in TSI mode the agent adds "options use-vc" so DNS
	// queries go over TCP (the only transport TSI supports for DNS).
	resolvConf := filepath.Join(rootfsDir, "etc", "resolv.conf")
	if fi, err := os.Lstat(resolvConf); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(resolvConf)
	}

	files := []fileSpec{
		// /tmp: ensure world-writable sticky bit — squashfs default is 0755.
		// The agent also mounts tmpfs here on boot, but this covers any path
		// that does not go through the agent's mountKernelFilesystems.
		// (The directory entry in the ext4 image sets the on-disk mode.)

		// sysctl: allow unprivileged user namespaces and IP forwarding for
		// Docker inside the VM.
		{
			path: "etc/sysctl.d/99-nexus.conf",
			content: `# Nexus VM: enable features needed for Docker and development tools.
net.ipv4.ip_forward = 1
kernel.unprivileged_userns_clone = 1
`,
			mode: 0o644,
		},
	}

	var errs []string
	for _, f := range files {
		dest := filepath.Join(rootfsDir, f.path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			errs = append(errs, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dest), err))
			continue
		}
		if err := os.WriteFile(dest, []byte(f.content), f.mode); err != nil {
			errs = append(errs, fmt.Sprintf("write %s: %v", f.path, err))
		}
	}

	// Fix /tmp to be world-writable with sticky bit.
	tmpDir := filepath.Join(rootfsDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o1777); err != nil {
		errs = append(errs, fmt.Sprintf("mkdir tmp: %v", err))
	} else if err := os.Chmod(tmpDir, 0o1777); err != nil {
		errs = append(errs, fmt.Sprintf("chmod tmp: %v", err))
	}

	// Create the apt cache directories that are absent in minimal squashfs images.
	// Without these, apt-get install fails immediately with "directory missing".
	for _, dir := range []string{
		"var/cache/apt/archives/partial",
		"var/lib/apt/lists/partial",
		"var/lib/dpkg/info",
		"var/lib/dpkg/updates",
		"var/lib/dpkg/parts",
	} {
		if err := os.MkdirAll(filepath.Join(rootfsDir, dir), 0o755); err != nil {
			errs = append(errs, fmt.Sprintf("mkdir %s: %v", dir, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

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

// ── Runtime verification ──────────────────────────────────────────────────────

func verifyRootlessRuntime(driver string) error {
	// libkrun shared library.
	libDir := rootlessLibkrunLibDir()
	if _, err := os.Stat(filepath.Join(libDir, "libkrun.so.1")); err != nil {
		return fmt.Errorf("libkrun.so.1 not found at %s", libDir)
	}

	// Verify passt.
	passt := rootlessPasstPath()
	if _, err := os.Stat(passt); err != nil {
		return fmt.Errorf("passt not found at %s", passt)
	}

	// Verify kernel.
	if _, err := os.Stat(rootlessKernelPath()); err != nil {
		return fmt.Errorf("kernel not found at %s", rootlessKernelPath())
	}

	// Verify rootfs.
	if _, err := os.Stat(rootlessRootfsPath()); err != nil {
		return fmt.Errorf("rootfs not found at %s", rootlessRootfsPath())
	}

	return nil
}

// ── State persistence ─────────────────────────────────────────────────────────

func persistRootlessState(driver string) error {
	effectiveDriver := driver
	if effectiveDriver == "" {
		effectiveDriver = "libkrun"
	}
	state := rootlessState{
		SchemaVersion:     1,
		InstalledAt:       time.Now().UTC().Format(time.RFC3339),
		Driver:            effectiveDriver,
		NetworkBackend:    "tsi", // built into libkrun
		NetworkBackendBin: "",
		KernelPath:        rootlessKernelPath(),
		RootfsPath:        rootlessRootfsPath(),
		KVMAccess:         "ok",
		LibkrunLibDir:     rootlessLibkrunLibDir(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rootlessStateFilePath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(rootlessStateFilePath(), data, 0o644)
}

// ── Utility helpers ───────────────────────────────────────────────────────────

func atomicWriteFile(dest string, data []byte, mode os.FileMode) error {
	return writeFileAtomic(dest, data, mode)
}

func atomicWriteExec(dest string, data []byte) error {
	return atomicWriteFile(dest, data, 0o755)
}

func httpDownload(url string) ([]byte, error) {
	return httpDownloadWithTimeout(url, 10*time.Minute)
}

func httpDownloadWithTimeout(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func httpDownloadWithRetry(url string, attempts int, timeout time.Duration) ([]byte, error) {
	if attempts < 1 {
		attempts = 1
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		data, err := httpDownloadWithTimeout(url, timeout)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if attempt < attempts {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
	}
	return nil, fmt.Errorf("download %s failed after %d attempts: %w", url, attempts, lastErr)
}

func downloadAndExtractTarGz(url, innerPath string) ([]byte, error) {
	data, err := httpDownload(url)
	if err != nil {
		return nil, err
	}

	gr, err := gzip.NewReader(strings.NewReader(""))
	_ = gr
	// Use io.NopCloser pattern
	gzReader, err := gzip.NewReader(
		func() io.Reader {
			return &readerBytes{data: data}
		}(),
	)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	defer gzReader.Close()

	tr := tar.NewReader(gzReader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		if header.Name == innerPath || strings.HasSuffix(header.Name, "/"+filepath.Base(innerPath)) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive from %s", innerPath, url)
}

type readerBytes struct {
	data   []byte
	offset int
}

func (r *readerBytes) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
