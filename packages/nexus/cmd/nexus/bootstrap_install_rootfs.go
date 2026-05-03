//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
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

// installRootfsDirForLibkrun pulls the Ubuntu base image from the OCI registry
// and extracts it directly to rootfs-dir/ — the only rootfs artifact libkrun needs.
// It deliberately skips building rootfs.ext4, saving 5–10 min of ext4 work.
//
// Image source: ubuntu:26.04 (Docker Hub, multi-arch).  OCI layers are cached
// at ~/.cache/nexus/bundle-layers/ so subsequent installs work offline.
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

	extractDir := filepath.Join(tmpDir, "rootfs")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("mkdir extract dir: %w", err)
	}

	// ── Legacy path: cached squashfs from old installs ────────────────────────
	squashfsCache := filepath.Join(rootlessVMDir(), "ubuntu.squashfs")
	if _, err := os.Stat(squashfsCache); err == nil && vmRootfsIsSquashfs(squashfsCache) {
		fmt.Fprintf(w, "  using cached squashfs (legacy) from %s\n", squashfsCache)

		fakerootDBPath := filepath.Join(tmpDir, "fakeroot.db")
		hasFakeroot := func() bool { _, err := exec.LookPath("fakeroot"); return err == nil }()
		runCmd := func(name string, args ...string) *exec.Cmd {
			if hasFakeroot {
				allArgs := append([]string{"-s", fakerootDBPath, "-i", fakerootDBPath, name}, args...)
				return exec.Command("fakeroot", allArgs...)
			}
			return exec.Command(name, args...)
		}

		if _, err := exec.LookPath("unsquashfs"); err != nil {
			return fmt.Errorf("unsquashfs not found — install squashfs-tools")
		}
		unsqCmd := runCmd("unsquashfs", "-d", extractDir, squashfsCache)
		unsqCmd.Stdout = w
		unsqCmd.Stderr = w
		if err := unsqCmd.Run(); err != nil {
			return fmt.Errorf("unsquashfs: %w", err)
		}
	} else {
		// ── OCI path: pull ubuntu:26.04 from Docker Hub ───────────────────────
		imageRef := bundle.DefaultBaseImage
		layerCacheDir := defaultOCILayerCacheDir()
		fmt.Fprintf(w, "  pulling OCI image %s ...\n", imageRef)
		emitPhase(w, emitJSON, "asset-install", "start", "pulling OCI image "+imageRef)

		ctx := context.Background()
		if err := bundle.ExtractImageToDir(ctx, imageRef, extractDir, layerCacheDir); err != nil {
			return fmt.Errorf("extract OCI image: %w", err)
		}
	}

	// ── Patch and install to permanent location ───────────────────────────────
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

	// ── Inject the nexus agent into the rootfs directory ─────────────────────
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

// defaultOCILayerCacheDir returns the directory used to cache OCI layer tars.
func defaultOCILayerCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "bundle-layers")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "nexus", "bundle-layers")
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
