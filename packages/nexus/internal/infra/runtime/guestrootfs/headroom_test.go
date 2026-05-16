package guestrootfs

import (
	"os"
	"strconv"
	"testing"
)

func TestOperationalTargetBytes_default(t *testing.T) {
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_BYTES", "")
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_GIB", "")
	if got := OperationalTargetBytes(); got != DefaultOperationalBytes {
		t.Fatalf("OperationalTargetBytes() = %d; want %d", got, DefaultOperationalBytes)
	}
}

func TestOperationalTargetBytes_envBytes(t *testing.T) {
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_BYTES", "2147483648")
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_GIB", "")
	if got := OperationalTargetBytes(); got != 2147483648 {
		t.Fatalf("OperationalTargetBytes() = %d; want 2147483648", got)
	}
}

func TestOperationalTargetBytes_envGiB(t *testing.T) {
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_BYTES", "")
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_GIB", "12")
	if got := OperationalTargetBytes(); got != 12<<30 {
		t.Fatalf("OperationalTargetBytes() = %d; want %d", got, 12<<30)
	}
}

func TestOperationalTargetBytes_invalidFallsBack(t *testing.T) {
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_BYTES", "not-a-number")
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_GIB", "")
	if got := OperationalTargetBytes(); got != DefaultOperationalBytes {
		t.Fatalf("OperationalTargetBytes() = %d; want default %d", got, DefaultOperationalBytes)
	}
}

func TestOperationalTargetBytes_bytesPriorityOverGiB(t *testing.T) {
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_BYTES", strconv.FormatInt(3<<30, 10))
	t.Setenv("NEXUS_VM_ROOTFS_OPERATIONAL_GIB", "99")
	if got := OperationalTargetBytes(); got != 3<<30 {
		t.Fatalf("OperationalTargetBytes() = %d; want %d", got, 3<<30)
	}
}

func TestEnsureOperationalHeadroom_missingFile(t *testing.T) {
	err := EnsureOperationalHeadroom("/nonexistent/nexus-rootfs-test.ext4")
	if err == nil {
		t.Fatal("expected error")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist, got %v", err)
	}
}
