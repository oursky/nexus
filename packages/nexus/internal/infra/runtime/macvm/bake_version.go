//go:build darwin

package macvm

import "fmt"

// BakeStampVersion is the version of the macOS VM rootfs pre-baked image.
// Bump this when tools installed in the rootfs change.
// Must match the tag used when uploading to GitHub Releases:
//
//	github.com/oursky/nexus/releases/download/rootfs-macos-arm64-v{N}/rootfs-macos-arm64.ext4.gz
const BakeStampVersion = 1

// RootFSSHA256 is the expected SHA256 of the compressed rootfs asset
// (rootfs-macos-arm64.ext4.gz) for BakeStampVersion.
// Set to empty string to skip the integrity check (safe for development).
// Fill in after the first real asset is built by the CI workflow.
const RootFSSHA256 = ""

// RootFSVersion returns the GitHub release tag for the current macOS rootfs version.
func RootFSVersion() string {
	return fmt.Sprintf("rootfs-macos-arm64-v%d", BakeStampVersion)
}

// DefaultRootFSURL is the download URL for the pre-baked macOS VM rootfs.
// Hosted as a GitHub Release asset on oursky/nexus.
func DefaultRootFSURL() string {
	return fmt.Sprintf(
		"https://github.com/oursky/nexus/releases/download/%s/rootfs-macos-arm64.ext4.gz",
		RootFSVersion(),
	)
}
