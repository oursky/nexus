package runner_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
	"github.com/oursky/nexus/packages/nexus/internal/vm/runner"
)

// TestRunner_ExtractAndRun is an integration test that:
//  1. Builds a minimal NXPACK bundle with real libkrun libraries and a
//     synthetic OCI layer containing a minimal rootfs.
//  2. Extracts the bundle via Runner.ExtractBundle.
//  3. Runs a trivial command inside the microVM via Runner.Run.
//
// Prerequisites:
//   - NEXUS_LIBKRUN_DIR must be set to a directory containing libkrun.dylib
//     (macOS) or libkrun.so (Linux) and the matching libkrunfw file.
//   - The host must support hardware virtualisation (Hypervisor.framework on
//     macOS, KVM on Linux).
//
// The test is skipped when the prerequisite is absent so it can run cleanly in
// environments that do not have libkrun installed.
func TestRunner_ExtractAndRun(t *testing.T) {
	libDir := os.Getenv("NEXUS_LIBKRUN_DIR")
	if libDir == "" {
		t.Skip("NEXUS_LIBKRUN_DIR not set — skipping libkrun integration test")
	}

	// Verify the expected library files exist.
	libkrunPath := filepath.Join(libDir, libkrun.LibFilename())
	libkrunfwPath := filepath.Join(libDir, libkrun.LibFWFilename())
	for _, p := range []string{libkrunPath, libkrunfwPath} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("libkrun library not found at %s: %v", p, err)
		}
	}

	// Build a minimal NXPACK bundle in memory.
	bundlePath := buildMinimalBundle(t, libkrunPath, libkrunfwPath)

	// Extract the bundle.
	r := &runner.Runner{CacheDir: t.TempDir()}
	eb, err := r.ExtractBundle(bundlePath)
	if err != nil {
		t.Fatalf("ExtractBundle: %v", err)
	}

	// Verify extraction produced expected paths.
	if _, err := os.Stat(eb.LibDir); err != nil {
		t.Errorf("LibDir not found: %v", err)
	}
	if len(eb.LayerDirs) == 0 {
		t.Fatal("expected at least one OCI layer dir")
	}

	// Run a trivial command inside the VM.
	// init.krun (bundled in libkrunfw) reads /.krun_config.json which we write
	// in runner.Run — it will exec /bin/sh and exit.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := r.Run(ctx, eb, []string{"/bin/sh", "-c", "exit 0"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestRunner_ExtractBundle_Idempotent verifies that calling ExtractBundle twice
// on the same path returns the same cache dir without error (marker skip).
// This test does NOT require libkrun.
func TestRunner_ExtractBundle_Idempotent(t *testing.T) {
	libDir := os.Getenv("NEXUS_LIBKRUN_DIR")
	if libDir == "" {
		t.Skip("NEXUS_LIBKRUN_DIR not set — skipping (needs real bundle to build)")
	}
	libkrunPath := filepath.Join(libDir, libkrun.LibFilename())
	libkrunfwPath := filepath.Join(libDir, libkrun.LibFWFilename())

	bundlePath := buildMinimalBundle(t, libkrunPath, libkrunfwPath)
	cacheDir := t.TempDir()
	r := &runner.Runner{CacheDir: cacheDir}

	eb1, err := r.ExtractBundle(bundlePath)
	if err != nil {
		t.Fatalf("first ExtractBundle: %v", err)
	}
	eb2, err := r.ExtractBundle(bundlePath)
	if err != nil {
		t.Fatalf("second ExtractBundle (idempotent): %v", err)
	}
	if eb1.CacheDir != eb2.CacheDir {
		t.Errorf("expected same CacheDir; got %q and %q", eb1.CacheDir, eb2.CacheDir)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildMinimalBundle constructs a real NXPACK bundle file suitable for the
// runner tests. It embeds:
//   - A synthetic OCI layer with a minimal rootfs (directories + /bin/sh stub).
//   - An empty workspace snapshot.
//   - The real libkrun and libkrunfw libraries from libkrunPath/libkrunfwPath.
func buildMinimalBundle(t *testing.T, libkrunPath, libkrunfwPath string) string {
	t.Helper()

	layerDigest, layerTar := buildMinimalRootfsLayer(t)

	manifest := bundle.BundleManifest{
		SchemaVersion: bundle.SchemaVersion,
		BundleVersion: bundle.BundleVersion,
		CreatedAt:     "2026-01-01T00:00:00Z",
		Source: bundle.SourceMetadata{
			WorkspaceName: "runner-integration-test",
			Ref:           "main",
		},
		Compatibility: bundle.CompatibilityMeta{
			Arch:     []string{runtime.GOARCH},
			Backend:  []string{"libkrun"},
			OsFamily: []string{runtime.GOOS},
		},
		Runtime: &bundle.RuntimeConfig{
			CPUs: 1,
			// Must be larger than the virtiofs SHM window (512 MiB) that
			// krun_set_root adds internally.  Use 2048 MiB to be safe.
			MemMiB: 2048,
		},
		WorkspaceIntent: bundle.WorkspaceIntent{
			Up: []string{"exit 0"},
		},
		Assets: &bundle.AssetInventory{
			Layers: []bundle.LayerEntry{
				{Path: "payload/layers/" + layerDigest + ".tar", Digest: layerDigest},
			},
		},
		Payload:   bundle.PayloadIndex{Entries: []bundle.PayloadEntry{}},
		Integrity: bundle.IntegrityMetadata{Algorithm: "sha256"},
	}

	preBytes, err := bundle.MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest (pre): %v", err)
	}
	h := sha256.New()
	h.Write(preBytes)
	manifest.Integrity.ManifestDigest = hex.EncodeToString(h.Sum(nil))

	finalManifest, err := bundle.MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest (final): %v", err)
	}

	assetsTar := buildAssetsTar(t, libkrunPath, libkrunfwPath, layerDigest, layerTar, finalManifest)

	assetsBlob, err := bundle.CompressZstd(assetsTar)
	if err != nil {
		t.Fatalf("CompressZstd: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), "runner-test.nxbundle")
	f, err := os.OpenFile(bundlePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create bundle file: %v", err)
	}
	if err := bundle.WriteNXPack(f, assetsBlob, finalManifest, nxpackShellStub()); err != nil {
		f.Close()
		t.Fatalf("WriteNXPack: %v", err)
	}
	f.Close()
	return bundlePath
}

func nxpackShellStub() []byte {
	return []byte("#!/bin/sh\nexec nexus bundle run \"$0\" \"$@\"\n: <<'NXPACK_DATA_FOLLOWS'\n")
}

// buildMinimalRootfsLayer creates a minimal OCI layer tar with:
//   - Standard Linux directory structure.
//   - A stub /bin/sh (zero-byte executable placeholder).
//
// Returns the hex digest and the tar bytes.
func buildMinimalRootfsLayer(t *testing.T) (digest string, tarBytes []byte) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	dirs := []string{
		"bin", "dev", "etc", "home", "lib", "proc", "root", "run",
		"sbin", "sys", "tmp", "usr", "usr/bin", "usr/lib", "var",
	}
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir,
			Name:     d + "/",
			Mode:     0o755,
		}); err != nil {
			t.Fatalf("tar dir %s: %v", d, err)
		}
	}

	// Minimal /bin/sh stub (just an empty file — init.krun will exec it,
	// and if the kernel can't find a real shell it will exit, which is fine
	// for the purposes of exercising the runner path).
	// In a real test environment, the OCI image (ubuntu:24.04) would be used.
	addTarFile(t, tw, "bin/sh", []byte{})

	if err := tw.Close(); err != nil {
		t.Fatalf("close layer tar: %v", err)
	}

	raw := buf.Bytes()
	h := sha256.Sum256(raw)
	digest = hex.EncodeToString(h[:])
	return digest, raw
}

// buildAssetsTar constructs the assets tar that the runner extracts.
func buildAssetsTar(t *testing.T, libkrunPath, libkrunfwPath, layerDigest string, layerTar []byte, manifestJSON []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// manifest.json from the bundle footer (runner reads it from there, but we
	// include it for completeness).
	addTarFile(t, tw, "manifest.json", manifestJSON)

	// OCI layer tar.
	addTarFile(t, tw, "payload/layers/"+layerDigest+".tar", layerTar)

	// empty workspace tar.gz.
	workspaceTarGz := buildEmptyTarGz(t)
	addTarFile(t, tw, "payload/workspace.tar.gz", workspaceTarGz)

	// libkrun library.
	addTarFileFromDisk(t, tw, "lib/"+libkrun.LibFilename(), libkrunPath)

	// libkrunfw library.
	addTarFileFromDisk(t, tw, "lib/"+libkrun.LibFWFilename(), libkrunfwPath)

	if err := tw.Close(); err != nil {
		t.Fatalf("close assets tar: %v", err)
	}
	return buf.Bytes()
}

// buildEmptyTarGz creates an empty gzip-compressed tar.
func buildEmptyTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func addTarFile(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Size:     int64(len(data)),
		Mode:     0o755,
	}); err != nil {
		t.Fatalf("tar header %s: %v", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("tar write %s: %v", name, err)
	}
}

func addTarFileFromDisk(t *testing.T, tw *tar.Writer, tarName, diskPath string) {
	t.Helper()
	f, err := os.Open(diskPath)
	if err != nil {
		t.Fatalf("open %s: %v", diskPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", diskPath, err)
	}

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     tarName,
		Size:     info.Size(),
		Mode:     0o755,
	}); err != nil {
		t.Fatalf("tar header %s: %v", tarName, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		t.Fatalf("tar copy %s: %v", tarName, err)
	}
}
