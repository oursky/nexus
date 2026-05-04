// Package runner implements the NXPACK bundle extraction and VM execution path.
//
// It reads a .nxbundle file, extracts its assets to a cache directory, loads
// libkrun dynamically from the extracted lib dir, and boots a microVM running
// the workspace workload.
package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
	vmnet "github.com/oursky/nexus/packages/nexus/internal/vm/net"
)

// ExtractedBundle holds the result of extracting a NXPACK bundle to disk.
type ExtractedBundle struct {
	// CacheDir is the root extraction directory, e.g. ~/.cache/nexus/bundles/<sha256>/
	CacheDir string
	// WorkspaceDir is the extracted workspace source directory.
	WorkspaceDir string
	// LibDir is the directory containing libkrun and libkrunfw.
	LibDir string
	// LayerDirs lists extracted OCI layer directories.
	LayerDirs []string
	// Meta is the parsed bundle metadata.
	Meta bundle.BundleMeta
}

// Runner handles bundle extraction and VM execution.
type Runner struct {
	// CacheDir is the base directory for extracted bundles.
	// Defaults to DefaultCacheDir() if empty.
	CacheDir string
	// ForwardPorts is a list of host ports to forward into the VM via gvproxy.
	// Only used on macOS where gvproxy provides virtio-net.
	ForwardPorts []int
}

// DefaultCacheDir returns the default bundle cache directory.
func DefaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "nexus", "bundles")
	}
	return filepath.Join(home, ".cache", "nexus", "bundles")
}

// ExtractBundle reads a NXPACK bundle file, extracts all assets to a cache
// directory. If the bundle has already been extracted (marker file present),
// extraction is skipped.
//
// The cache directory is named by the SHA256 of the bundle file path to keep
// multiple bundles isolated.
func (r *Runner) ExtractBundle(bundlePath string) (ExtractedBundle, error) {
	cacheBase := r.CacheDir
	if cacheBase == "" {
		cacheBase = DefaultCacheDir()
	}

	// Name the cache entry after a hash of the absolute bundle path.
	abs, err := filepath.Abs(bundlePath)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: resolve bundle path: %w", err)
	}
	h := sha256.Sum256([]byte(abs))
	cacheDir := filepath.Join(cacheBase, hex.EncodeToString(h[:])[:16])

	marker := filepath.Join(cacheDir, ".extracted")
	if _, err := os.Stat(marker); err == nil {
		// Already extracted — re-parse meta and return.
		return r.loadExtracted(cacheDir)
	}

	// Open bundle file.
	f, err := os.Open(bundlePath)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: open bundle: %w", err)
	}
	defer f.Close()

	// Read and parse footer.
	footer, err := bundle.ReadNXPackFooter(f)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: read footer: %w", err)
	}

	// Read assets blob.
	if _, err := f.Seek(int64(footer.AssetsOffset), io.SeekStart); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: seek to assets: %w", err)
	}
	assetsBlob := make([]byte, footer.AssetsSize)
	if _, err := io.ReadFull(f, assetsBlob); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: read assets blob: %w", err)
	}

	// Decompress assets blob (zstd-compressed tar).
	assetsTar, err := bundle.DecompressZstd(assetsBlob)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: decompress assets: %w", err)
	}

	// Create cache dir and extract.
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: create cache dir: %w", err)
	}

	meta, err := extractAssetsTar(bytes.NewReader(assetsTar), cacheDir)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: extract assets: %w", err)
	}

	metaBytes, err := bundle.MarshalMeta(meta)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: marshal meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "meta.json"), metaBytes, 0o644); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: write meta: %w", err)
	}

	// Write marker.
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: write marker: %w", err)
	}

	return buildExtractedBundle(cacheDir, meta), nil
}

// Run boots a microVM from an extracted bundle and runs cmd inside it.
// If cmd is empty the VM boots with /sbin/init (interactive/service mode).
// This function blocks until the VM exits.
func (r *Runner) Run(ctx context.Context, eb ExtractedBundle, cmd []string) error {
	// Raise file descriptor limits — libkrun requires a high fd limit.
	raiseFDLimits()

	libkrunPath, libkrunfwPath := libkrun.LibPaths(eb.LibDir)

	// Set DYLD_LIBRARY_PATH (macOS) / LD_LIBRARY_PATH (Linux) so that
	// libkrun can locate libkrunfw by its soname at runtime.
	switch runtime.GOOS {
	case "darwin":
		setEnvPath("DYLD_LIBRARY_PATH", eb.LibDir)
	default:
		setEnvPath("LD_LIBRARY_PATH", eb.LibDir)
	}

	lib, err := libkrun.Load(libkrunPath, libkrunfwPath)
	if err != nil {
		return fmt.Errorf("runner: load libkrun: %w", err)
	}
	defer lib.Close()

	// Silence libkrun output during normal operation (level 0 = none).
	// Set level 4 (debug) if NEXUS_LIBKRUN_LOG is set.
	logLevel := uint32(0)
	if os.Getenv("NEXUS_LIBKRUN_LOG") != "" {
		logLevel = 4
	}
	_ = lib.SetLogLevel(logLevel)

	vmCtx, err := lib.NewContext()
	if err != nil {
		return fmt.Errorf("runner: create VM context: %w", err)
	}
	defer vmCtx.Free()

	// Configure VM resources from meta.
	cpus := uint8(1)
	memMiB := uint32(512)
	if eb.Meta.CPUs > 0 {
		cpus = eb.Meta.CPUs
	}
	if eb.Meta.Memory > 0 {
		memMiB = eb.Meta.Memory
	}
	if err := vmCtx.SetVMConfig(cpus, memMiB); err != nil {
		return fmt.Errorf("runner: set VM config: %w", err)
	}

	rootfsDir := ""
	rootfsImage := ""
	rootfsImageExists := false

	// Use pre-merged OCI layer for the current arch.
	if len(eb.LayerDirs) == 0 {
		return fmt.Errorf("runner: bundle contains no OCI layers — cannot boot VM")
	}
	rootfsDir = eb.LayerDirs[0]

	// Ensure DNS resolver is configured inside the VM. init.krun does not create
	// /etc/resolv.conf, and without it DNS resolution fails for apt/curl/docker.
	// On macOS, gvproxy forwards DNS at 192.168.127.1. On Linux, passt forwards
	// DNS using the host's resolvers; we use 8.8.8.8 as a reliable fallback.
	dnsServer := "192.168.127.1"
	if runtime.GOOS == "linux" {
		dnsServer = "8.8.8.8"
	}
	if err := writeFile(filepath.Join(rootfsDir, "etc", "resolv.conf"), []byte("nameserver "+dnsServer+"\n"), 0o644); err != nil {
		return fmt.Errorf("runner: write resolv.conf: %w", err)
	}

	// Ensure iproute2 is present in the rootfs. The bundled libkrunfw kernel does
	// not support DHCP, so we must configure eth0 with a static IP using `ip`.
	if err := ensureNetTools(rootfsDir); err != nil {
		return fmt.Errorf("runner: ensure net tools: %w", err)
	}

	if runtime.GOOS == "linux" {
		if eb.WorkspaceDir != "" {
			if err := stageWorkspaceIntoRootfs(eb.WorkspaceDir, filepath.Join(rootfsDir, "workspace")); err != nil {
				return fmt.Errorf("runner: stage workspace: %w", err)
			}
		}
		if err := os.MkdirAll(filepath.Join(rootfsDir, "workspace"), 0o755); err != nil {
			return fmt.Errorf("runner: ensure /workspace mountpoint: %w", err)
		}
		rootfsImage = filepath.Join(eb.CacheDir, "rootfs.ext4")
		if _, err := os.Stat(rootfsImage); os.IsNotExist(err) {
			if err := buildRootFSImage(rootfsDir, rootfsImage); err != nil {
				return fmt.Errorf("runner: build rootfs image: %w", err)
			}
			rootfsImageExists = false
		} else if err != nil {
			return fmt.Errorf("runner: stat rootfs image: %w", err)
		} else {
			rootfsImageExists = true
		}
		// Sync the run script into the existing rootfs image so that each run
		// uses the current script (bake vs up vs down) rather than a stale one.
		if rootfsImageExists {
			scriptHostPath := filepath.Join(rootfsDir, "workspace", ".nexus-run.sh")
			if _, err := os.Stat(scriptHostPath); err == nil {
				if syncErr := writeFileIntoExt4(rootfsImage, scriptHostPath, "/workspace/.nexus-run.sh"); syncErr != nil {
					return fmt.Errorf("runner: sync run script into rootfs image: %w", syncErr)
				}
			}
		}
		if err := vmCtx.AddDisk("rootfs", rootfsImage, 0, false); err != nil {
			return fmt.Errorf("runner: add rootfs disk: %w", err)
		}
		if err := vmCtx.SetRootDiskRemount("/dev/vda", "ext4", "rw"); err != nil {
			return fmt.Errorf("runner: set root disk remount: %w", err)
		}
	} else {
		// Set rootfs as the VM root filesystem.
		if err := vmCtx.SetRoot(rootfsDir); err != nil {
			return fmt.Errorf("runner: set root: %w", err)
		}
		// Stage workspace files into the rootfs so they are available at
		// /workspace inside the VM. On macOS, virtiofs workspace mounts are
		// not reliably auto-mounted by init.krun, so we copy files directly.
		if eb.WorkspaceDir != "" {
			if err := stageWorkspaceIntoRootfs(eb.WorkspaceDir, filepath.Join(rootfsDir, "workspace")); err != nil {
				return fmt.Errorf("runner: stage workspace: %w", err)
			}
		}
		// Ensure /workspace exists in the rootfs so virtiofs mount and bake stamp work.
		if err := os.MkdirAll(filepath.Join(rootfsDir, "workspace"), 0o755); err != nil {
			return fmt.Errorf("runner: ensure /workspace mountpoint: %w", err)
		}
	}

	// Use a custom kernel with bridge/netfilter support if one is present.
	customKernelPath := ""
	cacheDir := eb.CacheDir
	if cacheDir == "" {
		cacheDir = DefaultCacheDir()
	}
	candidates := []string{
		filepath.Join(cacheDir, "Image-custom"),
		filepath.Join(DefaultCacheDir(), "..", "kernels", "Image-custom"),
		filepath.Join(DefaultCacheDir(), "..", "kernels", "vmlinux-custom"),
	}
	// On Linux, also check the daemon-installed kernel path.
	if runtime.GOOS == "linux" {
		home, _ := os.UserHomeDir()
		if home != "" {
			candidates = append(candidates, filepath.Join(home, ".local", "share", "nexus", "vm", "vmlinux.bin"))
		}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			customKernelPath = candidate
			break
		}
	}
	if customKernelPath != "" {
		// x86_64 uses ELF (vmlinux), arm64 uses raw Image.
		var kernelFormat uint32 = libkrun.KernelFormatRaw
		if runtime.GOARCH == "amd64" {
			kernelFormat = libkrun.KernelFormatElf
		}
		if setErr := vmCtx.SetKernel(customKernelPath, kernelFormat, "", ""); setErr != nil {
			return fmt.Errorf("runner: set custom kernel: %w", setErr)
		}
	}

	// Configure networking. On macOS, use virtio-net via gvproxy. On Linux,
	// use passt for full Ethernet support (required for Docker bridge networking).
	// Both provide NAT + port forwarding; TSI is not used because it lacks
	// Ethernet frame support needed for Docker bridge networking.
	var passtProc *vmnet.Passt
	var gvproxyProc *vmnet.GVProxy
	if runtime.GOOS == "darwin" {
		if gvpPath, err := vmnet.FindGVProxy(true); err == nil {
			sockPath := filepath.Join(eb.CacheDir, "gvproxy.sock")
			gvp, err := vmnet.StartGVProxy(gvpPath, sockPath)
			if err == nil {
				gvproxyProc = gvp
				defer func() {
					if gvproxyProc != nil {
						_ = gvproxyProc.Stop()
					}
				}()
				// libkrun will send the vfkit magic on connect (required by gvproxy).
				if err := vmCtx.AddNetUnixgram(sockPath, nil, libkrun.CompatNetFeatures, libkrun.NetFlagVFKit); err != nil {
					return fmt.Errorf("runner: add virtio-net: %w", err)
				}
				// Expose VM ports on the host via gvproxy's forwarder API.
				for _, port := range r.ForwardPorts {
					if err := gvproxyProc.ExposePort(port); err != nil {
						return fmt.Errorf("runner: gvproxy expose port %d: %w", port, err)
					}
				}
			} else {
				return fmt.Errorf("runner: gvproxy failed to start: %w", err)
			}
		} else {
			return fmt.Errorf("runner: gvproxy not found and could not be downloaded: %w", err)
		}
	} else {
		// Linux: use passt for virtio-net.
		if passtPath, err := vmnet.FindPasst(true); err == nil {
			p, err := vmnet.StartPasst(passtPath, vmnet.PasstConfig{
				GuestIP: "10.0.2.15",
				Gateway: "10.0.2.2",
				DNS:     "8.8.8.8",
				Ports:   r.ForwardPorts,
			})
			if err == nil {
				passtProc = p
				defer func() {
					if passtProc != nil {
						_ = passtProc.Stop()
					}
				}()
				if err := vmCtx.SetPasstFd(passtProc.FD()); err != nil {
					return fmt.Errorf("runner: set passt fd: %w", err)
				}
			} else {
				return fmt.Errorf("runner: passt failed to start: %w", err)
			}
		} else {
			return fmt.Errorf("runner: passt not found and could not be downloaded: %w", err)
		}
	}

	// Set working directory inside the VM.
	workdir := "/"
	if eb.WorkspaceDir != "" {
		workdir = "/workspace"
	}
	if err := vmCtx.SetWorkdir(workdir); err != nil {
		return fmt.Errorf("runner: set workdir: %w", err)
	}

	// Set the exec target BEFORE adding virtiofs mounts (libkrun requirement).
	// Write /.krun_config.json so that init.krun (bundled in libkrunfw) picks
	// up the entrypoint, env, and working directory.
	// IMPORTANT: do NOT pass os.Environ() — libkrun serialises the guest env into
	// the kernel command line which has a ~4 KiB limit.
	guestEnv := []string{
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		// TSI transports TCP sockets; force DNS over TCP so apt/curl can resolve.
		"RES_OPTIONS=use-vc",
	}
	if runtime.GOOS == "darwin" {
		// KRUN_DHCP is ignored by init.krun in the current libkrunfw build.
		// We configure eth0 with a static IP in the run script instead.
		guestEnv = append(guestEnv, "KRUN_DHCP=1")
	}

	execCmd := cmd
	if len(execCmd) == 0 {
		if len(eb.Meta.Up) > 0 {
			execCmd = []string{"/bin/sh", "-c", strings.Join(eb.Meta.Up, " && ")}
		} else {
			execCmd = []string{"/bin/sh"}
		}
	}
	if cfgErr := writeKrunConfig(rootfsDir, execCmd, guestEnv, workdir); cfgErr != nil {
		return fmt.Errorf("runner: write krun config: %w", cfgErr)
	}
	if runtime.GOOS == "linux" && rootfsImageExists {
		cfgPath := filepath.Join(rootfsDir, ".krun_config.json")
		if err := writeFileIntoExt4(rootfsImage, cfgPath, "/.krun_config.json"); err != nil {
			return fmt.Errorf("runner: sync krun config into rootfs image: %w", err)
		}
	}
	if err := vmCtx.SetExec(execCmd[0], execCmd[1:], guestEnv); err != nil {
		return fmt.Errorf("runner: set exec: %w", err)
	}

	// Add virtiofs mounts AFTER set_exec (libkrun requirement).
	if eb.WorkspaceDir != "" && runtime.GOOS != "linux" {
		if err := vmCtx.AddVirtioFS("workspace", eb.WorkspaceDir); err != nil {
			return fmt.Errorf("runner: add virtiofs workspace: %w", err)
		}
	}

	// Boot the VM — blocks until exit. Run in a goroutine so we can handle
	// signals and context cancellation gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- vmCtx.StartEnter()
	}()

	select {
	case err := <-startErrCh:
		if err != nil {
			return fmt.Errorf("runner: VM exited with error: %w", err)
		}
		return nil
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "runner: context cancelled, shutting down VM...")
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "runner: received signal %v, shutting down VM...\n", sig)
	}

	// Tear down network backends to force the VM to exit.
	if passtProc != nil {
		_ = passtProc.Stop()
	}
	if gvproxyProc != nil {
		_ = gvproxyProc.Stop()
	}

	// Wait for StartEnter to return (with a timeout so we don't hang forever).
	select {
	case err := <-startErrCh:
		if err != nil {
			return fmt.Errorf("runner: VM exited with error: %w", err)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("runner: VM did not exit within 10s of shutdown signal")
	}
}

// loadExtracted re-parses an already-extracted bundle cache directory.
func (r *Runner) loadExtracted(cacheDir string) (ExtractedBundle, error) {
	metaPath := filepath.Join(cacheDir, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: read cached meta: %w", err)
	}
	meta, err := bundle.ParseMeta(data)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: parse cached meta: %w", err)
	}
	return buildExtractedBundle(cacheDir, meta), nil
}

// buildExtractedBundle constructs an ExtractedBundle from a cache dir and meta.
func buildExtractedBundle(cacheDir string, meta bundle.BundleMeta) ExtractedBundle {
	libDir := filepath.Join(cacheDir, "lib")
	platformKey := runtime.GOOS + "-" + runtime.GOARCH
	platformDir := filepath.Join(libDir, platformKey)
	if info, err := os.Stat(platformDir); err == nil && info.IsDir() {
		libDir = platformDir
	}

	eb := ExtractedBundle{
		CacheDir:     cacheDir,
		WorkspaceDir: filepath.Join(cacheDir, "workspace"),
		LibDir:       libDir,
		Meta:         meta,
	}

	// Pre-merged layer for current arch (extracted from inner tar).
	currentArch := runtime.GOARCH
	layerDir := filepath.Join(cacheDir, "layers", currentArch)
	if info, err := os.Stat(layerDir); err == nil && info.IsDir() {
		eb.LayerDirs = append(eb.LayerDirs, layerDir)
	}

	return eb
}

// extractAssetsTar unpacks the assets tar into destDir using the rigid directory
// structure. It returns the parsed BundleMeta.
func extractAssetsTar(r io.Reader, destDir string) (bundle.BundleMeta, error) {
	var meta bundle.BundleMeta
	foundMeta := false

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return bundle.BundleMeta{}, fmt.Errorf("reading assets tar: %w", err)
		}

		// Skip directories; we create them on demand.
		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		dest, extractInner, err := resolveDestPath(hdr.Name, destDir)
		if err != nil {
			// Unrecognised path — skip.
			continue
		}

		if hdr.Name == "meta.json" {
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return bundle.BundleMeta{}, fmt.Errorf("read meta.json: %w", readErr)
			}
			meta, err = bundle.ParseMeta(data)
			if err != nil {
				return bundle.BundleMeta{}, fmt.Errorf("parse meta.json: %w", err)
			}
			foundMeta = true
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				return bundle.BundleMeta{}, fmt.Errorf("write meta.json: %w", err)
			}
			continue
		}

		if extractInner {
			// The entry itself is a tar archive — extract its contents into dest.
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return bundle.BundleMeta{}, fmt.Errorf("read inner tar %s: %w", hdr.Name, readErr)
			}
			if mkErr := os.MkdirAll(dest, 0o755); mkErr != nil {
				return bundle.BundleMeta{}, fmt.Errorf("mkdir %s: %w", dest, mkErr)
			}
			var innerReader io.Reader = bytes.NewReader(data)
			// Decompress gzip-wrapped tars (e.g. workspace.tar.gz).
			if strings.HasSuffix(hdr.Name, ".tar.gz") {
				gr, gzErr := gzip.NewReader(bytes.NewReader(data))
				if gzErr != nil {
					return bundle.BundleMeta{}, fmt.Errorf("gzip open inner tar %s: %w", hdr.Name, gzErr)
				}
				defer gr.Close()
				innerReader = gr
			}
			if exErr := extractInnerTar(innerReader, dest); exErr != nil {
				return bundle.BundleMeta{}, fmt.Errorf("extract inner tar %s: %w", hdr.Name, exErr)
			}
			continue
		}

		// Plain file — write to dest.
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return bundle.BundleMeta{}, fmt.Errorf("mkdir for %s: %w", dest, mkErr)
		}
		//nolint:gosec // bundle files are trusted
		outF, createErr := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0o600)
		if createErr != nil {
			return bundle.BundleMeta{}, fmt.Errorf("create %s: %w", dest, createErr)
		}
		if _, cpErr := io.Copy(outF, tr); cpErr != nil {
			outF.Close()
			return bundle.BundleMeta{}, fmt.Errorf("write %s: %w", dest, cpErr)
		}
		outF.Close()
	}

	if !foundMeta {
		return bundle.BundleMeta{}, fmt.Errorf("meta.json not found in assets tar")
	}

	return meta, nil
}

// resolveDestPath maps a tar entry name to a destination path.
// extractInner=true means the entry is itself a tar that should be extracted into dest.
func resolveDestPath(name, destDir string) (dest string, extractInner bool, err error) {
	switch {
	case name == "meta.json":
		return filepath.Join(destDir, "meta.json"), false, nil

	case name == "workspace.tar.gz":
		return filepath.Join(destDir, "workspace"), true, nil

	case strings.HasPrefix(name, "layers/") && strings.HasSuffix(name, ".tar"):
		base := filepath.Base(name)
		layerName := strings.TrimSuffix(base, ".tar")
		return filepath.Join(destDir, "layers", layerName), true, nil

	case strings.HasPrefix(name, "lib/"):
		return filepath.Join(destDir, name), false, nil

	default:
		return "", false, fmt.Errorf("unrecognised entry: %s", name)
	}
}

// extractInnerTar unpacks a tar reader into destDir, creating files as needed.
func extractInnerTar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, filepath.Clean("/"+hdr.Name))
		// Guard against path traversal.
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) && target != destDir {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
				return mkErr
			}
		case tar.TypeReg, tar.TypeRegA:
			if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
				return mkErr
			}
			//nolint:gosec
			f, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0o600)
			if createErr != nil {
				return createErr
			}
			if _, cpErr := io.Copy(f, tr); cpErr != nil {
				f.Close()
				return cpErr
			}
			f.Close()
		case tar.TypeSymlink:
			// Best effort — ignore errors (e.g. already exists).
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
	return nil
}

// raiseFDLimits raises the process file descriptor limit to the system maximum.
// libkrun opens many file descriptors internally and requires a high limit.
func raiseFDLimits() {
	// Use syscall-level rlimit to avoid importing "syscall" cross-platform.
	// This is a best-effort operation; failure is silently ignored.
	_ = raiseRlimitNofile()
}

// setEnvPath prepends dir to the colon-separated env variable named key,
// creating it if absent. This is used to set DYLD_LIBRARY_PATH / LD_LIBRARY_PATH
// so that libkrun can locate libkrunfw by its soname at runtime.
func setEnvPath(key, dir string) {
	existing := os.Getenv(key)
	if existing == "" {
		_ = os.Setenv(key, dir)
	} else {
		_ = os.Setenv(key, dir+":"+existing)
	}
}

// mergeOCILayers applies OCI layers in order onto destDir, creating a merged
// rootfs suitable for krun_set_root. Each layer is a plain tar archive extracted
// on top of the previous one (later layers overwrite earlier files, whiteout
// entries delete files per OCI spec).
func mergeOCILayers(layerDirs []string, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mergeOCILayers: mkdir dest: %w", err)
	}
	for _, layerDir := range layerDirs {
		// Each layerDir was extracted from a layer tar; re-walk it and copy
		// entries into destDir, applying whiteout semantics.
		if err := applyLayerDir(layerDir, destDir); err != nil {
			return fmt.Errorf("mergeOCILayers: apply layer %s: %w", layerDir, err)
		}
	}
	return nil
}

// applyLayerDir copies the contents of srcDir into destDir, applying OCI
// whiteout semantics:
//   - A file named ".wh.<name>" deletes <name> in destDir.
//   - A file named ".wh..wh..opq" marks an opaque whiteout: delete all of destDir/<parent>.
func applyLayerDir(srcDir, destDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		base := filepath.Base(rel)
		destPath := filepath.Join(destDir, rel)

		// Opaque whiteout: delete entire parent directory contents.
		if base == ".wh..wh..opq" {
			parent := filepath.Dir(destPath)
			entries, rdErr := os.ReadDir(parent)
			if rdErr == nil {
				for _, e := range entries {
					_ = os.RemoveAll(filepath.Join(parent, e.Name()))
				}
			}
			return nil
		}

		// Regular whiteout: delete the named file/dir.
		if strings.HasPrefix(base, ".wh.") {
			target := filepath.Join(filepath.Dir(destPath), strings.TrimPrefix(base, ".wh."))
			_ = os.RemoveAll(target)
			return nil
		}

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		// Symlink.
		if info.Mode()&os.ModeSymlink != 0 {
			link, lErr := os.Readlink(path)
			if lErr != nil {
				return lErr
			}
			_ = os.Remove(destPath)
			return os.Symlink(link, destPath)
		}

		// Regular file: copy.
		return copyFile(path, destPath, info.Mode())
	})
}

// copyFile copies src to dst with the given mode.
func copyFile(src, dst string, mode os.FileMode) error {
	if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
		return mkErr
	}
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer in.Close()
	//nolint:gosec
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// krunConfig matches the /.krun_config.json schema read by init.krun.
type krunConfig struct {
	Cmd        []string          `json:"Cmd"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
}

// writeKrunConfig writes /.krun_config.json into rootfsDir so that init.krun
// (bundled in libkrunfw) picks up the entrypoint, env, and working directory.
func writeKrunConfig(rootfsDir string, cmd, env []string, workdir string) error {
	cfg := krunConfig{
		Cmd:        cmd,
		Env:        env,
		WorkingDir: workdir,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("writeKrunConfig: marshal: %w", err)
	}
	cfgPath := filepath.Join(rootfsDir, ".krun_config.json")
	return os.WriteFile(cfgPath, data, 0o644)
}

func buildRootFSImage(rootfsDir, outPath string) error {
	bytesUsed, err := dirSize(rootfsDir)
	if err != nil {
		return err
	}
	minSize := int64(2 * 1024 * 1024 * 1024)
	if raw := strings.TrimSpace(os.Getenv("NEXUS_RUNNER_ROOTFS_MIN_MIB")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			minSize = v * 1024 * 1024
		}
	}
	const overhead = int64(512 * 1024 * 1024)
	imageSize := bytesUsed + overhead
	if imageSize < minSize {
		imageSize = minSize
	}

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create image: %w", err)
	}
	if err := f.Truncate(imageSize); err != nil {
		f.Close()
		return fmt.Errorf("truncate image: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close image: %w", err)
	}

	cmd := exec.Command("mkfs.ext4", "-F", "-d", rootfsDir, outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 rootfs image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("compute dir size: %w", err)
	}
	return total, nil
}

func stageWorkspaceIntoRootfs(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dstDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func writeFileIntoExt4(imagePath, srcPath, dstPath string) error {
	_ = exec.Command("debugfs", "-w", "-R", "rm "+dstPath, imagePath).Run()
	cmd := exec.Command("debugfs", "-w", "-R", "write "+srcPath+" "+dstPath, imagePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs write %s: %w: %s", dstPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureNetTools checks that the rootfs has the `ip` binary required for
// virtio-net static IP configuration. If missing, it downloads iproute2 and
// its dependencies from the Ubuntu repository and extracts them into the
// rootfs. This is a workaround because the bundled libkrunfw kernel does not
// support DHCP (NET_FLAG_DHCPClient returns EINVAL).
func ensureNetTools(rootfsDir string) error {
	if _, err := os.Stat(filepath.Join(rootfsDir, "bin", "ip")); err == nil {
		return nil // already present
	}

	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}

	cacheDir := filepath.Join(DefaultCacheDir(), "..", "bundle-layers", "iproute2-deb", arch)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("ensureNetTools: mkdir cache: %w", err)
	}

	mirror := "http://archive.ubuntu.com/ubuntu"
	if arch == "arm64" {
		mirror = "http://ports.ubuntu.com/ubuntu-ports"
	}

	packages := netToolPackages(arch)

	for _, pkg := range packages {
		fname := filepath.Join(cacheDir, pkg.name+".deb")
		if _, err := os.Stat(fname); err != nil {
			url := mirror + "/" + pkg.path
			if err := downloadFile(url, fname); err != nil {
				return fmt.Errorf("ensureNetTools: download %s: %w", pkg.name, err)
			}
		}
		extractDir := filepath.Join(cacheDir, "extract_"+pkg.name)
		if err := os.MkdirAll(extractDir, 0o755); err != nil {
			return err
		}
		if err := extractDeb(fname, extractDir); err != nil {
			return fmt.Errorf("ensureNetTools: extract %s: %w", pkg.name, err)
		}
		if err := copyExtractedFiles(extractDir, rootfsDir); err != nil {
			return fmt.Errorf("ensureNetTools: copy %s: %w", pkg.name, err)
		}
	}
	return nil
}

func netToolPackages(arch string) []struct {
	name string
	path string
} {
	switch arch {
	case "amd64":
		return []struct {
			name string
			path string
		}{
			{"iproute2", "pool/main/i/iproute2/iproute2_6.1.0-1ubuntu6_amd64.deb"},
			{"libbpf1", "pool/main/libb/libbpf/libbpf1_1.3.0-2build2_amd64.deb"},
			{"libmnl0", "pool/main/libm/libmnl/libmnl0_1.0.5-2build1_amd64.deb"},
			{"libelf1t64", "pool/main/e/elfutils/libelf1t64_0.190-1.1build4_amd64.deb"},
		}
	default:
		return []struct {
			name string
			path string
		}{
			{"iproute2", "pool/main/i/iproute2/iproute2_6.1.0-1ubuntu6_arm64.deb"},
			{"libbpf1", "pool/main/libb/libbpf/libbpf1_1.3.0-2build2_arm64.deb"},
			{"libmnl0", "pool/main/libm/libmnl/libmnl0_1.0.5-2build1_arm64.deb"},
			{"libelf1t64", "pool/main/e/elfutils/libelf1t64_0.190-1.1build4_arm64.deb"},
		}
	}
}

func downloadFile(url, outPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func extractDeb(debPath, destDir string) error {
	// .deb is an ar archive containing data.tar.zst.
	arPath, err := exec.LookPath("ar")
	if err != nil {
		return fmt.Errorf("ar not found: %w", err)
	}
	cmd := exec.Command(arPath, "-x", debPath)
	cmd.Dir = destDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ar extract: %w", err)
	}
	dataTarZst := filepath.Join(destDir, "data.tar.zst")
	if _, err := os.Stat(dataTarZst); err != nil {
		return fmt.Errorf("data.tar.zst not found in %s", debPath)
	}
	zstdPath, err := exec.LookPath("zstd")
	if err != nil {
		return fmt.Errorf("zstd not found: %w", err)
	}
	dataTar := filepath.Join(destDir, "data.tar")
	cmd = exec.Command(zstdPath, "-d", dataTarZst, "-o", dataTar)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zstd decompress: %w", err)
	}
	cmd = exec.Command("tar", "-xf", dataTar, "-C", destDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar extract: %w", err)
	}
	_ = os.Remove(dataTar)
	return nil
}

func copyExtractedFiles(srcDir, rootfsDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(rootfsDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		// Preserve symlinks (e.g. sbin/ip -> /bin/ip in deb packages).
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			_ = os.Remove(dst)
			return os.Symlink(linkTarget, dst)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
