//go:build linux

//nolint:unused
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ── Download URLs ─────────────────────────────────────────────────────────────

const (
	passtVersion = "20250501.0"
)

func vmSquashfsURL() string {
	switch runtime.GOARCH {
	case "arm64":
		return "https://cloud-images.ubuntu.com/minimal/releases/resolute/release/ubuntu-26.04-minimal-cloudimg-arm64-root.tar.xz"
	default:
		return "https://cloud-images.ubuntu.com/minimal/releases/resolute/release/ubuntu-26.04-minimal-cloudimg-amd64-root.tar.xz"
	}
}

// vmRootfsIsSquashfs reports whether the cached rootfs archive is squashfs
// (legacy squashfs CI format) rather than a tar.xz (Ubuntu CDN format).
func vmRootfsIsSquashfs(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := f.Read(magic[:]); err != nil {
		return false
	}
	// squashfs magic: 'sqsh' (0x73717368) or 'hsqs' (0x68737173)
	return magic == [4]byte{0x73, 0x71, 0x73, 0x68} || magic == [4]byte{0x68, 0x73, 0x71, 0x73}
}

func passtDownloadURL() string {
	// passt static binary from upstream builds.
	// NOTE: passt.top only provides x86_64 builds. aarch64 must be installed
	// via the system package manager (e.g. apt install passt).
	switch runtime.GOARCH {
	case "arm64":
		return ""
	default:
		return "https://passt.top/builds/latest/x86_64/passt"
	}
}

func downloadAndExtractTarGz(url, innerPath string) ([]byte, error) {
	data, err := httpDownload(url)
	if err != nil {
		return nil, err
	}

	gzReader, err := gzip.NewReader(
		func() io.Reader {
			return &readerBytes{data: data}
		}(),
	)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	defer gzReader.Close()

	tr := tar.NewReader(gzReader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		if header.Name == innerPath || strings.HasSuffix(header.Name, "/"+filepath.Base(innerPath)) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive from %s", innerPath, url)
}

type readerBytes struct {
	data   []byte
	offset int
}

func (r *readerBytes) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
