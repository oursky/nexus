package bundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func makeTestNXPackBundle(t *testing.T, workspaceName string) string {
	t.Helper()
	requireCrossPlatformBinaries(t)

	meta := BundleMeta{
		Arch: []string{"amd64", "arm64"},
		Up:   []string{"echo up"},
	}
	assetsTar, err := buildAssetsTar(nil, nil, nil, meta)
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	assetsBlob, err := CompressZstd(assetsTar)
	if err != nil {
		t.Fatalf("CompressZstd: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), workspaceName+".nxbundle")
	if err := WriteNXPackBundle(bundlePath, assetsBlob); err != nil {
		t.Fatalf("writeNXPackBundle: %v", err)
	}
	return bundlePath
}

// TestImporter_NXPACK_DryRun verifies that Import with dryRun=true reads
// the meta and prints a compatibility report without provisioning.
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
