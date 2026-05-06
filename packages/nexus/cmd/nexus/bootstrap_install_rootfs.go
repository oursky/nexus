//go:build linux

//nolint:unused
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
	lkruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
)

// ── VM rootfs installation ────────────────────────────────────────────────────

func installVMRootfsRootless(w io.Writer, emitJSON bool) error {
	dest := rootlessRootfsPath()
	if _, err := os.Stat(dest); err == nil {
		return nil // already present (agent injection handled separately)
	}

	if err := buildRootlessRootfs(w, dest); err != nil {
		return err
	}
	// A freshly-built rootfs does NOT have the agent injected yet.
	// Remove the cached agent hash so ensureGuestAgent knows to
	// re-inject rather than skip because the old hash still matches.
	_ = os.Remove(filepath.Join(xdgStateNexus(), "rootfs-agent.sha256"))
	// Also remove the current bake stamp so the next daemon start re-bakes the
	// rootfs with developer tools baked into the fresh image.
	lkruntime.DeleteBakeStamp(xdgStateNexus())
	return nil
}

// installRootfsDirForLibkrun pulls the Ubuntu base image from the OCI registry
// and extracts it directly to rootfs-dir/ — the only rootfs artifact libkrun needs.
// It deliberately skips building rootfs.ext4, saving 5–10 min of ext4 work.
//
// Image source: ubuntu:26.04 (Docker Hub, multi-arch). OCI layers are cached
// at ~/.cache/nexus/bundle-layers/ so subsequent installs work offline.
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

	imageRef := bundle.DefaultBaseImage
	layerCacheDir := defaultOCILayerCacheDir()
	fmt.Fprintf(w, "  pulling OCI image %s ...\n", imageRef)
	emitPhase(w, emitJSON, "asset-install", "start", "pulling OCI image "+imageRef)

	ctx := context.Background()
	if err := bundle.ExtractImageToDir(ctx, imageRef, extractDir, layerCacheDir); err != nil {
		return fmt.Errorf("extract OCI image: %w", err)
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
		// /tmp: ensure world-writable sticky bit — the extracted rootfs may default to 0755.
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

	// Create the apt cache directories that are absent in the minimal rootfs archive.
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
