package hostpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsRemoteGitLocation reports whether s looks like an https/http/git/ssh remote URL
// (not supported for workspace or project repo paths).
func IsRemoteGitLocation(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "git://") || strings.HasPrefix(lower, "ssh://") {
		return true
	}
	if strings.HasPrefix(s, "git@") {
		return true
	}
	return strings.Contains(lower, "://")
}

// ResolveLocalDirOnHost returns path as an absolute directory if it exists on this host.
func ResolveLocalDirOnHost(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if IsRemoteGitLocation(path) {
		return "", fmt.Errorf("remote Git URLs are not supported; use a local directory path")
	}

	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		abs, err := filepath.Abs(clean)
		if err != nil {
			return "", fmt.Errorf("resolve path: %w", err)
		}
		clean = abs
	}

	fi, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("path not found on daemon (use the same absolute path where the project exists on this host): %w", err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a directory: %s", clean)
	}
	return filepath.Clean(clean), nil
}
