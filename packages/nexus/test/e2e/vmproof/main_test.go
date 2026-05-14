//go:build e2e

package vmproof_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

var cliSuite *harness.CLISuite

func TestMain(m *testing.M) {
	if runtime.GOOS == "darwin" {
		// vmproof assumes Linux libkrun + passt + compose-in-VM; macOS VM E2E uses workspace/cli shards.
		os.Exit(0)
	}
	cliSuite = harness.NewCLISuite()
	os.Exit(cliSuite.Run(m))
}
