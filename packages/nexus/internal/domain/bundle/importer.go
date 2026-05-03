package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Importer validates and imports a .nxbundle archive.
type Importer struct{}

// NewImporter constructs an Importer.
func NewImporter() *Importer {
	return &Importer{}
}

// Import opens a .nxbundle, validates it, and (when dryRun is false) provisions
// the bundle cache directory so the workspace is ready to run.
//
// The function detects the bundle format automatically:
//   - NXPACK (v2): footer-addressed binary format produced by the current exporter
//   - Legacy gzip-tar (v1): tar.gz with manifest.json at root (read-only; no provisioning)
func (imp *Importer) Import(ctx context.Context, bundlePath string, dryRun bool) error {
	f, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("bundle: open bundle: %w", err)
	}
	defer f.Close()

	// Detect format by trying to read the NXPACK footer.
	footer, footerErr := ReadNXPackFooter(f)
	if footerErr == nil {
		return imp.importNXPack(ctx, f, footer, bundlePath, dryRun)
	}

	// Fall back to legacy gzip-tar format.
	return imp.importLegacyTarGz(ctx, f, dryRun)
}

// importNXPack handles NXPACK v2 bundles.
func (imp *Importer) importNXPack(ctx context.Context, f *os.File, footer PackFooter, bundlePath string, dryRun bool) error {
	// Read manifest JSON.
	if _, err := f.Seek(int64(footer.ManifestOffset), io.SeekStart); err != nil {
		return fmt.Errorf("bundle: seek to manifest: %w", err)
	}
	manifestData := make([]byte, footer.ManifestSize)
	if _, err := io.ReadFull(f, manifestData); err != nil {
		return fmt.Errorf("bundle: read manifest: %w", err)
	}

	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("cannot parse manifest: %v", err)}
	}
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	if err := verifyIntegrity(manifest, manifestData); err != nil {
		return err
	}
	if err := checkCompatibility(manifest); err != nil {
		return err
	}

	// Read assets blob.
	if _, err := f.Seek(int64(footer.AssetsOffset), io.SeekStart); err != nil {
		return fmt.Errorf("bundle: seek to assets: %w", err)
	}
	assetsBlob := make([]byte, footer.AssetsSize)
	if _, err := io.ReadFull(f, assetsBlob); err != nil {
		return fmt.Errorf("bundle: read assets blob: %w", err)
	}

	// Decompress assets.
	assetsTar, err := DecompressZstd(assetsBlob)
	if err != nil {
		return fmt.Errorf("bundle: decompress assets: %w", err)
	}

	hasWorkspace := hasAssetEntry(assetsTar, "payload/workspace.tar.gz")

	if dryRun {
		printCompatibilityReportNXPack(manifest, hasWorkspace)
		return nil
	}

	// Extract to cache directory.
	cacheDir, err := bundleCacheDir(bundlePath)
	if err != nil {
		return err
	}

	if err := extractAssetsTar(assetsTar, cacheDir); err != nil {
		return fmt.Errorf("bundle: extract assets: %w", err)
	}

	fmt.Printf("import complete: workspace %q cached at %s\n", manifest.Source.WorkspaceName, cacheDir)
	return nil
}

// importLegacyTarGz handles the legacy gzip-tar v1 format (read/validate only).
func (imp *Importer) importLegacyTarGz(_ context.Context, f *os.File, dryRun bool) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("bundle: rewind file: %w", err)
	}

	// Try gzip.
	gr, err := gzip.NewReader(f)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("not a valid bundle (not NXPACK, not gzip): %v", err)}
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	manifestBytes, hasWorkspacePayload, err := scanBundle(tr)
	if err != nil {
		return err
	}

	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("cannot parse manifest.json: %v", err)}
	}
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	if err := verifyIntegrity(manifest, manifestBytes); err != nil {
		return err
	}
	if err := checkCompatibility(manifest); err != nil {
		return err
	}

	if dryRun {
		printCompatibilityReport(manifest, hasWorkspacePayload)
		return nil
	}

	fmt.Printf("import complete: workspace %q (legacy bundle, no VM provisioning)\n", manifest.Source.WorkspaceName)
	return nil
}

// ExtractBundle extracts an NXPACK bundle to the default cache location and
// returns the cache directory path. If already extracted (marker present), it
// returns the existing path immediately.
func ExtractBundle(bundlePath string) (string, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return "", fmt.Errorf("bundle: open: %w", err)
	}
	defer f.Close()

	footer, err := ReadNXPackFooter(f)
	if err != nil {
		return "", fmt.Errorf("bundle: read footer: %w", err)
	}

	cacheDir, err := bundleCacheDir(bundlePath)
	if err != nil {
		return "", err
	}

	// Check extraction marker.
	marker := filepath.Join(cacheDir, ".extracted")
	if _, statErr := os.Stat(marker); statErr == nil {
		return cacheDir, nil
	}

	// Read + decompress assets.
	if _, err := f.Seek(int64(footer.AssetsOffset), io.SeekStart); err != nil {
		return "", fmt.Errorf("bundle: seek assets: %w", err)
	}
	assetsBlob := make([]byte, footer.AssetsSize)
	if _, err := io.ReadFull(f, assetsBlob); err != nil {
		return "", fmt.Errorf("bundle: read assets: %w", err)
	}
	assetsTar, err := DecompressZstd(assetsBlob)
	if err != nil {
		return "", fmt.Errorf("bundle: decompress assets: %w", err)
	}

	if err := extractAssetsTar(assetsTar, cacheDir); err != nil {
		return "", err
	}

	// Expand workspace snapshot into workspace/ if present.
	workspaceTarGz := filepath.Join(cacheDir, "payload", "workspace.tar.gz")
	if _, statErr := os.Stat(workspaceTarGz); statErr == nil {
		workspaceDir := filepath.Join(cacheDir, "workspace")
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return "", fmt.Errorf("bundle: mkdir workspace: %w", err)
		}
		if err := extractTarGzInto(workspaceTarGz, workspaceDir); err != nil {
			return "", fmt.Errorf("bundle: extract workspace snapshot: %w", err)
		}
	}

	// Write the manifest to cacheDir/manifest.json so `nexus bundle run` can read it.
	if _, err := f.Seek(int64(footer.ManifestOffset), io.SeekStart); err != nil {
		return "", fmt.Errorf("bundle: seek manifest for write: %w", err)
	}
	manifestData := make([]byte, footer.ManifestSize)
	if _, err := io.ReadFull(f, manifestData); err != nil {
		return "", fmt.Errorf("bundle: read manifest for write: %w", err)
	}
	manifestDest := filepath.Join(cacheDir, "manifest.json")
	if err := os.WriteFile(manifestDest, manifestData, 0o644); err != nil {
		return "", fmt.Errorf("bundle: write manifest: %w", err)
	}

	// Write marker.
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		return "", fmt.Errorf("bundle: write extraction marker: %w", err)
	}

	return cacheDir, nil
}

// bundleCacheDir returns the cache directory for a given bundle file.
// The directory is named after the SHA-256 of the bundle's absolute path
// so it's stable and reproducible.
func bundleCacheDir(bundlePath string) (string, error) {
	abs, err := filepath.Abs(bundlePath)
	if err != nil {
		return "", fmt.Errorf("bundle: resolve path: %w", err)
	}
	h := sha256.Sum256([]byte(abs))
	name := hex.EncodeToString(h[:])[:16]

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("bundle: home dir: %w", err)
	}
	dir := filepath.Join(home, ".cache", "nexus", "bundles", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("bundle: create cache dir: %w", err)
	}
	return dir, nil
}

// extractAssetsTar extracts an uncompressed tar archive into destDir,
// routing entries under their natural paths relative to destDir.
func extractAssetsTar(tarBytes []byte, destDir string) error {
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read assets tar: %w", err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		// Sanitise path — reject absolute paths and ".." traversal.
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || clean == ".." || len(clean) >= 3 && clean[:3] == "../" {
			return fmt.Errorf("bundle: unsafe tar entry %q", hdr.Name)
		}
		dest := filepath.Join(destDir, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("bundle: mkdir for %s: %w", dest, err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("bundle: read entry %s: %w", hdr.Name, err)
		}
		//nolint:gosec // extracted file mode follows tar header
		if err := os.WriteFile(dest, data, os.FileMode(hdr.Mode)&0o755|0o644); err != nil {
			return fmt.Errorf("bundle: write %s: %w", dest, err)
		}
	}
	return nil
}

// extractTarGzInto expands a gzip-compressed tar archive at srcPath into destDir.
func extractTarGzInto(srcPath, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Sanitise path.
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || clean == ".." || len(clean) >= 3 && clean[:3] == "../" {
			return fmt.Errorf("unsafe tar entry %q", hdr.Name)
		}

		dest := filepath.Join(destDir, clean)
		if hdr.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dest, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", dest, err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}
		mode := os.FileMode(hdr.Mode)
		if mode == 0 {
			mode = 0o644
		}
		//nolint:gosec
		if err := os.WriteFile(dest, data, mode); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
	}
	return nil
}

// hasAssetEntry returns true if an uncompressed tar archive contains an entry
// with the given name.
func hasAssetEntry(tarBytes []byte, name string) bool {
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		hdr, err := tr.Next()
		if err != nil {
			return false
		}
		if hdr.Name == name {
			return true
		}
	}
}

// scanBundle reads the tar archive and returns the bytes of manifest.json
// and whether payload/workspace.tar.gz is present.
func scanBundle(tr *tar.Reader) (manifestBytes []byte, hasWorkspacePayload bool, err error) {
	for {
		hdr, hdrErr := tr.Next()
		if hdrErr == io.EOF {
			break
		}
		if hdrErr != nil {
			return nil, false, &InvalidBundle{Reason: fmt.Sprintf("read archive: %v", hdrErr)}
		}
		switch hdr.Name {
		case "manifest.json":
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return nil, false, &InvalidBundle{Reason: fmt.Sprintf("read manifest.json: %v", readErr)}
			}
			manifestBytes = data
		case "payload/workspace.tar.gz":
			hasWorkspacePayload = true
		}
	}
	if manifestBytes == nil {
		return nil, false, &InvalidBundle{Reason: "manifest.json not found in bundle"}
	}
	return manifestBytes, hasWorkspacePayload, nil
}

// verifyIntegrity checks the stored manifestDigest by zeroing the integrity fields,
// re-serialising, and re-hashing — matching the exporter's canonical-bytes scheme.
func verifyIntegrity(manifest BundleManifest, _ []byte) error {
	if manifest.Integrity.Algorithm != "sha256" {
		return &InvalidBundle{Reason: fmt.Sprintf("unsupported digest algorithm: %s", manifest.Integrity.Algorithm)}
	}
	stored := manifest.Integrity.ManifestDigest

	canonical := manifest
	canonical.Integrity.ManifestDigest = ""
	canonicalBytes, err := MarshalManifest(canonical)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("re-serialise for integrity check: %v", err)}
	}

	h := sha256.New()
	h.Write(canonicalBytes)
	got := hex.EncodeToString(h.Sum(nil))
	if got != stored {
		return &IntegrityViolation{Expected: stored, Got: got}
	}
	return nil
}

// checkCompatibility returns IncompatibleHost if the current arch is not listed.
func checkCompatibility(manifest BundleManifest) error {
	arch := runtime.GOARCH
	archOK := false
	for _, a := range manifest.Compatibility.Arch {
		if a == arch {
			archOK = true
			break
		}
	}
	osOK := false
	for _, o := range manifest.Compatibility.OsFamily {
		if o == runtime.GOOS {
			osOK = true
			break
		}
	}
	if !archOK {
		return &IncompatibleHost{Want: manifest.Compatibility.Arch, Got: arch}
	}
	if !osOK {
		return &IncompatibleHost{Want: manifest.Compatibility.OsFamily, Got: runtime.GOOS}
	}
	return nil
}

// printCompatibilityReportNXPack prints a dry-run report for NXPACK bundles.
func printCompatibilityReportNXPack(manifest BundleManifest, hasWorkspace bool) {
	fmt.Printf("dry-run compatibility report\n")
	fmt.Printf("  workspace:  %s\n", manifest.Source.WorkspaceName)
	fmt.Printf("  ref:        %s\n", manifest.Source.Ref)
	fmt.Printf("  arch:       %v — compatible\n", manifest.Compatibility.Arch)
	fmt.Printf("  backend:    %v\n", manifest.Compatibility.Backend)
	fmt.Printf("  osFamily:   %v\n", manifest.Compatibility.OsFamily)
	digestPreview := manifest.Integrity.ManifestDigest
	if len(digestPreview) > 12 {
		digestPreview = digestPreview[:12]
	}
	fmt.Printf("  integrity:  %s (%s) OK\n", digestPreview, manifest.Integrity.Algorithm)
	if hasWorkspace {
		fmt.Printf("  payload:    workspace.tar.gz present\n")
	} else {
		fmt.Printf("  payload:    no workspace snapshot\n")
	}
	if manifest.Runtime != nil {
		fmt.Printf("  runtime:    %s (CPUs: %d, Memory: %dMiB)\n",
			manifest.Runtime.Mode, manifest.Runtime.CPUs, manifest.Runtime.MemMiB)
	}
	fmt.Printf("workspace intent (from Nexusfile):\n")
	fmt.Printf("  workspace.init: %v\n", manifest.WorkspaceIntent.Init)
	fmt.Printf("  workspace.up:   %v\n", manifest.WorkspaceIntent.Up)
	fmt.Printf("  workspace.down: %v\n", manifest.WorkspaceIntent.Down)
	fmt.Printf("  initMode:       %s\n", manifest.WorkspaceIntent.InitMode)
}

// printCompatibilityReport prints a human-readable dry-run report (legacy format).
func printCompatibilityReport(manifest BundleManifest, hasWorkspacePayload bool) {
	fmt.Printf("dry-run compatibility report\n")
	fmt.Printf("  workspace:  %s\n", manifest.Source.WorkspaceName)
	fmt.Printf("  ref:        %s\n", manifest.Source.Ref)
	fmt.Printf("  arch:       %v — compatible\n", manifest.Compatibility.Arch)
	fmt.Printf("  backend:    %v\n", manifest.Compatibility.Backend)
	fmt.Printf("  osFamily:   %v\n", manifest.Compatibility.OsFamily)
	digestPreview := manifest.Integrity.ManifestDigest
	if len(digestPreview) > 12 {
		digestPreview = digestPreview[:12]
	}
	fmt.Printf("  integrity:  %s (%s) OK\n", digestPreview, manifest.Integrity.Algorithm)
	if hasWorkspacePayload {
		fmt.Printf("  payload:    workspace.tar.gz present\n")
	} else {
		fmt.Printf("  payload:    stub (no workspace files)\n")
	}
	fmt.Printf("workspace intent (from Nexusfile):\n")
	fmt.Printf("  workspace.init: %v\n", manifest.WorkspaceIntent.Init)
	fmt.Printf("  workspace.up:   %v\n", manifest.WorkspaceIntent.Up)
	fmt.Printf("  workspace.down: %v\n", manifest.WorkspaceIntent.Down)
	fmt.Printf("  initMode:       %s\n", manifest.WorkspaceIntent.InitMode)
}
