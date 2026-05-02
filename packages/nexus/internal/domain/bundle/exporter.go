package bundle

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
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
// NXPACK .nxbundle to outPath. The output path receives a ".nxbundle" suffix if
// not already present. The bundle is chmod 0755 so it can be executed directly.
// Returns the output path and any error.
func (e *Exporter) Export(ctx context.Context, workspaceName, outPath string) (string, error) {
	// Ensure .nxbundle suffix.
	if filepath.Ext(outPath) != ".nxbundle" {
		outPath += ".nxbundle"
	}

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

	var wsResult struct {
		Workspace struct {
			WorkspaceName string `json:"workspaceName"`
			Ref           string `json:"ref"`
			Repo          string `json:"repo"`
			Backend       string `json:"backend"`
			RootPath      string `json:"rootPath"`
		} `json:"workspace"`
	}
	if infoErr := rpc.Do(conn, "workspace.info", map[string]any{"id": wsID}, &wsResult); infoErr != nil {
		return "", fmt.Errorf("bundle: fetch workspace info: %w", infoErr)
	}
	ws := wsResult.Workspace

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
	// Always use the default base image — bake commands in the Nexusfile are
	// shell commands that run inside the VM, not OCI image references.
	imageRef := DefaultBaseImage
	ociLayers, layerErr := FetchLayersCached(ctx, imageRef)
	if layerErr != nil {
		return "", fmt.Errorf("bundle: fetch OCI layers for %q: %w", imageRef, layerErr)
	}

	libPaths, libErr := resolvePortableLibkrunAssets(ctx)
	if libErr != nil {
		return "", libErr
	}

	// Build the assets tar (uncompressed) and collect asset inventory.
	assetsTar, inventory, buildErr := buildAssetsTar(archiveBytes, ociLayers, libPaths)
	if buildErr != nil {
		return "", fmt.Errorf("bundle: build assets tar: %w", buildErr)
	}
	if len(inventory.Libraries) == 0 {
		return "", fmt.Errorf("bundle: missing required libkrun runtime libraries in bundle assets (run Nexus bootstrap/install to provision runtime libs before export)")
	}

	// Compress the assets tar with zstd.
	assetsBlob, compErr := CompressZstd(assetsTar)
	if compErr != nil {
		return "", fmt.Errorf("bundle: compress assets: %w", compErr)
	}

	// Build manifest.
	now := time.Now().UTC().Format(time.RFC3339)
	manifest := BundleManifest{
		SchemaVersion: SchemaVersion,
		BundleVersion: BundleVersion,
		CreatedAt:     now,
		Source: SourceMetadata{
			WorkspaceName: ws.WorkspaceName,
			Ref:           ws.Ref,
			ProjectHint:   ws.Repo,
		},
		Compatibility: CompatibilityMeta{
			Arch:     []string{runtime.GOARCH},
			Backend:  []string{ws.Backend},
			OsFamily: []string{runtime.GOOS},
		},
		WorkspaceIntent: workspaceIntent,
		Payload:         PayloadIndex{Entries: []PayloadEntry{}},
		Integrity: IntegrityMetadata{
			Algorithm: "sha256",
		},
		Runtime: &RuntimeConfig{
			Mode:   "vm",
			CPUs:   vmCPUs,
			MemMiB: vmMemMiB,
		},
		Assets: inventory,
	}

	// Serialise manifest without digest to compute the canonical digest.
	manifestBytes, marshalErr := MarshalManifest(manifest)
	if marshalErr != nil {
		return "", marshalErr
	}
	h := sha256.New()
	h.Write(manifestBytes)
	manifest.Integrity.ManifestDigest = hex.EncodeToString(h.Sum(nil))

	// Re-serialise with digest filled in.
	finalManifest, finalErr := MarshalManifest(manifest)
	if finalErr != nil {
		return "", finalErr
	}

	// Write the NXPACK bundle.
	if writeErr := writeNXPackBundle(outPath, assetsBlob, finalManifest); writeErr != nil {
		return "", writeErr
	}

	return outPath, nil
}

// BuildAssetsTar is the exported version of buildAssetsTar for use by the CLI.
func BuildAssetsTar(archiveBytes []byte, ociLayers []OCILayer) ([]byte, *AssetInventory, error) {
	return buildAssetsTar(archiveBytes, ociLayers, nil)
}

// WriteNXPackBundle is the exported version of writeNXPackBundle for use by the CLI.
// It embeds the currently running nexus binary into the bundle.
func WriteNXPackBundle(dst string, assetsBlob, manifestJSON []byte) error {
	return writeNXPackBundle(dst, assetsBlob, manifestJSON)
}

// WriteNXPackBundleWithBinary writes a self-executing NXPACK bundle embedding
// the provided nexus binary bytes instead of reading os.Executable().
// Use this when you need to embed a specific binary (e.g. in tests or tooling).
func WriteNXPackBundleWithBinary(dst string, assetsBlob, manifestJSON, nexusBin []byte) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bundle: create output file: %w", err)
	}
	defer f.Close()

	stub := nxpackShellStub(len(nexusBin))
	preamble := append(stub, nexusBin...) //nolint:gocritic

	if err := WriteNXPack(f, assetsBlob, manifestJSON, preamble); err != nil {
		return err
	}
	if err := f.Chmod(0o755); err != nil { //nolint:gosec
		return fmt.Errorf("bundle: chmod bundle: %w", err)
	}
	return nil
}

// buildAssetsTar constructs the uncompressed tar archive containing:
//   - payload/workspace.tar.gz  (if archiveBytes is non-empty)
//   - payload/layers/<hex>.tar  (for each OCI layer)
//
// It returns the tar bytes and the asset inventory for the manifest.
func buildAssetsTar(archiveBytes []byte, ociLayers []OCILayer, libPaths []string) ([]byte, *AssetInventory, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	inventory := &AssetInventory{
		Libraries: []AssetEntry{},
		Layers:    []LayerEntry{},
	}

	// Workspace snapshot.
	if len(archiveBytes) > 0 {
		if err := writeTarEntry(tw, "payload/workspace.tar.gz", archiveBytes); err != nil {
			return nil, nil, err
		}
		dig := sha256Hex(archiveBytes)
		inventory.Workspace = &AssetEntry{
			Path:   "payload/workspace.tar.gz",
			Size:   int64(len(archiveBytes)),
			SHA256: dig,
		}
	}

	// OCI layers.
	for _, layer := range ociLayers {
		bundlePath := LayerBundlePath(layer.Digest)
		if err := writeTarEntry(tw, bundlePath, layer.Data); err != nil {
			return nil, nil, err
		}
		inventory.Layers = append(inventory.Layers, LayerEntry{
			Digest: layer.Digest,
			Path:   bundlePath,
			Size:   int64(len(layer.Data)),
		})
	}

	if len(libPaths) == 0 {
		// Preserve direct BuildAssetsTar behavior for tests/tooling: only try local discovery.
		var libErr error
		libPaths, libErr = discoverLibkrunAssets()
		if libErr != nil {
			return nil, nil, libErr
		}
	}
	for _, p := range libPaths {
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, nil, fmt.Errorf("bundle: read lib asset %s: %w", p, readErr)
		}
		name := "lib/" + filepath.Base(p)
		if err := writeTarEntry(tw, name, data); err != nil {
			return nil, nil, err
		}
		inventory.Libraries = append(inventory.Libraries, AssetEntry{
			Path:   name,
			Size:   int64(len(data)),
			SHA256: sha256Hex(data),
		})
	}

	if err := tw.Close(); err != nil {
		return nil, nil, fmt.Errorf("bundle: close assets tar: %w", err)
	}
	return buf.Bytes(), inventory, nil
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

// writeNXPackBundle writes a self-executing NXPACK bundle to dst.
// The file is created (or truncated) and marked executable (0755).
//
// The bundle is fully self-contained: the current nexus binary is embedded
// immediately after the shell stub. No nexus installation is required on the
// target machine — the stub extracts and execs the embedded binary.
func writeNXPackBundle(dst string, assetsBlob, manifestJSON []byte) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bundle: create output file: %w", err)
	}
	defer f.Close()

	// Read the current nexus binary to embed it.
	nexusBin, err := readCurrentBinary()
	if err != nil {
		return fmt.Errorf("bundle: read nexus binary: %w", err)
	}

	// Generate stub with the nexus binary size baked in so it can dd-extract it.
	stub := nxpackShellStub(len(nexusBin))

	// Preamble = stub + raw nexus binary.  WriteNXPack writes this before the
	// assets blob; NXPACK footer offsets are absolute from file start and thus
	// account for the full preamble automatically.
	preamble := append(stub, nexusBin...) //nolint:gocritic

	if err := WriteNXPack(f, assetsBlob, manifestJSON, preamble); err != nil {
		return err
	}

	// Ensure the file is executable regardless of umask.
	if err := f.Chmod(0o755); err != nil { //nolint:gosec
		return fmt.Errorf("bundle: chmod bundle: %w", err)
	}
	return nil
}

// readCurrentBinary returns the bytes of the currently running nexus executable.
func readCurrentBinary() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	// Follow symlinks so we get the real binary, not a wrapper script.
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

// nxpackShellStub returns a minimal POSIX shell script that, when executed,
// extracts the embedded nexus binary (which follows the script at a known byte
// offset) to a temp file and execs it with `bundle run "$0" "$@"`.
//
// nexusSize is the byte length of the nexus binary that is appended immediately
// after this stub. The stub length itself is computed from the script text, so
// the caller must pass a nexusSize consistent with what will actually be written.
//
// The script uses a here-doc sentinel so the shell never tries to parse the
// binary data that follows.
func nxpackShellStub(nexusSize int) []byte {
	// We need to know the stub's own byte length to compute NEXUS_OFFSET.
	// Build the script with a placeholder, measure, then substitute.
	const placeholder = "XXXXXXXXXXXXXXXXXX" // 18 chars, wider than any realistic value

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
		"# macOS: re-sign extracted binary so it can execute (best-effort; may lack\n" +
		"# hypervisor entitlement if the host nexus binary is not in PATH).\n" +
		"if [ \"$(uname -s)\" = \"Darwin\" ] && command -v codesign >/dev/null 2>&1; then\n" +
		"  codesign --sign - --force \"$_TMP\" >/dev/null 2>&1 || true\n" +
		"fi\n" +
		"exec \"$_TMP\" bundle run \"$0\" \"$@\"\n" +
		": <<'NXPACK_DATA_FOLLOWS'\n"

	// The stub length with placeholders in place tells us the offset at which
	// the nexus binary starts. Substituting the real numbers changes the length
	// by at most a few bytes; we pad with spaces to keep the length stable.
	templateLen := len(scriptTemplate)
	// NEXUS_OFFSET = len of final stub = templateLen (we'll pad to templateLen exactly).
	nexusOffset := templateLen

	offsetStr := fmt.Sprintf("%d", nexusOffset)
	sizeStr := fmt.Sprintf("%d", nexusSize)

	// Pad each substitution to exactly len(placeholder) chars with trailing spaces.
	// This keeps the stub length == templateLen == nexusOffset. Trailing spaces
	// on a shell assignment are harmless (value is the number, rest is whitespace
	// before the newline — actually shell trims those... use a comment instead).
	// Simpler: pad with leading zeros for the number, which is also fine in shell.
	offsetPadded := fmt.Sprintf("%-*s", len(placeholder), offsetStr)
	sizePadded := fmt.Sprintf("%-*s", len(placeholder), sizeStr)

	script := scriptTemplate
	// Replace first occurrence (NEXUS_OFFSET line), then second (NEXUS_SIZE line).
	script = replaceFirst(script, placeholder, offsetPadded)
	script = replaceFirst(script, placeholder, sizePadded)

	if len(script) != templateLen {
		// Should never happen — padding ensures equal length.
		panic(fmt.Sprintf("bundle: stub length changed after substitution: %d → %d", templateLen, len(script)))
	}

	return []byte(script)
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
