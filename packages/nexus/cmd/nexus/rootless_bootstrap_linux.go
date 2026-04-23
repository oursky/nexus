//go:build linux

package main

import (
	"archive/tar"
	"compress/gzip"
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

func rootlessFirecrackerPath() string {
	return filepath.Join(xdgLocalBin(), "firecracker")
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
	SchemaVersion     int    `json:"schema_version"`
	InstalledAt       string `json:"installed_at"`
	NexusVersion      string `json:"nexus_version"`
	FirecrackerBin    string `json:"firecracker_bin"`
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
	Status  string `json:"status"`  // "start" | "ok" | "error"
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
	firecrackerVersion = "v1.13.0"
	passtVersion       = "20250501.0"
)

func firecrackerDownloadURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return fmt.Sprintf("https://github.com/firecracker-microvm/firecracker/releases/download/%s/firecracker-%s-aarch64.tgz", firecrackerVersion, firecrackerVersion)
	default: // amd64
		return fmt.Sprintf("https://github.com/firecracker-microvm/firecracker/releases/download/%s/firecracker-%s-x86_64.tgz", firecrackerVersion, firecrackerVersion)
	}
}

func firecrackerBinaryName() string {
	switch runtime.GOARCH {
	case "arm64":
		return fmt.Sprintf("release-%s-aarch64/firecracker-%s-aarch64", firecrackerVersion, firecrackerVersion)
	default:
		return fmt.Sprintf("release-%s-x86_64/firecracker-%s-x86_64", firecrackerVersion, firecrackerVersion)
	}
}

func vmKernelURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/aarch64/vmlinux-5.10.239"
	default:
		return "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/vmlinux-5.10.239"
	}
}

func vmSquashfsURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/aarch64/ubuntu-24.04.squashfs"
	default:
		return "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.13/x86_64/ubuntu-24.04.squashfs"
	}
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

// ── libkrun library installation ──────────────────────────────────────────────

// installLibkrunLibsRootless ensures libkrun.so and libkrunfw.so are present
// in the canonical nexus lib directory (~/.local/share/nexus/lib/).
//
// Source priority:
//  1. Already installed (idempotent).
//  2. Copy from ~/.smolvm/lib/ if smolvm is installed.
//  3. Otherwise emit a remediation hint.
func installLibkrunLibsRootless(w io.Writer, emitJSON bool) error {
	destDir := rootlessLibkrunLibDir()

	// Already installed if the main library is present.
	if _, err := os.Stat(filepath.Join(destDir, "libkrun.so.1")); err == nil {
		return nil
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create libkrun lib dir %s: %w", destDir, err)
	}

	srcDir := smolvmSourceDir()
	if _, err := os.Stat(filepath.Join(srcDir, "libkrun.so.1")); err != nil {
		return fmt.Errorf(
			"prerequisite_error.libkrun_missing: libkrun shared library not found at %s\n\n"+
				"Remediation:\n"+
				"  Install smolvm (provides libkrun + libkrunfw):\n"+
				"    curl -fsSL https://raw.githubusercontent.com/smol-machines/smolvm/main/install.sh | sh\n"+
				"  Or build from source: https://github.com/containers/libkrun\n"+
				"  After installation, re-run: nexus daemon start --driver=libkrun",
			srcDir,
		)
	}

	fmt.Fprintf(w, "  installing libkrun libraries from %s...\n", srcDir)
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
		dst := filepath.Join(destDir, name)

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

	// Verify the main library landed.
	if _, err := os.Stat(filepath.Join(destDir, "libkrun.so.1")); err != nil {
		return fmt.Errorf("libkrun.so.1 not found after installation attempt")
	}
	fmt.Fprintf(w, "  libkrun libraries installed at %s\n", destDir)
	return nil
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
// driver selects which runtime-specific assets to install: "firecracker", "libkrun", or "" (both).
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

	// Firecracker binary — skip when running libkrun-only.
	if driver == "" || driver == "firecracker" {
		if err := installFirecrackerRootless(w, emitJSON); err != nil {
			emitPhase(w, emitJSON, "asset-install", "error", err.Error())
			return fmt.Errorf("asset-install: firecracker: %w", err)
		}
	}

	// libkrun shared libraries — only needed for the libkrun driver.
	if driver == "libkrun" {
		if err := installLibkrunLibsRootless(w, emitJSON); err != nil {
			emitPhase(w, emitJSON, "asset-install", "error", err.Error())
			return fmt.Errorf("asset-install: libkrun: %w", err)
		}
	}

	if err := installPasstRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: passt: %w", err)
	}

	if err := installVMKernelRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: kernel: %w", err)
	}

	if err := installVMRootfsRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: rootfs: %w", err)
	}

	if err := ensureDataVolumeXFS(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: data-volume: %w", err)
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

// ── Data volume detection ─────────────────────────────────────────────────────

const (
	// xfsImagePath is the sparse backing file for the XFS loop mount.
	xfsImagePath = "/data/nexus.img"
	// xfsMountPoint is where the XFS volume is expected to be mounted.
	xfsMountPoint = "/data/nexus"
)

// xfsSetupOneLiner is the single sudo command users run once as a prerequisite
// before the first `nexus daemon start`. It creates a 200 GiB sparse XFS
// image, mounts it, and registers it in /etc/fstab for persistence.
const xfsSetupOneLiner = `sudo bash -c "mkdir -p /data && truncate -s 200G /data/nexus.img && mkfs.xfs /data/nexus.img && mkdir -p /data/nexus && mount -o loop /data/nexus.img /data/nexus && chown $(id -u):$(id -g) /data/nexus && echo '/data/nexus.img /data/nexus xfs loop,defaults 0 0' >> /etc/fstab"`

// ensureDataVolumeXFS verifies that the Nexus data volume is on XFS (required
// for O(1) reflink workspace clones). If it is not, it prints the one-liner
// the user needs to run once and returns an error.
func ensureDataVolumeXFS(w io.Writer, emitJSON bool) error {
	dir := nexusDataVolumeDirBootstrap()
	if reflinkAvailableBootstrap(dir) {
		fmt.Fprintf(w, "  data volume: %s (%s) — XFS reflink ready\n", dir, filesystemTypeBootstrap(dir))
		return nil
	}
	fsType := filesystemTypeBootstrap(dir)
	return fmt.Errorf(
		"data volume at %s is %q — nexus requires XFS.\n\n"+
			"Run this once on the Linux host, then re-run daemon start:\n\n"+
			"  %s",
		dir, fsType, xfsSetupOneLiner,
	)
}

// nexusDataVolumeDirBootstrap mirrors daemon.nexusDataVolumeDir without
// importing the daemon package (bootstrap runs before daemon wiring).
func nexusDataVolumeDirBootstrap() string {
	if v := strings.TrimSpace(os.Getenv("NEXUS_DATA_DIR")); v != "" {
		return v
	}
	const dedicated = "/data/nexus"
	if fi, err := os.Stat(dedicated); err == nil && fi.IsDir() {
		if f, err := os.CreateTemp(dedicated, ".nexus-probe-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			return dedicated
		}
	}
	return xdgStateNexus()
}

// filesystemTypeBootstrap returns a human-readable filesystem type for dir.
func filesystemTypeBootstrap(dir string) string {
	out, err := exec.Command("stat", "-f", "-c", "%T", dir).Output()
	if err == nil {
		t := strings.TrimSpace(string(out))
		if t != "" {
			return t
		}
	}
	return "unknown"
}

// reflinkAvailableBootstrap probes whether dir supports cp --reflink=always.
func reflinkAvailableBootstrap(dir string) bool {
	probe, err := os.CreateTemp(dir, ".nexus-reflink-probe-src-*")
	if err != nil {
		return false
	}
	probe.Close()
	dst := probe.Name() + "-dst"
	defer func() {
		os.Remove(probe.Name())
		os.Remove(dst)
	}()
	return exec.Command("cp", "--reflink=always", probe.Name(), dst).Run() == nil
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

// ── Firecracker installation ──────────────────────────────────────────────────

func installFirecrackerRootless(w io.Writer, emitJSON bool) error {
	dest := rootlessFirecrackerPath()

	// Check if already installed with embedded binary first
	if len(embeddedFirecracker) > 0 {
		if needsInstall(dest, embeddedFirecracker) {
			fmt.Fprintf(w, "  installing firecracker from embedded binary\n")
			if err := atomicWriteExec(dest, embeddedFirecracker); err != nil {
				return fmt.Errorf("install embedded firecracker: %w", err)
			}
		}
		return nil
	}

	// Already installed
	if _, err := os.Stat(dest); err == nil {
		return nil
	}

	// Download from GitHub releases
	fmt.Fprintf(w, "  downloading firecracker %s...\n", firecrackerVersion)
	url := firecrackerDownloadURL()
	binName := firecrackerBinaryName()

	data, err := downloadAndExtractTarGz(url, binName)
	if err != nil {
		return fmt.Errorf("download firecracker from %s: %w", url, err)
	}
	if err := atomicWriteExec(dest, data); err != nil {
		return fmt.Errorf("install firecracker: %w", err)
	}
	fmt.Fprintf(w, "  firecracker installed at %s\n", dest)
	return nil
}

func needsInstall(dest string, newContent []byte) bool {
	existing, err := os.ReadFile(dest)
	if err != nil {
		return true
	}
	return len(existing) != len(newContent)
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

	// Priority: temp cache, then legacy /var/lib/nexus, then download.
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

	fmt.Fprintf(w, "  downloading VM kernel...\n")
	url := vmKernelURL()
	data, err := httpDownload(url)
	if err != nil {
		return fmt.Errorf("download kernel from %s: %w", url, err)
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
	// Remove the cached agent hash so ensureFirecrackerGuestAgent knows to
	// re-inject rather than skip because the old hash still matches.
	_ = os.Remove(filepath.Join(xdgStateNexus(), "rootfs-agent.sha256"))
	return nil
}

// copyFileRootless copies a file in userspace (no root needed).
func copyFileRootless(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
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

	files := []fileSpec{
		// apt: disable the _apt sandbox entirely and always run as root.
		// Without this, apt-key creates temp files as the _apt user and
		// fails when /tmp is not world-writable.
		{
			path: "etc/apt/apt.conf.d/99nexus-root",
			content: `// Nexus: run apt as root — the VM is a fully trusted root environment.
APT::Sandbox::User "root";
`,
			mode: 0o644,
		},
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

	// Cache squashfs under ~/.local/share/nexus/vm/ — user-owned, survives
	// reboots, and avoids /tmp world-writable / root-ownership problems.
	squashfsCache := filepath.Join(rootlessVMDir(), "ubuntu.squashfs")
	squashfsPath := squashfsCache
	if _, err := os.Stat(squashfsCache); err != nil {
		fmt.Fprintf(w, "  downloading squashfs rootfs (this may take a few minutes)...\n")
		url := vmSquashfsURL()
		data, err := httpDownload(url)
		if err != nil {
			return fmt.Errorf("download squashfs from %s: %w", url, err)
		}
		if err := atomicWriteFile(squashfsCache, data, 0o644); err != nil {
			return fmt.Errorf("save squashfs: %w", err)
		}
	} else {
		fmt.Fprintf(w, "  using cached squashfs from %s\n", squashfsCache)
	}

	// Extract squashfs (no root needed)
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		return fmt.Errorf(
			"bootstrap_error.asset_install: unsquashfs not found\n\n"+
				"Remediation:\n"+
				"  sudo apt install squashfs-tools",
		)
	}

	rootfsDir := filepath.Join(tmpDir, "rootfs")
	fmt.Fprintf(w, "  extracting squashfs...\n")

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

	unsqCmd := runCmd("unsquashfs", "-d", rootfsDir, squashfsPath)
	unsqCmd.Stdout = w
	unsqCmd.Stderr = w
	if err := unsqCmd.Run(); err != nil {
		return fmt.Errorf("unsquashfs: %w", err)
	}

	// Patch the extracted rootfs so root has full control on first boot.
	fmt.Fprintf(w, "  patching rootfs for root environment...\n")
	if err := patchRootfsForRoot(rootfsDir); err != nil {
		fmt.Fprintf(w, "  warning: rootfs patch incomplete (non-fatal): %v\n", err)
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
		if err := buildCmd.Run(); err != nil {
			return fmt.Errorf("genext2fs: %w", err)
		}
		return nil
	}

	// mke2fs -d path (fakeroot-aware, same runCmd wrapper)
	mke2fsCmd := runCmd("mke2fs", "-F", "-t", "ext4", "-d", rootfsDir, "-q", dest, rootfsSize)
	mke2fsCmd.Stdout = w
	mke2fsCmd.Stderr = w
	if err := mke2fsCmd.Run(); err != nil {
		return fmt.Errorf("mke2fs -d: %w", err)
	}
	return nil
}


// ── Runtime verification ──────────────────────────────────────────────────────

func verifyRootlessRuntime(driver string) error {
	// Firecracker binary — only required for the firecracker driver.
	if driver == "" || driver == "firecracker" {
		fc := rootlessFirecrackerPath()
		if _, err := os.Stat(fc); err != nil {
			return fmt.Errorf("firecracker not found at %s", fc)
		}
		if out, err := exec.Command(fc, "--version").Output(); err != nil {
			return fmt.Errorf("firecracker --version: %w", err)
		} else if !strings.Contains(string(out), "Firecracker") {
			return fmt.Errorf("unexpected firecracker --version output: %s", strings.TrimSpace(string(out)))
		}
	}

	// libkrun shared library — only required for the libkrun driver.
	if driver == "libkrun" {
		libDir := rootlessLibkrunLibDir()
		if _, err := os.Stat(filepath.Join(libDir, "libkrun.so.1")); err != nil {
			return fmt.Errorf("libkrun.so.1 not found at %s", libDir)
		}
	}

	// Verify passt (required for both drivers).
	passt := rootlessPasstPath()
	if _, err := os.Stat(passt); err != nil {
		return fmt.Errorf("passt not found at %s", passt)
	}

	// Verify kernel (required for both drivers).
	if _, err := os.Stat(rootlessKernelPath()); err != nil {
		return fmt.Errorf("kernel not found at %s", rootlessKernelPath())
	}

	// Verify rootfs (required for both drivers).
	if _, err := os.Stat(rootlessRootfsPath()); err != nil {
		return fmt.Errorf("rootfs not found at %s", rootlessRootfsPath())
	}

	return nil
}

// ── State persistence ─────────────────────────────────────────────────────────

func persistRootlessState(driver string) error {
	state := rootlessState{
		SchemaVersion:     1,
		InstalledAt:       time.Now().UTC().Format(time.RFC3339),
		NetworkBackend:    "passt",
		NetworkBackendBin: rootlessPasstPath(),
		KernelPath:        rootlessKernelPath(),
		RootfsPath:        rootlessRootfsPath(),
		KVMAccess:         "ok",
	}
	if driver == "" || driver == "firecracker" {
		state.FirecrackerBin = rootlessFirecrackerPath()
	}
	if driver == "libkrun" {
		state.LibkrunLibDir = rootlessLibkrunLibDir()
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
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func atomicWriteExec(dest string, data []byte) error {
	return atomicWriteFile(dest, data, 0o755)
}

func httpDownload(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
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
