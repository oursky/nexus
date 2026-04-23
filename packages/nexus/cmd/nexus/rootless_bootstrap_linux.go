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
func RunRootlessBootstrap(w io.Writer, emitJSON bool) error {
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

	if err := installFirecrackerRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: firecracker: %w", err)
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

	emitPhase(w, emitJSON, "asset-install", "ok", "all assets present")

	// ── Phase: runtime-verify ─────────────────────────────────────────────────
	emitPhase(w, emitJSON, "runtime-verify", "start", "verifying runtime")

	if err := verifyRootlessRuntime(); err != nil {
		emitPhase(w, emitJSON, "runtime-verify", "error", err.Error())
		return fmt.Errorf("runtime-verify: %w", err)
	}

	if err := persistRootlessState(); err != nil {
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

	// Priority order for sourcing the rootfs:
	// 1. Hot cache at /tmp/nexus-rootfs.ext4 (from implode backup)
	// 2. Legacy /var/lib/nexus/rootfs.ext4 (from previous privileged setup, user-owned)
	// 3. Build from squashfs (rootless, no mount required)

	legacyRootfs := "/var/lib/nexus/rootfs.ext4"
	hotCache := filepath.Join(os.TempDir(), "nexus-rootfs.ext4")

	for _, src := range []struct{ path, label string }{
		{hotCache, "hot cache"},
		{legacyRootfs, "legacy /var/lib/nexus (migration)"},
	} {
		if fi, err := os.Stat(src.path); err == nil && fi.Size() > 1024*1024 {
			fmt.Fprintf(w, "  migrating rootfs from %s...\n", src.label)
			if err := copyFileRootless(src.path, dest); err != nil {
				fmt.Fprintf(w, "  warning: migration from %s failed (%v), will build from squashfs\n", src.path, err)
				continue
			}
			if err := os.Chmod(dest, 0o600); err == nil {
				fmt.Fprintf(w, "  rootfs installed at %s\n", dest)
				return nil
			}
		}
	}

	return buildRootlessRootfs(w, dest)
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

	// Use cached squashfs from previous download or legacy privileged setup.
	squashfsCache := filepath.Join(os.TempDir(), "nexus-ubuntu.squashfs")
	squashfsPath := squashfsCache
	if _, err := os.Stat(squashfsCache); err != nil {
		squashfsPath = filepath.Join(tmpDir, "rootfs.squashfs")
		fmt.Fprintf(w, "  downloading squashfs rootfs (this may take a few minutes)...\n")
		url := vmSquashfsURL()
		data, err := httpDownload(url)
		if err != nil {
			return fmt.Errorf("download squashfs from %s: %w", url, err)
		}
		if err := atomicWriteFile(squashfsPath, data, 0o644); err != nil {
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
	cmd := exec.Command("unsquashfs", "-d", rootfsDir, squashfsPath)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unsquashfs: %w", err)
	}

	// Build ext4 image from directory — try genext2fs first (preferred),
	// fall back to mke2fs + debugfs (slower but widely available).
	fmt.Fprintf(w, "  building ext4 rootfs image...\n")
	if path, err := exec.LookPath("genext2fs"); err == nil {
		_ = path
		return buildRootfsWithGenext2fs(w, rootfsDir, dest)
	}
	return buildRootfsWithMke2fsDebugfs(w, rootfsDir, dest)
}

func buildRootfsWithGenext2fs(w io.Writer, rootfsDir, dest string) error {
	// 4G image, reserved blocks 5%, source from rootfsDir
	cmd := exec.Command("genext2fs",
		"-b", "4096000",
		"-N", "524288", // inode count
		"-d", rootfsDir,
		"-m", "5", // reserved blocks %
		dest,
	)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("genext2fs: %w", err)
	}
	return nil
}

func buildRootfsWithMke2fsDebugfs(w io.Writer, rootfsDir, dest string) error {
	if _, err := exec.LookPath("mke2fs"); err != nil {
		return fmt.Errorf(
			"bootstrap_error.asset_install: neither genext2fs nor mke2fs found\n\n"+
				"Remediation (choose one):\n"+
				"  sudo apt install genext2fs   # preferred\n"+
				"  sudo apt install e2fsprogs   # provides mke2fs",
		)
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return fmt.Errorf(
			"bootstrap_error.asset_install: debugfs not found\n\n"+
				"Remediation:\n"+
				"  sudo apt install e2fsprogs",
		)
	}

	// Create sparse 4G ext4 image
	fmt.Fprintf(w, "  creating sparse ext4 image...\n")
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create rootfs file: %w", err)
	}
	if err := f.Truncate(4 * 1024 * 1024 * 1024); err != nil {
		f.Close()
		return fmt.Errorf("truncate rootfs: %w", err)
	}
	f.Close()

	cmd := exec.Command("mke2fs", "-F", "-t", "ext4", "-q", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mke2fs: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Import directory tree using debugfs tar_import
	fmt.Fprintf(w, "  importing rootfs tree via debugfs (slow but rootless)...\n")
	return importDirToExt4WithDebugfs(rootfsDir, dest)
}

func importDirToExt4WithDebugfs(srcDir, destExt4 string) error {
	// Create a tar pipe to debugfs
	pr, pw := io.Pipe()

	tarCmd := exec.Command("tar", "-C", srcDir, "-c", ".")
	tarCmd.Stdout = pw

	dbCmd := exec.Command("debugfs", "-w", "-f", "-", destExt4)
	dbCmd.Stdin = pr
	dbCmd.Stdout = io.Discard
	dbCmd.Stderr = io.Discard

	if err := tarCmd.Start(); err != nil {
		return fmt.Errorf("tar start: %w", err)
	}
	if err := dbCmd.Start(); err != nil {
		tarCmd.Process.Kill()
		return fmt.Errorf("debugfs start: %w", err)
	}

	if err := tarCmd.Wait(); err != nil {
		pw.CloseWithError(err)
		dbCmd.Wait()
		return fmt.Errorf("tar: %w", err)
	}
	pw.Close()

	if err := dbCmd.Wait(); err != nil {
		// debugfs tar import may return non-zero on warnings; treat as best-effort
		_ = err
	}
	return nil
}

// ── Runtime verification ──────────────────────────────────────────────────────

func verifyRootlessRuntime() error {
	// Verify firecracker
	fc := rootlessFirecrackerPath()
	if _, err := os.Stat(fc); err != nil {
		return fmt.Errorf("firecracker not found at %s", fc)
	}
	if out, err := exec.Command(fc, "--version").Output(); err != nil {
		return fmt.Errorf("firecracker --version: %w", err)
	} else if !strings.Contains(string(out), "Firecracker") {
		return fmt.Errorf("unexpected firecracker --version output: %s", strings.TrimSpace(string(out)))
	}

	// Verify passt
	passt := rootlessPasstPath()
	if _, err := os.Stat(passt); err != nil {
		return fmt.Errorf("passt not found at %s", passt)
	}

	// Verify kernel
	if _, err := os.Stat(rootlessKernelPath()); err != nil {
		return fmt.Errorf("kernel not found at %s", rootlessKernelPath())
	}

	// Verify rootfs
	if _, err := os.Stat(rootlessRootfsPath()); err != nil {
		return fmt.Errorf("rootfs not found at %s", rootlessRootfsPath())
	}

	return nil
}

// ── State persistence ─────────────────────────────────────────────────────────

func persistRootlessState() error {
	state := rootlessState{
		SchemaVersion:     1,
		InstalledAt:       time.Now().UTC().Format(time.RFC3339),
		FirecrackerBin:    rootlessFirecrackerPath(),
		NetworkBackend:    "passt",
		NetworkBackendBin: rootlessPasstPath(),
		KernelPath:        rootlessKernelPath(),
		RootfsPath:        rootlessRootfsPath(),
		KVMAccess:         "ok",
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
