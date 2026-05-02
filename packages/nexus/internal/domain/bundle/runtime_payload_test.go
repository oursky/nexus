package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
)

func TestExtractRuntimeLibs(t *testing.T) {
	tmp := t.TempDir()
	tarball := filepath.Join(tmp, "smolvm.tar.gz")
	libDir := filepath.Join(tmp, "lib")

	buf := bytes.NewBuffer(nil)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)

	files := map[string][]byte{
		"smolvm-x/lib/" + libkrun.LibFilename():   []byte("krun"),
		"smolvm-x/lib/" + libkrun.LibFWFilename(): []byte("fw"),
	}
	for name, data := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write data: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := os.WriteFile(tarball, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}

	if err := extractRuntimeLibs(tarball, libDir); err != nil {
		t.Fatalf("extractRuntimeLibs: %v", err)
	}
	libPath, fwPath := libkrun.LibPaths(libDir)
	if _, err := os.Stat(libPath); err != nil {
		t.Fatalf("lib missing: %v", err)
	}
	if _, err := os.Stat(fwPath); err != nil {
		t.Fatalf("fw missing: %v", err)
	}
}

func TestEnsureRuntimeTarball_DownloadAndChecksum(t *testing.T) {
	payload := []byte("runtime payload bytes")
	h := sha256.Sum256(payload)
	sha := hex.EncodeToString(h[:])

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	tmp := t.TempDir()
	tarballPath := filepath.Join(tmp, "dist.tar.gz")
	spec := runtimePayloadSpec{
		Platform: "darwin-arm64",
		URL:      ts.URL,
		SHA256:   sha,
	}

	if err := ensureRuntimeTarball(context.Background(), tarballPath, spec); err != nil {
		t.Fatalf("ensureRuntimeTarball: %v", err)
	}
	if ok, err := fileMatchesSHA256(tarballPath, sha); err != nil || !ok {
		t.Fatalf("tarball checksum invalid: ok=%v err=%v", ok, err)
	}
}
