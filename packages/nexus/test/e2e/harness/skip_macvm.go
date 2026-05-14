//go:build e2e

package harness

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// SkipIfE2EMacVM skips tests that are not yet validated (or not supported) on
// macOS when NEXUS_E2E_DRIVER=vm (fork/snapshot-heavy flows, vmproof, etc.).
func SkipIfE2EMacVM(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" && strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_E2E_DRIVER")), "vm") {
		t.Skip("skipped on macOS E2E VM driver (fork/snapshot parity not yet covered)")
	}
}
