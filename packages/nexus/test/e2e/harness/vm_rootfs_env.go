//go:build e2e

package harness

import (
	"os"
	"strings"
)

// VMRootfsFromEnv returns NEXUS_VM_ROOTFS, or NEXUS_E2E_ROOTFS when unset.
func VMRootfsFromEnv() string {
	if r := strings.TrimSpace(os.Getenv("NEXUS_VM_ROOTFS")); r != "" {
		return r
	}
	return strings.TrimSpace(os.Getenv("NEXUS_E2E_ROOTFS"))
}
