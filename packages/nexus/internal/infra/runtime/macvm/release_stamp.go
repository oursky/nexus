//go:build darwin

package macvm

import (
	"os"
	"path/filepath"
	"strings"
)

const rootfsReleaseStampName = "rootfs-release"

// ReadRootFSReleaseStamp returns the stored release tag or "".
func ReadRootFSReleaseStamp(stampDir string) string {
	data, err := os.ReadFile(filepath.Join(stampDir, rootfsReleaseStampName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteRootFSReleaseStamp writes the release tag.
func WriteRootFSReleaseStamp(stampDir, releaseTag string) error {
	return os.WriteFile(filepath.Join(stampDir, rootfsReleaseStampName), []byte(releaseTag), 0o644)
}

// IsRootFSReleaseStale returns true if the stored release tag doesn't match
// the expected release.
func IsRootFSReleaseStale(stampDir, expectedRelease string) bool {
	return ReadRootFSReleaseStamp(stampDir) != expectedRelease
}
