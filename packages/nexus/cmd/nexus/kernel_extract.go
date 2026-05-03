package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// extractEmbeddedKernel extracts the embedded kernel (vmlinux on Linux, Image on
// macOS) to the standard cache location if it doesn't already exist there.
// It returns the path to the extracted kernel, or empty string if there is no
// embedded kernel for this platform.
func extractEmbeddedKernel() (string, error) {
	if len(embeddedKernel) == 0 {
		return "", nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	var dest string
	if _, err := os.Stat(filepath.Join(home, ".cache", "nexus", "kernels", "Image-custom")); err == nil {
		// Already have an arm64 custom kernel.
		dest = filepath.Join(home, ".cache", "nexus", "kernels", "Image-custom")
	} else if _, err := os.Stat(filepath.Join(home, ".cache", "nexus", "kernels", "vmlinux-custom")); err == nil {
		// Already have an x86_64 custom kernel.
		dest = filepath.Join(home, ".cache", "nexus", "kernels", "vmlinux-custom")
	} else {
		// No custom kernel present — extract the embedded one.
		if embeddedKernel[0] == 0x7f && embeddedKernel[1] == 'E' && embeddedKernel[2] == 'L' && embeddedKernel[3] == 'F' {
			// ELF vmlinux (Linux x86_64)
			dest = filepath.Join(home, ".cache", "nexus", "kernels", "vmlinux-custom")
		} else {
			// Raw Image (macOS arm64 or Linux arm64)
			dest = filepath.Join(home, ".cache", "nexus", "kernels", "Image-custom")
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", fmt.Errorf("extract kernel: mkdir: %w", err)
		}
		if err := os.WriteFile(dest, embeddedKernel, 0o644); err != nil {
			return "", fmt.Errorf("extract kernel: write: %w", err)
		}
	}

	return dest, nil
}
