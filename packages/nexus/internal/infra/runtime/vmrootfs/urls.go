// Package vmrootfs holds GitHub Release URLs for pre-built libkrun guest rootfs
// assets (Linux guests: amd64 on Linux hosts, arm64 on Apple Silicon). Works on any GOOS (pure functions).
package vmrootfs

import (
	"fmt"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/buildinfo"
)

// LegacyMacRootFSBakeStamp is the numeric suffix for dev / legacy release tags
// (rootfs-macos-arm64-v{N}) when buildinfo.Version is not a semver release.
const LegacyMacRootFSBakeStamp = 1

// LegacyMacOSRootFSTag returns the legacy GitHub release tag used before rootfs
// artifacts were attached to semantic-release tags.
func LegacyMacOSRootFSTag() string {
	return fmt.Sprintf("rootfs-macos-arm64-v%d", LegacyMacRootFSBakeStamp)
}

// ReleaseTagForRootfsAssets returns the GitHub release tag that holds
// rootfs-*.ext4.gz files. Semver releases use the same tag as Nexus binaries
// (e.g. v1.2.3). Dev builds use LegacyMacOSRootFSTag().
func ReleaseTagForRootfsAssets() string {
	v := strings.TrimSpace(buildinfo.Version)
	if v == "" || v == "dev" {
		return "latest"
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// MacOSGuestRootFSURL is the gzipped ext4 for macOS libkrun VMs.
func MacOSGuestRootFSURL() string {
	return fmt.Sprintf(
		"https://github.com/oursky/nexus/releases/download/%s/rootfs-darwin-arm64.ext4.gz",
		ReleaseTagForRootfsAssets(),
	)
}

// LinuxAMD64GuestRootFSURL is the gzipped ext4 for Linux amd64 libkrun guests.
func LinuxAMD64GuestRootFSURL() string {
	return fmt.Sprintf(
		"https://github.com/oursky/nexus/releases/download/%s/rootfs-linux-amd64.ext4.gz",
		ReleaseTagForRootfsAssets(),
	)
}
