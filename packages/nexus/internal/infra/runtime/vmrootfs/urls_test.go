package vmrootfs

import "testing"

func TestLegacyMacOSRootFSTag(t *testing.T) {
	if got, want := LegacyMacOSRootFSTag(), "rootfs-macos-arm64-v1"; got != want {
		t.Fatalf("LegacyMacOSRootFSTag() = %q; want %q", got, want)
	}
}
