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
	"path/filepath"
	"runtime"
	"strings"

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
	// LayerDirs lists extracted OCI layer directories in manifest order.
	LayerDirs []string
	// Manifest is the parsed bundle manifest.
	Manifest bundle.BundleManifest
}

// Runner handles bundle extraction and VM execution.
type Runner struct {
	// CacheDir is the base directory for extracted bundles.
	// Defaults to DefaultCacheDir() if empty.
	CacheDir string
}

// DefaultCacheDir returns the default bundle cache directory.
func DefaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "nexus", "bundles")
	}
	return filepath.Join(home, ".cache", "nexus", "bundles")
}

// ExtractBundle reads a NXPACK bundle file, verifies its integrity, and extracts
// all assets to a cache directory. If the bundle has already been extracted
// (marker file present), extraction is skipped.
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
		// Already extracted — re-parse manifest and return.
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

	// Read manifest JSON.
	if _, err := f.Seek(int64(footer.ManifestOffset), io.SeekStart); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: seek to manifest: %w", err)
	}
	manifestJSON := make([]byte, footer.ManifestSize)
	if _, err := io.ReadFull(f, manifestJSON); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: read manifest: %w", err)
	}

	// Parse manifest.
	manifest, err := bundle.ParseManifest(manifestJSON)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: parse manifest: %w", err)
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
	if err := extractTar(bytes.NewReader(assetsTar), cacheDir, manifest); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: extract assets: %w", err)
	}

	manifestData, err := bundle.MarshalManifest(manifest)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "manifest.json"), manifestData, 0o644); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: write manifest: %w", err)
	}

	// Write marker.
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: write marker: %w", err)
	}

	return buildExtractedBundle(cacheDir, manifest), nil
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

	// Configure VM resources from manifest.
	cpus := uint8(1)
	memMiB := uint32(512)
	if eb.Manifest.Runtime != nil {
		if eb.Manifest.Runtime.CPUs > 0 {
			cpus = eb.Manifest.Runtime.CPUs
		}
		if eb.Manifest.Runtime.MemMiB > 0 {
			memMiB = eb.Manifest.Runtime.MemMiB
		}
	}
	if err := vmCtx.SetVMConfig(cpus, memMiB); err != nil {
		return fmt.Errorf("runner: set VM config: %w", err)
	}

	// Merge OCI layers into a single rootfs directory and use it as the VM root.
	// init.krun (bundled in libkrunfw) reads /.krun_config.json for the entrypoint.
	if len(eb.LayerDirs) == 0 {
		return fmt.Errorf("runner: bundle contains no OCI layers — cannot boot VM")
	}
	mergedDir := filepath.Join(eb.CacheDir, "merged-rootfs")
	if mergeErr := mergeOCILayers(eb.LayerDirs, mergedDir); mergeErr != nil {
		return fmt.Errorf("runner: merge OCI layers: %w", mergeErr)
	}
	rootfsDir := mergedDir
	rootfsImage := ""
	rootfsImageExists := false

	// Ensure DNS resolver is configured inside the VM. init.krun does not create
	// /etc/resolv.conf, and without it DNS resolution fails for apt/curl/docker.
	if err := writeFile(filepath.Join(rootfsDir, "etc", "resolv.conf"), []byte("nameserver 8.8.8.8\nnameserver 1.1.1.1\noptions use-vc\n"), 0o644); err != nil {
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
	// Check the bundle cache first, then fall back to the global kernel dir.
	customKernelPath := ""
	bundleKernel := filepath.Join(eb.CacheDir, "Image-custom")
	globalKernel := filepath.Join(DefaultCacheDir(), "..", "kernels", "Image-custom")
	if _, err := os.Stat(bundleKernel); err == nil {
		customKernelPath = bundleKernel
	} else if _, err := os.Stat(globalKernel); err == nil {
		customKernelPath = globalKernel
	}
	if customKernelPath != "" {
		if setErr := vmCtx.SetKernel(customKernelPath, libkrun.KernelFormatRaw, "", ""); setErr != nil {
			return fmt.Errorf("runner: set custom kernel: %w", setErr)
		}
	}

	// Configure networking. On macOS, use virtio-net via gvproxy for full
	// Ethernet support (required for Docker bridge networking). On Linux or
	// if gvproxy is unavailable, fall back to TSI ( Transparent Socket
	// Impersonation ) which only supports TCP/UDP sockets.
	var gvproxy *vmnet.GVProxy
	if runtime.GOOS == "darwin" {
		if gvpPath, err := vmnet.FindGVProxy(true); err == nil {
			sockPath := filepath.Join(eb.CacheDir, "gvproxy.sock")
			gvp, err := vmnet.StartGVProxy(gvpPath, sockPath)
			if err == nil {
				gvproxy = gvp
				defer func() {
					if gvproxy != nil {
						_ = gvproxy.Stop()
					}
				}()
				// libkrun will send the vfkit magic on connect (required by gvproxy).
				if err := vmCtx.AddNetUnixgram(sockPath, nil, libkrun.CompatNetFeatures, libkrun.NetFlagVFKit); err != nil {
					return fmt.Errorf("runner: add virtio-net: %w", err)
				}
			} else {
				return fmt.Errorf("runner: gvproxy failed to start: %w", err)
			}
		} else {
			return fmt.Errorf("runner: gvproxy not found and could not be downloaded: %w", err)
		}
	}
	if gvproxy == nil {
		// Linux: use TSI (Transparent Socket Impersonation) as fallback.
		const tsiFeatureHijackINET = 1 << 0
		if err := vmCtx.DisableImplicitVsock(); err != nil {
			return fmt.Errorf("runner: disable implicit vsock: %w", err)
		}
		if err := vmCtx.AddVsock(tsiFeatureHijackINET); err != nil {
			return fmt.Errorf("runner: enable TSI vsock features: %w", err)
		}
		if err := vmCtx.SetPortMap(nil); err != nil {
			return fmt.Errorf("runner: set TSI port map: %w", err)
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
		if len(eb.Manifest.WorkspaceIntent.Up) > 0 {
			execCmd = []string{"/bin/sh", "-c", strings.Join(eb.Manifest.WorkspaceIntent.Up, " && ")}
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

	// Boot the VM — blocks until exit.
	if err := vmCtx.StartEnter(); err != nil {
		return fmt.Errorf("runner: VM exited with error: %w", err)
	}
	return nil
}

// loadExtracted re-parses an already-extracted bundle cache directory.
func (r *Runner) loadExtracted(cacheDir string) (ExtractedBundle, error) {
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: read cached manifest: %w", err)
	}
	manifest, err := bundle.ParseManifest(data)
	if err != nil {
		return ExtractedBundle{}, fmt.Errorf("runner: parse cached manifest: %w", err)
	}
	return buildExtractedBundle(cacheDir, manifest), nil
}

// buildExtractedBundle constructs an ExtractedBundle from a cache dir and manifest.
func buildExtractedBundle(cacheDir string, manifest bundle.BundleManifest) ExtractedBundle {
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
		Manifest:     manifest,
	}

	// Collect layer dirs from manifest in order.
	if manifest.Assets != nil {
		for _, layer := range manifest.Assets.Layers {
			// layer.Path is like "payload/layers/abc123def456.tar"
			// Extract the hex prefix as the layer dir name.
			base := filepath.Base(layer.Path)
			name := strings.TrimSuffix(base, ".tar")
			eb.LayerDirs = append(eb.LayerDirs, filepath.Join(cacheDir, "layers", name))
		}
	}
	return eb
}

// extractTar unpacks the assets tar into destDir, routing entries to their
// correct subdirectories based on path prefix.
//
// Routing rules:
//   - manifest.json            → destDir/manifest.json
//   - payload/workspace.tar.gz → extracted into destDir/workspace/
//   - payload/layers/<hex>.tar → extracted into destDir/layers/<hex>/
//   - lib/*                    → destDir/lib/<filename>
func extractTar(r io.Reader, destDir string, _ bundle.BundleManifest) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading assets tar: %w", err)
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

		if extractInner {
			// The entry itself is a tar archive — extract its contents into dest.
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return fmt.Errorf("read inner tar %s: %w", hdr.Name, readErr)
			}
			if mkErr := os.MkdirAll(dest, 0o755); mkErr != nil {
				return fmt.Errorf("mkdir %s: %w", dest, mkErr)
			}
			var innerReader io.Reader = bytes.NewReader(data)
			// Decompress gzip-wrapped tars (e.g. workspace.tar.gz).
			if strings.HasSuffix(hdr.Name, ".tar.gz") {
				gr, gzErr := gzip.NewReader(bytes.NewReader(data))
				if gzErr != nil {
					return fmt.Errorf("gzip open inner tar %s: %w", hdr.Name, gzErr)
				}
				defer gr.Close()
				innerReader = gr
			}
			if exErr := extractInnerTar(innerReader, dest); exErr != nil {
				return fmt.Errorf("extract inner tar %s: %w", hdr.Name, exErr)
			}
			continue
		}

		// Plain file — write to dest.
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return fmt.Errorf("mkdir for %s: %w", dest, mkErr)
		}
		//nolint:gosec // bundle files are trusted (integrity verified before extraction)
		outF, createErr := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0o600)
		if createErr != nil {
			return fmt.Errorf("create %s: %w", dest, createErr)
		}
		if _, cpErr := io.Copy(outF, tr); cpErr != nil {
			outF.Close()
			return fmt.Errorf("write %s: %w", dest, cpErr)
		}
		outF.Close()
	}
	return nil
}

// resolveDestPath maps a tar entry name to a destination path.
// extractInner=true means the entry is itself a tar that should be extracted into dest.
func resolveDestPath(name, destDir string) (dest string, extractInner bool, err error) {
	switch {
	case name == "manifest.json":
		return filepath.Join(destDir, "manifest.json"), false, nil

	case name == "payload/workspace.tar.gz":
		return filepath.Join(destDir, "workspace"), true, nil

	case strings.HasPrefix(name, "payload/layers/") && strings.HasSuffix(name, ".tar"):
		// e.g. "payload/layers/abc123def456.tar" → layers/abc123def456/
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
	const minSize = int64(2 * 1024 * 1024 * 1024)
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

	// Cache directory for downloaded .deb files.
	cacheDir := filepath.Join(DefaultCacheDir(), "..", "bundle-layers", "iproute2-deb")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("ensureNetTools: mkdir cache: %w", err)
	}

	// Ubuntu 24.04 (noble) arm64 packages required for `ip`.
	packages := []struct {
		name string
		path string
	}{
		{"iproute2", "pool/main/i/iproute2/iproute2_1.8.10-3ubuntu2_arm64.deb"},
		{"libbpf1", "pool/main/libb/libbpf/libbpf1_1.3.0-2build2_arm64.deb"},
		{"libmnl0", "pool/main/libm/libmnl/libmnl0_1.0.5-2build1_arm64.deb"},
		{"libelf1t64", "pool/main/e/elfutils/libelf1t64_0.190-1.1build4_arm64.deb"},
	}

	for _, pkg := range packages {
		fname := filepath.Join(cacheDir, pkg.name+".deb")
		if _, err := os.Stat(fname); err != nil {
			url := "http://ports.ubuntu.com/ubuntu-ports/" + pkg.path
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
		// Skip deb metadata and docs to keep the rootfs small.
		if strings.HasPrefix(rel, "DEBIAN/") || strings.HasPrefix(rel, "usr/share/doc/") || strings.HasPrefix(rel, "usr/share/man/") {
			return nil
		}
		dst := filepath.Join(rootfsDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(dst)
			return os.Symlink(linkTarget, dst)
		}
		return copyFile(path, dst, info.Mode())
	})
}
