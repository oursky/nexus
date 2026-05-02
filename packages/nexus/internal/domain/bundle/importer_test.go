package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func makeTestNXPackBundle(t *testing.T, workspaceName string) string {
	t.Helper()

	assetsTar, _, err := buildAssetsTar(nil, nil, nil)
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	assetsBlob, err := CompressZstd(assetsTar)
	if err != nil {
		t.Fatalf("CompressZstd: %v", err)
	}

	manifest := BundleManifest{
		SchemaVersion: SchemaVersion,
		BundleVersion: BundleVersion,
		CreatedAt:     "2026-01-01T00:00:00Z",
		Source: SourceMetadata{
			WorkspaceName: workspaceName,
			Ref:           "main",
		},
		Compatibility: CompatibilityMeta{
			Arch:     []string{"amd64", "arm64"},
			Backend:  []string{"process"},
			OsFamily: []string{"darwin", "linux"},
		},
		WorkspaceIntent: WorkspaceIntent{
			Up: []string{"echo up"},
		},
		Payload:   PayloadIndex{Entries: []PayloadEntry{}},
		Integrity: IntegrityMetadata{Algorithm: "sha256"},
	}

	// Compute manifest digest before final marshal.
	preBytes, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest pre: %v", err)
	}
	h := sha256.New()
	h.Write(preBytes)
	manifest.Integrity.ManifestDigest = hex.EncodeToString(h.Sum(nil))

	finalManifest, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest final: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), workspaceName+".nxbundle")
	if err := writeNXPackBundle(bundlePath, assetsBlob, finalManifest); err != nil {
		t.Fatalf("writeNXPackBundle: %v", err)
	}
	return bundlePath
}

// TestImporter_NXPACK_DryRun verifies that Import with dryRun=true reads
// the manifest and prints a compatibility report without provisioning.
func TestImporter_NXPACK_DryRun(t *testing.T) {
	bundlePath := makeTestNXPackBundle(t, "dryrun-ws")
	imp := NewImporter()
	if err := imp.Import(context.Background(), bundlePath, true); err != nil {
		t.Fatalf("Import dry-run: %v", err)
	}
}

// TestImporter_NXPACK_Extract verifies that Import with dryRun=false
// completes without error and creates the bundle cache directory.
func TestImporter_NXPACK_Extract(t *testing.T) {
	bundlePath := makeTestNXPackBundle(t, "extract-ws")
	imp := NewImporter()
	if err := imp.Import(context.Background(), bundlePath, false); err != nil {
		t.Fatalf("Import: %v", err)
	}

	cacheDir, err := bundleCacheDir(bundlePath)
	if err != nil {
		t.Fatalf("bundleCacheDir: %v", err)
	}

	// The cache directory must exist after a successful import.
	if _, statErr := os.Stat(cacheDir); statErr != nil {
		t.Errorf("expected cache dir at %s: %v", cacheDir, statErr)
	}
}

// TestImporter_NXPACK_Idempotent verifies that calling Import twice does not
// return an error (idempotent via .extracted marker).
func TestImporter_NXPACK_Idempotent(t *testing.T) {
	bundlePath := makeTestNXPackBundle(t, "idempotent-ws")
	imp := NewImporter()
	if err := imp.Import(context.Background(), bundlePath, false); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if err := imp.Import(context.Background(), bundlePath, false); err != nil {
		t.Fatalf("second Import (idempotent): %v", err)
	}
}

// TestExtractBundle verifies the public ExtractBundle helper used by nexus bundle run.
func TestExtractBundle(t *testing.T) {
	bundlePath := makeTestNXPackBundle(t, "extract-bundle-ws")
	cacheDir, err := ExtractBundle(bundlePath)
	if err != nil {
		t.Fatalf("ExtractBundle: %v", err)
	}
	if cacheDir == "" {
		t.Fatal("ExtractBundle returned empty cacheDir")
	}
	// Marker file must exist.
	marker := filepath.Join(cacheDir, ".extracted")
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("expected .extracted marker at %s: %v", marker, statErr)
	}
}
