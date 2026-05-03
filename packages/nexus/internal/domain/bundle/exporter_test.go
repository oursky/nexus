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
// archive or OCI layers produces a valid tar with an inventory.
func TestBuildAssetsTar_NoWorkspace(t *testing.T) {
	tarBytes, inventory, err := buildAssetsTar(nil, nil, nil)
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	if inventory == nil {
		t.Fatal("inventory is nil")
	}
	if inventory.Workspace != nil {
		t.Errorf("expected no workspace entry, got %+v", inventory.Workspace)
	}
	if inventory.AgentRootfs != nil {
		t.Errorf("expected no agentRootfs entry, got %+v", inventory.AgentRootfs)
	}
	if len(inventory.Layers) != 0 {
		t.Errorf("expected no layers, got %d", len(inventory.Layers))
	}
	// Should not contain any agent-rootfs entry.
	if tarHasEntry(t, tarBytes, "payload/agent-rootfs.tar") {
		t.Error("tar unexpectedly contains payload/agent-rootfs.tar")
	}
}

// TestBuildAssetsTar_WithWorkspace verifies workspace bytes are packed correctly.
func TestBuildAssetsTar_WithWorkspace(t *testing.T) {
	fakeArchive := []byte("fake workspace tar.gz data")
	tarBytes, inventory, err := buildAssetsTar(fakeArchive, nil, nil)
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	if inventory.Workspace == nil {
		t.Fatal("expected workspace entry in inventory")
	}
	if inventory.Workspace.Path != "payload/workspace.tar.gz" {
		t.Errorf("unexpected workspace path: %s", inventory.Workspace.Path)
	}
	if inventory.Workspace.Size != int64(len(fakeArchive)) {
		t.Errorf("workspace size mismatch: got %d want %d", inventory.Workspace.Size, len(fakeArchive))
	}
	if !tarHasEntry(t, tarBytes, "payload/workspace.tar.gz") {
		t.Error("tar does not contain payload/workspace.tar.gz")
	}
}

// TestBuildAssetsTar_WithOCILayers verifies OCI layers are packed and inventoried.
func TestBuildAssetsTar_WithOCILayers(t *testing.T) {
	layers := []OCILayer{
		{Digest: "sha256:aabbccddeeff001122334455", Data: []byte("layer1")},
		{Digest: "sha256:112233445566aabbccddeeff", Data: []byte("layer2")},
	}
	_, inventory, err := buildAssetsTar(nil, map[string][]OCILayer{"": layers}, nil)
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	if len(inventory.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(inventory.Layers))
	}
	for i, l := range inventory.Layers {
		if l.Digest != layers[i].Digest {
			t.Errorf("layer %d digest mismatch: got %s want %s", i, l.Digest, layers[i].Digest)
		}
		if l.Size != int64(len(layers[i].Data)) {
			t.Errorf("layer %d size mismatch: got %d want %d", i, l.Size, len(layers[i].Data))
		}
	}
}

// TestWriteNXPackBundle verifies that writeNXPackBundle produces a valid NXPACK
// file with a shell stub, readable footer, and extractable manifest.
func TestWriteNXPackBundle(t *testing.T) {
	// Build a minimal assets tar.
	tarBytes, _, err := buildAssetsTar(nil, nil, nil)
	if err != nil {
		t.Fatalf("buildAssetsTar: %v", err)
	}
	assetsBlob, err := CompressZstd(tarBytes)
	if err != nil {
		t.Fatalf("CompressZstd: %v", err)
	}

	manifestJSON := []byte(`{"schemaVersion":"2","bundleVersion":"2.0.0"}`)

	tmp, err := os.CreateTemp(t.TempDir(), "*.nxbundle")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	if err := writeNXPackBundle(tmp.Name(), assetsBlob, manifestJSON); err != nil {
		t.Fatalf("writeNXPackBundle: %v", err)
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
	if footer.ManifestSize != uint64(len(manifestJSON)) {
		t.Errorf("ManifestSize mismatch: got %d want %d", footer.ManifestSize, len(manifestJSON))
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

	// ExtractNXPackManifest must round-trip the manifest.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	gotManifest, err := ExtractNXPackManifest(f)
	if err != nil {
		t.Fatalf("ExtractNXPackManifest: %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("manifest mismatch:\n got: %s\nwant: %s", gotManifest, manifestJSON)
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
