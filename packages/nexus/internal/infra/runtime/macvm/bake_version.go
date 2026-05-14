//go:build darwin

package macvm

import "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/vmrootfs"

// BakeStampVersion is the legacy rootfs tag suffix (see vmrootfs.LegacyMacRootFSBakeStamp).
const BakeStampVersion = vmrootfs.LegacyMacRootFSBakeStamp

// RootFSSHA256 is the expected SHA256 of the **uncompressed** ext4 at
// cfg.RootFSCachePath after download+decompress. Empty skips verification.
const RootFSSHA256 = ""

// LinuxARM64RootFSSHA256 is optional verification for Linux arm64 rootfs downloads.
const LinuxARM64RootFSSHA256 = ""

// RootFSVersionLegacy is the legacy GitHub tag (pre-semver rootfs releases).
func RootFSVersionLegacy() string {
	return vmrootfs.LegacyMacOSRootFSTag()
}

// DefaultRootFSURL downloads the macOS libkrun guest rootfs from the current release.
func DefaultRootFSURL() string {
	return vmrootfs.MacOSGuestRootFSURL()
}

// DefaultLinuxARM64RootFSURL is the same guest image layout for Linux arm64 libkrun.
func DefaultLinuxARM64RootFSURL() string {
	return vmrootfs.LinuxARM64GuestRootFSURL()
}
