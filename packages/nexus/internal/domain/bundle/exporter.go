package bundle

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
)

// Exporter creates .nxbundle archives from a running workspace.
type Exporter struct{}

// NewExporter constructs an Exporter.
func NewExporter() *Exporter {
	return &Exporter{}
}

// Export looks up the named workspace via the daemon and writes a self-executing
// NXPACK .nxbundle to outPath. The caller is responsible for choosing the final
// path (e.g. appending ".nxbundle" when appropriate). The bundle is chmod 0755
// so it can be executed directly. Returns the output path and any error.
func (e *Exporter) Export(ctx context.Context, workspaceName, outPath string) (string, error) {
	// Resolve workspace via daemon RPC.
	conn, connErr := rpc.EnsureDaemon()
	if connErr != nil {
		return "", fmt.Errorf("bundle: connect to daemon: %w", connErr)
	}
	defer conn.Close()

	wsID, wsIDErr := rpc.ResolveWorkspaceID(ctx, conn, workspaceName)
	if wsIDErr != nil {
		return "", fmt.Errorf("bundle: resolve workspace: %w", wsIDErr)
	}

	// Attempt to populate WorkspaceIntent (including VM resources) from the daemon-side Nexusfile.
	var intentResult struct {
		WorkspaceIntent WorkspaceIntent `json:"workspaceIntent"`
	}
	var workspaceIntent WorkspaceIntent
	var vmCPUs uint8 = 2
	var vmMemMiB uint32 = 2048
	if intentErr := rpc.Do(conn, "workspace.nexusfile", map[string]any{"id": wsID}, &intentResult); intentErr == nil {
		workspaceIntent = intentResult.WorkspaceIntent
		if intentResult.WorkspaceIntent.CPUs > 0 {
			vmCPUs = intentResult.WorkspaceIntent.CPUs
		}
		if intentResult.WorkspaceIntent.MemMiB > 0 {
			vmMemMiB = intentResult.WorkspaceIntent.MemMiB
		}
	}

	// Fetch workspace archive (tar.gz of repo dir, excluding host-specific files).
	var archiveBytes []byte
	var archiveResult struct {
		ArchiveB64 string `json:"archiveB64"`
	}
	if archErr := rpc.Do(conn, "workspace.archive", map[string]any{"id": wsID}, &archiveResult); archErr != nil {
		// Non-fatal: bundle proceeds without workspace snapshot.
		fmt.Fprintf(os.Stderr, "warning: workspace.archive failed: %v\n", archErr)
	} else if archiveResult.ArchiveB64 != "" {
		decoded, decErr := base64.StdEncoding.DecodeString(archiveResult.ArchiveB64)
		if decErr == nil {
			archiveBytes = decoded
		}
	}

	// Fetch OCI layers for the base VM rootfs.
	imageRef := DefaultBaseImage

	arm64Layers, arm64Err := FetchLayersCachedForArch(ctx, imageRef, "arm64")
	if arm64Err != nil {
		return "", fmt.Errorf("bundle: fetch OCI layers for %q (arm64): %w", imageRef, arm64Err)
	}
	amd64Layers, amd64Err := FetchLayersCachedForArch(ctx, imageRef, "amd64")
	if amd64Err != nil {
		return "", fmt.Errorf("bundle: fetch OCI layers for %q (amd64): %w", imageRef, amd64Err)
	}

	// Pre-merge OCI layers per architecture into a single tar.
	mergedArm64, mergeArm64Err := mergeOCILayersToTar(arm64Layers)
	if mergeArm64Err != nil {
		return "", fmt.Errorf("bundle: merge arm64 layers: %w", mergeArm64Err)
	}
	mergedAmd64, mergeAmd64Err := mergeOCILayersToTar(amd64Layers)
	if mergeAmd64Err != nil {
		return "", fmt.Errorf("bundle: merge amd64 layers: %w", mergeAmd64Err)
	}

	mergedLayers := map[string][]OCILayer{
		"arm64": {{Digest: imageRef + ":merged", Data: mergedArm64}},
		"amd64": {{Digest: imageRef + ":merged", Data: mergedAmd64}},
	}

	allPlatformLibs, allErr := resolveAllPlatformLibkrunAssets(ctx)
	if allErr != nil {
		return "", allErr
	}

	meta := BundleMeta{
		Arch:    []string{"arm64", "amd64"},
		Bake:    workspaceIntent.Bake,
		Up:      workspaceIntent.Up,
		CPUs:    vmCPUs,
		Memory:  vmMemMiB,
		Workdir: "/workspace",
	}

	assetsTar, buildErr := buildAssetsTar(archiveBytes, mergedLayers, allPlatformLibs, meta)
	if buildErr != nil {
		return "", fmt.Errorf("bundle: build assets tar: %w", buildErr)
	}

	// Compress the assets tar with zstd.
	assetsBlob, compErr := CompressZstd(assetsTar)
	if compErr != nil {
		return "", fmt.Errorf("bundle: compress assets: %w", compErr)
	}

	// Write the NXPACK bundle.
	if writeErr := WriteNXPackBundle(outPath, assetsBlob); writeErr != nil {
		return "", writeErr
	}

	return outPath, nil
}

// WriteNXPackBundle writes a self-executing NXPACK bundle to dst.
// The file is created (or truncated) and marked executable (0755).
func WriteNXPackBundle(dst string, assetsBlob []byte) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bundle: create output file: %w", err)
	}
	defer f.Close()

	darwinBin, linuxBin, binErr := readCrossPlatformBinaries()
	if binErr != nil {
		return fmt.Errorf("bundle: read nexus binaries: %w", binErr)
	}

	stub := nxFatShellStub(len(darwinBin), len(linuxBin))

	preamble := make([]byte, 0, len(stub)+len(darwinBin)+len(linuxBin))
	preamble = append(preamble, stub...)
	preamble = append(preamble, darwinBin...)
	preamble = append(preamble, linuxBin...)

	if err := WriteNXPack(f, assetsBlob, preamble); err != nil {
		return err
	}

	if err := f.Chmod(0o755); err != nil { //nolint:gosec
		return fmt.Errorf("bundle: chmod bundle: %w", err)
	}
	return nil
}

// WriteNXPackBundleWithBinary writes a self-executing NXPACK bundle embedding
// the provided nexus binary bytes instead of reading os.Executable().
// Use this when you need to embed a specific binary (e.g. in tests or tooling).
func WriteNXPackBundleWithBinary(dst string, assetsBlob, nexusBin []byte) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bundle: create output file: %w", err)
	}
	defer f.Close()

	stub := nxpackShellStub(len(nexusBin))
	preamble := append(stub, nexusBin...) //nolint:gocritic

	if err := WriteNXPack(f, assetsBlob, preamble); err != nil {
		return err
	}
	if err := f.Chmod(0o755); err != nil { //nolint:gosec
		return fmt.Errorf("bundle: chmod bundle: %w", err)
	}
	return nil
}

// BuildAssetsTar is the exported version of buildAssetsTar for use by the CLI.
// It builds a single-platform assets tar without meta.json (for legacy use).
func BuildAssetsTar(archiveBytes []byte, ociLayers []OCILayer) ([]byte, error) {
	return buildAssetsTar(archiveBytes, map[string][]OCILayer{"": ociLayers}, nil, BundleMeta{})
}

// buildAssetsTar constructs the uncompressed tar archive with a rigid directory structure:
//
//	layers/arm64.tar
//	layers/amd64.tar
//	workspace.tar.gz
//	lib/<platform>/<lib>
//	meta.json
//
// platformLibs maps "darwin-arm64" → {libkrun.dylib, libkrunfw.dylib}, etc.
// If nil, falls back to discoverLibkrunAssets for single-platform behavior.
func buildAssetsTar(archiveBytes []byte, multiArchLayers map[string][]OCILayer, platformLibs map[string][]string, meta BundleMeta) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	if len(archiveBytes) > 0 {
		if err := writeTarEntry(tw, "workspace.tar.gz", archiveBytes); err != nil {
			return nil, err
		}
	}

	for arch, layers := range multiArchLayers {
		for _, layer := range layers {
			bundlePath := "layers/" + arch + ".tar"
			if err := writeTarEntry(tw, bundlePath, layer.Data); err != nil {
				return nil, err
			}
		}
	}

	if platformLibs != nil {
		for platform, paths := range platformLibs {
			for _, p := range paths {
				data, readErr := os.ReadFile(p)
				if readErr != nil {
					return nil, fmt.Errorf("bundle: read lib asset %s for %s: %w", p, platform, readErr)
				}
				name := "lib/" + platform + "/" + filepath.Base(p)
				if err := writeTarEntry(tw, name, data); err != nil {
					return nil, err
				}
			}
		}
	} else {
		libPaths, libErr := discoverLibkrunAssets()
		if libErr != nil {
			return nil, libErr
		}
		for _, p := range libPaths {
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil, fmt.Errorf("bundle: read lib asset %s: %w", p, readErr)
			}
			name := "lib/" + filepath.Base(p)
			if err := writeTarEntry(tw, name, data); err != nil {
				return nil, err
			}
		}
	}

	// Write meta.json
	metaBytes, err := MarshalMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("bundle: marshal meta: %w", err)
	}
	if err := writeTarEntry(tw, "meta.json", metaBytes); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("bundle: close assets tar: %w", err)
	}
	return buf.Bytes(), nil
}

func defaultLibkrunShareDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "lib"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("bundle: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "nexus", "lib"), nil
}

func resolveLibkrunSearchDirs() ([]string, error) {
	dirs := make([]string, 0, 8)
	seen := map[string]struct{}{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		dirs = append(dirs, clean)
	}

	add(os.Getenv("NEXUS_LIBKRUN_DIR"))

	libDir, err := defaultLibkrunShareDir()
	if err != nil {
		return nil, err
	}
	add(libDir)

	exe, err := os.Executable()
	if err == nil {
		real := exe
		if eval, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
			real = eval
		}
		exeDir := filepath.Dir(real)
		add(filepath.Join(exeDir, "lib"))
		add(filepath.Join(exeDir, "..", "lib"))
		add(exeDir)
	}

	if runtime.GOOS == "darwin" {
		add("/opt/homebrew/lib")
		add("/usr/local/lib")
	}

	return dirs, nil
}

func discoverLibkrunAssets() ([]string, error) {
	searchDirs, err := resolveLibkrunSearchDirs()
	if err != nil {
		return nil, err
	}
	for _, dir := range searchDirs {
		libkrunPath, libkrunfwPath := libkrun.LibPaths(dir)
		paths := []string{libkrunPath, libkrunfwPath}
		allPresent := true
		for _, p := range paths {
			if _, statErr := os.Stat(p); statErr != nil {
				if os.IsNotExist(statErr) {
					allPresent = false
					break
				}
				return nil, fmt.Errorf("bundle: stat lib asset %s: %w", p, statErr)
			}
		}
		if allPresent {
			return paths, nil
		}
	}
	// Allow export helper usage in environments without local libkrun assets
	// (e.g. unit tests and hosts that only consume bundles). Export() enforces
	// a strict runtime-library requirement before writing a bundle file.
	return nil, nil
}

// readCurrentBinary returns the bytes of the currently running nexus executable.
func readCurrentBinary() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		real = exe
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", real, err)
	}
	return data, nil
}

// readCrossPlatformBinaries returns nexus binaries for darwin-arm64 and linux-amd64.
// The current platform's binary is read from the running executable.
// The other platform's binary is located via search paths or cross-compiled.
func readCrossPlatformBinaries() (darwinBin, linuxBin []byte, err error) {
	currentBin, err := readCurrentBinary()
	if err != nil {
		return nil, nil, err
	}

	switch runtime.GOOS + "-" + runtime.GOARCH {
	case "darwin-arm64":
		darwinBin = currentBin
		linuxBin, err = findCrossBinary("linux-amd64")
	case "linux-amd64":
		linuxBin = currentBin
		darwinBin, err = findCrossBinary("darwin-arm64")
	default:
		return nil, nil, fmt.Errorf("bundle: unsupported export platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("bundle: find cross-platform binary: %w", err)
	}
	return darwinBin, linuxBin, nil
}

// findCrossBinary locates a pre-built nexus binary for the given platform.
// Search order:
//  1. $NEXUS_CROSS_BINARY_DIR/nexus-<platform> (explicit override)
//  2. Adjacent to the current executable (e.g. nexus-linux-amd64 next to nexus)
//  3. packages/nexus-swift/Resources/nexus-<platform> (repo layout)
//  4. ~/.local/share/nexus/bin/nexus-<platform>
func findCrossBinary(platform string) ([]byte, error) {
	candidates := []string{}

	if envDir := os.Getenv("NEXUS_CROSS_BINARY_DIR"); envDir != "" {
		candidates = append(candidates, filepath.Join(envDir, "nexus-"+platform))
	}

	exe, _ := os.Executable()
	if exe != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "nexus-"+platform))
	}

	relResource := filepath.Join("packages", "nexus-swift", "Resources", "nexus-"+platform)
	candidates = append(candidates, relResource)

	if dir, _ := os.Getwd(); dir != "" {
		for d := dir; d != "" && d != "/"; d = filepath.Dir(d) {
			p := filepath.Join(d, relResource)
			if p != relResource {
				candidates = append(candidates, p)
			}
			if _, err := os.Stat(filepath.Join(d, "go.work")); err == nil {
				candidates = append(candidates, filepath.Join(d, relResource))
				break
			}
		}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "nexus", "bin", "nexus-"+platform))
	}

	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("no pre-built nexus-%s found — build one with: GOOS=%s GOARCH=%s go build -o nexus-%s ./cmd/nexus/",
		platform, strings.SplitN(platform, "-", 2)[0], strings.SplitN(platform, "-", 2)[1], platform)
}

// nxFatShellStub returns a POSIX shell script that embeds offset/size pairs for
// both darwin-arm64 and linux-amd64 nexus binaries. The stub detects the host
// platform at runtime and dd-extracts the matching binary.
//
// Layout after stub: [darwin-arm64 binary][linux-amd64 binary][NXPACK data]
func nxFatShellStub(darwinSize, linuxSize int) []byte {
	const placeholder = "XXXXXXXXXXXXXXXXXX"

	scriptTemplate := "#!/bin/sh\n" +
		"# Nexus self-executing bundle — fully self-contained, no nexus in PATH needed.\n" +
		"# Usage: ./bundle.nxbundle [args...]\n" +
		"set -e\n" +
		"_DARWIN_OFFSET=" + placeholder + "\n" +
		"_DARWIN_SIZE=" + placeholder + "\n" +
		"_LINUX_OFFSET=" + placeholder + "\n" +
		"_LINUX_SIZE=" + placeholder + "\n" +
		"case \"$(uname -s)-$(uname -m)\" in\n" +
		"  Darwin-arm64)  _OFFSET=$_DARWIN_OFFSET _SIZE=$_DARWIN_SIZE ;;\n" +
		"  Linux-x86_64)  _OFFSET=$_LINUX_OFFSET  _SIZE=$_LINUX_SIZE  ;;\n" +
		"  *) echo \"unsupported platform (supported: darwin-arm64, linux-amd64)\"; exit 1 ;;\n" +
		"esac\n" +
		"_TMP=$(mktemp /tmp/.nexus-runner-XXXXXX)\n" +
		"dd if=\"$0\" bs=1 skip=\"$_OFFSET\" count=\"$_SIZE\" of=\"$_TMP\" 2>/dev/null\n" +
		"chmod +x \"$_TMP\"\n" +
		"if [ \"$(uname -s)\" = \"Darwin\" ] && command -v codesign >/dev/null 2>&1; then\n" +
		"  _ENT=$(mktemp /tmp/.nexus-ent-XXXXXX)\n" +
		"  base64 -d <<'NXENT' >\"$_ENT\"\n" +
		macOSEntitlementsBase64() + "\n" +
		"NXENT\n" +
		"  codesign --sign - --force --entitlements \"$_ENT\" \"$_TMP\" >/dev/null 2>&1 || true\n" +
		"  rm -f \"$_ENT\"\n" +
		"fi\n" +
		"exec \"$_TMP\" bundle run \"$0\" \"$@\"\n" +
		": <<'NXPACK_DATA_FOLLOWS'\n"

	templateLen := len(scriptTemplate)
	stubLen := templateLen

	darwinOffset := stubLen
	linuxOffset := stubLen + darwinSize

	offsets := []struct {
		placeholderIdx int
		value          string
	}{
		{0, fmt.Sprintf("%d", darwinOffset)},
		{1, fmt.Sprintf("%d", darwinSize)},
		{2, fmt.Sprintf("%d", linuxOffset)},
		{3, fmt.Sprintf("%d", linuxSize)},
	}

	script := scriptTemplate
	for _, o := range offsets {
		padded := fmt.Sprintf("%-*s", len(placeholder), o.value)
		script = replaceFirst(script, placeholder, padded)
	}

	if len(script) != templateLen {
		panic(fmt.Sprintf("bundle: fat stub length changed after substitution: %d → %d", templateLen, len(script)))
	}

	return []byte(script)
}

// nxpackShellStub returns a minimal POSIX shell script that, when executed,
// extracts the embedded nexus binary (which follows the script at a known byte
// offset) to a temp file and execs it with `bundle run "$0" "$@"`.
func nxpackShellStub(nexusSize int) []byte {
	const placeholder = "XXXXXXXXXXXXXXXXXX"

	scriptTemplate := "#!/bin/sh\n" +
		"# Nexus self-executing bundle — fully self-contained, no nexus in PATH needed.\n" +
		"# Usage: ./bundle.nxbundle [args...]\n" +
		"set -e\n" +
		"_NEXUS_OFFSET=" + placeholder + "\n" +
		"_NEXUS_SIZE=" + placeholder + "\n" +
		"# On macOS, prefer the installed nexus binary to preserve code-signing entitlements\n" +
		"# (required for Hypervisor.framework). Falls back to embedded binary extraction.\n" +
		"if command -v nexus >/dev/null 2>&1; then\n" +
		"  exec nexus bundle run \"$0\" \"$@\"\n" +
		"fi\n" +
		"_TMP=$(mktemp /tmp/.nexus-runner-XXXXXX)\n" +
		"dd if=\"$0\" bs=1 skip=\"$_NEXUS_OFFSET\" count=\"$_NEXUS_SIZE\" of=\"$_TMP\" 2>/dev/null\n" +
		"chmod +x \"$_TMP\"\n" +
		"# macOS: re-sign extracted binary with hypervisor entitlements so\n" +
		"# libkrun can use Hypervisor.framework.\n" +
		"if [ \"$(uname -s)\" = \"Darwin\" ] && command -v codesign >/dev/null 2>&1; then\n" +
		"  _ENT=$(mktemp /tmp/.nexus-ent-XXXXXX)\n" +
		"  base64 -d <<'NXENT' >\"$_ENT\"\n" +
		macOSEntitlementsBase64() + "\n" +
		"NXENT\n" +
		"  codesign --sign - --force --entitlements \"$_ENT\" \"$_TMP\" >/dev/null 2>&1 || true\n" +
		"  rm -f \"$_ENT\"\n" +
		"fi\n" +
		"exec \"$_TMP\" bundle run \"$0\" \"$@\"\n" +
		": <<'NXPACK_DATA_FOLLOWS'\n"

	templateLen := len(scriptTemplate)
	nexusOffset := templateLen

	offsetStr := fmt.Sprintf("%d", nexusOffset)
	sizeStr := fmt.Sprintf("%d", nexusSize)

	offsetPadded := fmt.Sprintf("%-*s", len(placeholder), offsetStr)
	sizePadded := fmt.Sprintf("%-*s", len(placeholder), sizeStr)

	script := scriptTemplate
	script = replaceFirst(script, placeholder, offsetPadded)
	script = replaceFirst(script, placeholder, sizePadded)

	if len(script) != templateLen {
		panic(fmt.Sprintf("bundle: stub length changed after substitution: %d → %d", templateLen, len(script)))
	}

	return []byte(script)
}

// macOSEntitlementsBase64 returns the base64-encoded macOS entitlements plist
// required for Hypervisor.framework access (com.apple.security.hypervisor)
// and dynamic library loading (com.apple.security.cs.disable-library-validation).
func macOSEntitlementsBase64() string {
	return "PD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZz0iVVRGLTgiPz4KPCFET0NUWVBFIHBsaXN0IFBVQkxJQyAiLS8vQXBwbGUvL0RURCBQTElTVCAxLjAvL0VOIiAiaHR0cDovL3d3dy5hcHBsZS5jb20vRFREcy9Qcm9wZXJ0eUxpc3QtMS4wLmR0ZCI+CjxwbGlzdCB2ZXJzaW9uPSIxLjAiPgo8ZGljdD4KICAgIDxrZXk+Y29tLmFwcGxlLnNlY3VyaXR5Lmh5cGVydmlzb3I8L2tleT4KICAgIDx0cnVlLz4KICAgIDxrZXk+Y29tLmFwcGxlLnNlY3VyaXR5LmNzLmRpc2FibGUtbGlicmFyeS12YWxpZGF0aW9uPC9rZXk+CiAgICA8dHJ1ZS8+CjwvZGljdD4KPC9wbGlzdD4K"
}

// replaceFirst replaces the first occurrence of old in s with new.
func replaceFirst(s, old, new string) string {
	idx := len(s)
	for i := 0; i <= len(s)-len(old); i++ {
		if s[i:i+len(old)] == old {
			idx = i
			break
		}
	}
	if idx == len(s) {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

// mergeOCILayersToTar merges a slice of OCI layer tars into a single tar archive
// by applying them in order (later layers overwrite earlier ones, whiteout
// entries delete files). The resulting tar contains the fully merged rootfs.
func mergeOCILayersToTar(layers []OCILayer) ([]byte, error) {
	// Create a temp directory to accumulate merged contents.
	parentDir, err := os.MkdirTemp("", "nexus-merge-layers-*")
	if err != nil {
		return nil, fmt.Errorf("bundle: create merge temp dir: %w", err)
	}
	defer os.RemoveAll(parentDir)

	tmpDir := filepath.Join(parentDir, "merged")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("bundle: create merged dir: %w", err)
	}

	// Extract each layer in order, applying whiteout semantics.
	for _, layer := range layers {
		layerDir := filepath.Join(parentDir, "layer-"+sha256Hex(layer.Data)[:8])
		if err := os.MkdirAll(layerDir, 0o755); err != nil {
			return nil, fmt.Errorf("bundle: mkdir layer dir: %w", err)
		}
		if err := extractTarBytes(bytes.NewReader(layer.Data), layerDir); err != nil {
			return nil, fmt.Errorf("bundle: extract layer %s: %w", layer.Digest, err)
		}
		if err := applyLayerDir(layerDir, tmpDir); err != nil {
			return nil, fmt.Errorf("bundle: apply layer %s: %w", layer.Digest, err)
		}
	}

	// Re-pack the merged directory into a single tar.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tmpDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, _ = os.Readlink(path)
		}
		hdr, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("bundle: walk merged dir: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("bundle: close merged tar: %w", err)
	}
	return buf.Bytes(), nil
}

// extractTarBytes extracts a plain tar archive from r into destDir.
func extractTarBytes(r io.Reader, destDir string) error {
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
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) && target != destDir {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
	return nil
}

// applyLayerDir copies the contents of srcDir into destDir, applying OCI
// whiteout semantics (same as runner.go but duplicated here to avoid import cycles).
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
		if strings.HasPrefix(base, ".wh.") {
			target := filepath.Join(filepath.Dir(destPath), strings.TrimPrefix(base, ".wh."))
			_ = os.RemoveAll(target)
			return nil
		}
		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, lErr := os.Readlink(path)
			if lErr != nil {
				return lErr
			}
			_ = os.Remove(destPath)
			return os.Symlink(link, destPath)
		}
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
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// sha256Hex returns the lowercase hex-encoded SHA-256 digest of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// writeTarEntry writes a single file entry into tw.
func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("bundle: write tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("bundle: write tar entry %s: %w", name, err)
	}
	return nil
}
