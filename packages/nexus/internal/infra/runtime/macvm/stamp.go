//go:build darwin

package macvm

import (
	"os"
	"path/filepath"
)

// bakeStampName is the stamp file checked at daemon start / bake time.
const bakeStampName = "rootfs-baked-" + HostBakeStampVersion

// ReadBakeStamp returns the current bake stamp content or "" if not present.
func ReadBakeStamp(stampDir string) string {
	data, err := os.ReadFile(filepath.Join(stampDir, bakeStampName))
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteBakeStamp writes the bake stamp file.
func WriteBakeStamp(stampDir string) error {
	return os.WriteFile(filepath.Join(stampDir, bakeStampName), []byte(HostBakeStampVersion), 0o644)
}

// DeleteBakeStamp removes the bake stamp file.
func DeleteBakeStamp(stampDir string) {
	_ = os.Remove(filepath.Join(stampDir, bakeStampName))
}

// IsBakeStale returns true if the bake stamp file is missing or version
// doesn't match HostBakeStampVersion.
func IsBakeStale(stampDir string) bool {
	return ReadBakeStamp(stampDir) != HostBakeStampVersion
}
