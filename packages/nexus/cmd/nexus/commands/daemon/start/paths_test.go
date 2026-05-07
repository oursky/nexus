package start

import "testing"

func TestXDGVMAsset_ReturnsXDGScopedPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/nexus-home")

	if got := XDGVMAsset("rootfs.ext4"); got != "/tmp/nexus-home/.local/share/nexus/vm/rootfs.ext4" {
		t.Fatalf("XDGVMAsset = %q", got)
	}
}
