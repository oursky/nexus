//go:build darwin

package macvm

import "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/vmrootfs"

// HostBakeStampVersion must match Linux libkrun host-side bake stamps
// (see packages/nexus/internal/infra/runtime/libkrun/bake_version.go).
const HostBakeStampVersion = "v18"

// BakeStampVersion is an alias for HostBakeStampVersion.
const BakeStampVersion = HostBakeStampVersion

// LegacyMacRootFSTag is the legacy GitHub tag used before semver-aligned rootfs assets.
func LegacyMacRootFSTag() string {
	return vmrootfs.LegacyMacOSRootFSTag()
}

// RootFSSHA256 is the expected SHA256 of the **uncompressed** ext4 after download (optional).
const RootFSSHA256 = ""

// LinuxAMD64RootFSSHA256 is optional verification for Linux amd64 guest rootfs URLs.
const LinuxAMD64RootFSSHA256 = ""

// DefaultRootFSURL selects the gzipped darwin/arm64 guest ext4 artifact for download.
func DefaultRootFSURL() string {
	return vmrootfs.MacOSGuestRootFSURL()
}

// DefaultLinuxAMD64RootFSURL is the gzipped linux/amd64 guest ext4 artifact URL.
func DefaultLinuxAMD64RootFSURL() string {
	return vmrootfs.LinuxAMD64GuestRootFSURL()
}
