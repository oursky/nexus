//go:build linux

//nolint:unused
package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

func httpDownload(url string) ([]byte, error) {
	return httpDownloadWithTimeout(url, 10*time.Minute)
}

func httpDownloadWithTimeout(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func httpDownloadWithRetry(url string, attempts int, timeout time.Duration) ([]byte, error) {
	if attempts < 1 {
		attempts = 1
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		data, err := httpDownloadWithTimeout(url, timeout)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if attempt < attempts {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
	}
	return nil, fmt.Errorf("download %s failed after %d attempts: %w", url, attempts, lastErr)
}
