package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
)

// TestBuildExtractedBundle_Layers verifies that layer dirs are mapped correctly.
func TestBuildExtractedBundle_Layers(t *testing.T) {
	cacheDir := t.TempDir()
	// Create a fake extracted layer dir so buildExtractedBundle finds it.
	layerDir := filepath.Join(cacheDir, "layers", runtime.GOARCH)
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "dummy"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	meta := bundle.BundleMeta{
		Arch: []string{"arm64", "amd64"},
	}

	eb := buildExtractedBundle(cacheDir, meta)

	// Should have 1 layer dir for current arch.
	if len(eb.LayerDirs) != 1 {
		t.Fatalf("expected 1 layer dir for current arch, got %d", len(eb.LayerDirs))
	}
	want := filepath.Join(cacheDir, "layers", runtime.GOARCH)
	if eb.LayerDirs[0] != want {
		t.Errorf("layer dir mismatch: got %q want %q", eb.LayerDirs[0], want)
	}
}

// TestResolveDestPath_Layers verifies layer paths are routed correctly.
func TestResolveDestPath_Layers(t *testing.T) {
	destDir := "/tmp/cache"
	dest, extractInner, err := resolveDestPath("layers/arm64.tar", destDir)
	if err != nil {
		t.Fatalf("resolveDestPath: %v", err)
	}
	if !extractInner {
		t.Error("expected extractInner=true for layer")
	}
	want := filepath.Join(destDir, "layers", "arm64")
	if dest != want {
		t.Errorf("dest mismatch: got %q want %q", dest, want)
	}
}

// TestResolveDestPath_UnknownEntry returns an error for unrecognised paths.
func TestResolveDestPath_UnknownEntry(t *testing.T) {
	_, _, err := resolveDestPath("payload/something-else.dat", "/tmp/cache")
	if err == nil {
		t.Error("expected error for unknown entry")
	}
}
