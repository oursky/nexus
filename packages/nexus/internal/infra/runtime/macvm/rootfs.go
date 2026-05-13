//go:build darwin

package macvm

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// defaultRootFSURL is the download URL for the pre-baked macOS VM rootfs.
// This image has the nexus-guest-agent pre-installed and all base tools baked in.
// The URL follows the same pattern as the Linux libkrun rootfs delivery.
const defaultRootFSURL = "https://dl.nexus.oursky.com/vm/rootfs-macos-arm64-v1.ext4.gz"

// ensureRootFS downloads the base rootfs if it does not already exist.
func ensureRootFS(ctx context.Context, cfg ManagerConfig) error {
	if _, err := os.Stat(cfg.RootFSCachePath); err == nil {
		return nil
	}
	urlStr := cfg.RootFSDownloadURL
	if urlStr == "" {
		urlStr = defaultRootFSURL
	}
	log.Printf("macvm: downloading base rootfs from %s → %s", urlStr, cfg.RootFSCachePath)
	if err := os.MkdirAll(filepath.Dir(cfg.RootFSCachePath), 0o755); err != nil {
		return fmt.Errorf("create rootfs dir: %w", err)
	}
	return downloadFile(ctx, urlStr, cfg.RootFSCachePath)
}

func downloadFile(ctx context.Context, urlStr, dst string) error {
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
