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
)

// EnsureRootFS downloads the base rootfs if it does not already exist at
// cfg.RootFSCachePath.  If cfg.RootFSDownloadURL is empty, DefaultRootFSURL()
// is used.  After a successful download the file is verified against
// RootFSSHA256 when that constant is non-empty.
func EnsureRootFS(ctx context.Context, cfg ManagerConfig) error {
	cfg = applyConfigDefaults(cfg)
	if _, err := os.Stat(cfg.RootFSCachePath); err == nil {
		return nil
	}
	urlStr := cfg.RootFSDownloadURL
	if urlStr == "" {
		urlStr = DefaultRootFSURL()
	}
	log.Printf("macvm: downloading base rootfs %s → %s", urlStr, cfg.RootFSCachePath)
	if err := os.MkdirAll(filepath.Dir(cfg.RootFSCachePath), 0o755); err != nil {
		return fmt.Errorf("create rootfs dir: %w", err)
	}
	if err := downloadAndDecompress(ctx, urlStr, cfg.RootFSCachePath); err != nil {
		return err
	}
	// Verify SHA256 against the compressed asset when the expected hash is known.
	if RootFSSHA256 != "" {
		if err := verifySHA256(cfg.RootFSCachePath, RootFSSHA256); err != nil {
			_ = os.Remove(cfg.RootFSCachePath)
			return fmt.Errorf("rootfs SHA256 mismatch: %w", err)
		}
		log.Printf("macvm: rootfs SHA256 OK")
	}
	return nil
}

// downloadAndDecompress fetches urlStr and writes the result to dst.
// If the URL path ends with ".gz" the response body is gunzipped on the fly;
// otherwise it is written verbatim.  A temporary file is used so that a
// partial download never leaves a corrupt file at dst.
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

	body := io.Reader(resp.Body)
	if u, perr := url.Parse(urlStr); perr == nil && strings.HasSuffix(strings.ToLower(u.Path), ".gz") {
		gr, zerr := gzip.NewReader(resp.Body)
		if zerr != nil {
			return zerr
		}
		defer gr.Close()
		body = gr
	}

	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	if _, err := io.Copy(f, body); err != nil {
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
