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
