package bundle

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
)

// TestBuildAssetsTar_NoWorkspace verifies that buildAssetsTar with no workspace
// archive or OCI layers produces a valid tar with meta.json.
func TestBuildAssetsTar_NoWorkspace(t *testing.T) {
	tarBytes, err := buildAssetsTar(nil, nil, nil, BundleMeta{})
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	if !tarHasEntry(t, tarBytes, "meta.json") {
		t.Error("tar does not contain meta.json")
	}
	if tarHasEntry(t, tarBytes, "workspace.tar.gz") {
		t.Error("tar unexpectedly contains workspace.tar.gz")
	}
}

// TestBuildAssetsTar_WithWorkspace verifies workspace bytes are packed correctly.
func TestBuildAssetsTar_WithWorkspace(t *testing.T) {
	fakeArchive := []byte("fake workspace tar.gz data")
	tarBytes, err := buildAssetsTar(fakeArchive, nil, nil, BundleMeta{})
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	if !tarHasEntry(t, tarBytes, "workspace.tar.gz") {
		t.Error("tar does not contain workspace.tar.gz")
	}
}

// TestBuildAssetsTar_WithOCILayers verifies pre-merged OCI layers are packed.
func TestBuildAssetsTar_WithOCILayers(t *testing.T) {
	merged := []OCILayer{
		{Digest: "sha256:merged001122334455", Data: []byte("merged-layer")},
	}
	tarBytes, err := buildAssetsTar(nil, map[string][]OCILayer{"": merged}, nil, BundleMeta{})
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	if !tarHasEntry(t, tarBytes, "layers/.tar") {
		// arch is empty string in this test
		t.Error("tar does not contain layers")
	}
}

// requireCrossPlatformBinaries skips the test if cross-platform nexus binaries
// are not available (e.g. on Linux CI runners without a pre-built darwin binary).
func requireCrossPlatformBinaries(t *testing.T) {
	t.Helper()
	_, _, err := readCrossPlatformBinaries()
	if err != nil {
		t.Skipf("skipping: cross-platform binaries not available: %v", err)
	}
}

// TestWriteNXPackBundle verifies that WriteNXPackBundle produces a valid NXPACK
// file with a shell stub and readable footer.
func TestWriteNXPackBundle(t *testing.T) {
	requireCrossPlatformBinaries(t)

	// Build a minimal assets tar.
	tarBytes, err := buildAssetsTar(nil, nil, nil, BundleMeta{})
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	assetsBlob, err := CompressZstd(tarBytes)
	if err != nil {
		t.Fatalf("CompressZstd: %v", err)
	}

	tmp, err := os.CreateTemp(t.TempDir(), "*.nxbundle")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	if err := WriteNXPackBundle(tmp.Name(), assetsBlob); err != nil {
		t.Fatalf("WriteNXPackBundle: %v", err)
	}

	// File must be executable.
	fi, err := os.Stat(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("bundle file is not executable: mode %v", fi.Mode())
	}

	// Read footer from the file.
	f, err := os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	footer, err := ReadNXPackFooter(f)
	if err != nil {
		t.Fatalf("ReadNXPackFooter: %v", err)
	}
	if footer.AssetsSize != uint64(len(assetsBlob)) {
		t.Errorf("AssetsSize mismatch: got %d want %d", footer.AssetsSize, len(assetsBlob))
	}

	// Verify shell stub is at the start.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 3)
	if _, err := io.ReadFull(f, head); err != nil {
		t.Fatal(err)
	}
	if string(head) != "#!/" {
		t.Errorf("expected shebang at start, got %q", head)
	}
}

// TestNXPackShellStub verifies the stub is a valid sh shebang and self-extracts.
func TestNXPackShellStub(t *testing.T) {
	const fakeNexusSize = 1234567
	stub := nxpackShellStub(fakeNexusSize)
	s := string(stub)
	if !strings.HasPrefix(s, "#!/bin/sh\n") {
		t.Errorf("stub does not start with shebang: %q", s[:min(len(s), 20)])
	}
	if !strings.Contains(s, "bundle run") {
		t.Error("stub does not invoke 'bundle run'")
	}
	// Stub must contain the nexus size so it can dd-extract the embedded binary.
	if !strings.Contains(s, "1234567") {
		t.Error("stub does not contain embedded nexus size")
	}
	// Stub prefers 'nexus' from PATH on macOS (to preserve code-signing entitlements
	// for Hypervisor.framework) and falls back to the embedded binary otherwise.
	if !strings.Contains(s, "command -v nexus") {
		t.Error("stub does not check for nexus in PATH")
	}
	if !strings.Contains(s, "exec nexus bundle run") {
		t.Error("stub does not prefer installed nexus binary")
	}
	if !strings.Contains(s, "exec \"$_TMP\" bundle run") {
		t.Error("stub does not fall back to embedded binary")
	}
}

func TestMacOSEntitlementsBase64(t *testing.T) {
	b64 := macOSEntitlementsBase64()
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	s := string(decoded)
	if !strings.Contains(s, "com.apple.security.hypervisor") {
		t.Error("entitlements plist missing com.apple.security.hypervisor")
	}
	if !strings.Contains(s, "com.apple.security.cs.disable-library-validation") {
		t.Error("entitlements plist missing com.apple.security.cs.disable-library-validation")
	}
	if !strings.HasPrefix(s, "<?xml") {
		t.Errorf("entitlements does not start with XML declaration: %q", s[:min(len(s), 20)])
	}
}

func TestNXPackShellStub_ContainsEntitlements(t *testing.T) {
	stub := nxpackShellStub(999)
	s := string(stub)
	if !strings.Contains(s, "base64 -d") {
		t.Error("stub does not contain base64 decode step")
	}
	if !strings.Contains(s, "codesign --sign - --force --entitlements") {
		t.Error("stub does not codesign with entitlements")
	}
	if !strings.Contains(s, macOSEntitlementsBase64()) {
		t.Error("stub does not contain the base64 entitlements blob")
	}
}

func TestDiscoverLibkrunAssets_UsesEnvDir(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))

	libPath, fwPath := libkrun.LibPaths(os.Getenv("NEXUS_LIBKRUN_DIR"))
	if err := os.WriteFile(libPath, []byte("lib"), 0o644); err != nil {
		t.Fatalf("write libkrun: %v", err)
	}
	if err := os.WriteFile(fwPath, []byte("fw"), 0o644); err != nil {
		t.Fatalf("write libkrunfw: %v", err)
	}

	got, err := discoverLibkrunAssets()
	if err != nil {
		t.Fatalf("discoverLibkrunAssets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(got))
	}
	if got[0] != libPath || got[1] != fwPath {
		t.Fatalf("unexpected paths: %#v", got)
	}
}

func TestDiscoverLibkrunAssets_MissingReturnsNil(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))

	got, err := discoverLibkrunAssets()
	if err != nil {
		t.Fatalf("discoverLibkrunAssets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no paths, got %#v", got)
	}
}

// tarHasEntry returns true if the tar archive contains an entry with the given name.
func tarHasEntry(t *testing.T, tarBytes []byte, name string) bool {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if hdr.Name == name {
			return true
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
