//go:build darwin

package macvm

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/guestrootfs"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/vmrootfs"
)

// EnsureRootFS downloads the base rootfs if it does not already exist at
// cfg.RootFSCachePath.  If cfg.RootFSDownloadURL is empty, tries zstd then gzip
// release URLs. After a successful download the file is verified against
// RootFSSHA256 when that constant is non-empty.
func EnsureRootFS(ctx context.Context, cfg ManagerConfig) error {
	cfg = applyConfigDefaults(cfg)
	if _, err := os.Stat(cfg.RootFSCachePath); err == nil {
		return guestrootfs.EnsureOperationalHeadroom(cfg.RootFSCachePath)
	}

	urls := []string{}
	if strings.TrimSpace(cfg.RootFSDownloadURL) != "" {
		urls = append(urls, strings.TrimSpace(cfg.RootFSDownloadURL))
	} else {
		urls = vmrootfs.MacOSGuestRootFSDownloadCandidates()
	}

	if err := os.MkdirAll(filepath.Dir(cfg.RootFSCachePath), 0o755); err != nil {
		return fmt.Errorf("create rootfs dir: %w", err)
	}

	var lastErr error
	for _, urlStr := range urls {
		log.Printf("macvm: downloading base rootfs %s → %s", urlStr, cfg.RootFSCachePath)
		lastErr = downloadAndDecompress(ctx, urlStr, cfg.RootFSCachePath)
		if lastErr == nil {
			break
		}
		log.Printf("macvm: rootfs download failed (%v); trying next URL if any", lastErr)
		_ = os.Remove(cfg.RootFSCachePath)
		_ = os.Remove(cfg.RootFSCachePath + ".tmp")
	}
	if lastErr != nil {
		return fmt.Errorf("rootfs download failed (tried %d URL(s)): %w", len(urls), lastErr)
	}

	if RootFSSHA256 != "" {
		if err := verifySHA256(cfg.RootFSCachePath, RootFSSHA256); err != nil {
			_ = os.Remove(cfg.RootFSCachePath)
			return fmt.Errorf("rootfs SHA256 mismatch: %w", err)
		}
		log.Printf("macvm: rootfs SHA256 OK")
	}
	if err := guestrootfs.EnsureOperationalHeadroom(cfg.RootFSCachePath); err != nil {
		return fmt.Errorf("guest rootfs operational headroom: %w", err)
	}
	return nil
}

// downloadAndDecompress fetches urlStr and writes the result to dst.
// Supports .zst (zstd), .gz (gzip), or raw body. Uses a temp file so partial
// downloads never corrupt dst.
func downloadAndDecompress(ctx context.Context, urlStr, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, urlStr)
	}

	pathLower := ""
	if u, perr := url.Parse(urlStr); perr == nil {
		pathLower = strings.ToLower(u.Path)
	}

	var r io.Reader = resp.Body

	switch {
	case strings.HasSuffix(pathLower, ".zst"):
		dec, zerr := zstd.NewReader(resp.Body)
		if zerr != nil {
			return zerr
		}
		defer dec.Close()
		r = dec
	case strings.HasSuffix(pathLower, ".gz"):
		gr, gerr := gzip.NewReader(resp.Body)
		if gerr != nil {
			return gerr
		}
		defer gr.Close()
		r = gr
	}

	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// verifySHA256 computes the SHA256 of the file at path and returns an error if
// it does not match expected (hex-encoded, case-insensitive).
func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("got %s, want %s", got, expected)
	}
	return nil
}
