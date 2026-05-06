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

func TestIsPortInUseExposeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "address already in use",
			err:  testErr("address already in use"),
			want: true,
		},
		{
			name: "bind in use message",
			err:  testErr("gvproxy expose 127.0.0.1:3000->192.168.127.2:3000: HTTP 500: listen tcp 127.0.0.1:3000: bind: address already in use"),
			want: true,
		},
		{
			name: "generic in use message",
			err:  testErr("address already in use"),
			want: true,
		},
		{
			name: "different error",
			err:  testErr("connection refused"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isPortInUseExposeError(tc.err)
			if got != tc.want {
				t.Fatalf("isPortInUseExposeError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPortInUseHintNoListener(t *testing.T) {
	hint := portInUseHint(65534)
	if hint != "" {
		t.Fatalf("expected empty hint when no listener, got %q", hint)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }
