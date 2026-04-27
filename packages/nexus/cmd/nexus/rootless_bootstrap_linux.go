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
	// passt static binary from upstream builds.
	// NOTE: passt.top only provides x86_64 builds. aarch64 must be installed
	// via the system package manager (e.g. apt install passt).
	switch runtime.GOARCH {
	case "arm64":
		return ""
	default:
		return "https://passt.top/builds/latest/x86_64/passt"
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

// verifyRootlessRuntime confirms all runtime assets are present.
// For libkrun driver this means libkrun libs, passt, kernel and rootfs.
// For sandbox driver only the rootfs is checked (when applicable).
func verifyRootlessRuntime(driver string) error {
	isLibkrun := driver == "libkrun" || driver == ""

	if isLibkrun {
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
