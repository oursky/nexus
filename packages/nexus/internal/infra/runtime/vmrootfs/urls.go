// Package vmrootfs holds GitHub Release URLs for pre-built libkrun guest rootfs
// assets (Linux guests: amd64 on Linux hosts, arm64 on Apple Silicon). Works on any GOOS (pure functions).
//
// Release pipelines publish shrunk ext4 images compressed with **zstd** (primary) and **gzip**
// (fallback). Hosts restore operational disk headroom after download (install.sh / guestrootfs).
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
// rootfs artifacts. Semver releases use the same tag as Nexus binaries
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

func releaseDownloadBase() string {
	return fmt.Sprintf(
		"https://github.com/oursky/nexus/releases/download/%s/",
		ReleaseTagForRootfsAssets(),
	)
}

// MacOSGuestRootFSDownloadCandidates returns preferred darwin/arm64 rootfs URLs (zstd first, gzip fallback).
func MacOSGuestRootFSDownloadCandidates() []string {
	b := releaseDownloadBase()
	return []string{
		b + "rootfs-darwin-arm64.ext4.zst",
		b + "rootfs-darwin-arm64.ext4.gz",
	}
}

// LinuxAMD64GuestRootFSDownloadCandidates returns preferred linux/amd64 host VM rootfs URLs (zstd first, gzip fallback).
func LinuxAMD64GuestRootFSDownloadCandidates() []string {
	b := releaseDownloadBase()
	return []string{
		b + "rootfs-linux-amd64.ext4.zst",
		b + "rootfs-linux-amd64.ext4.gz",
	}
}

// MacOSGuestRootFSURL is the preferred (zstd) darwin/arm64 guest rootfs URL.
func MacOSGuestRootFSURL() string {
	return MacOSGuestRootFSDownloadCandidates()[0]
}

// LinuxAMD64GuestRootFSURL is the preferred (zstd) linux/amd64 host rootfs URL.
func LinuxAMD64GuestRootFSURL() string {
	return LinuxAMD64GuestRootFSDownloadCandidates()[0]
}
