//go:build e2e

package cli_test

import (
	"os"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

var cliSuite *harness.CLISuite

func TestMain(m *testing.M) {
	cliSuite = harness.NewCLISuite()
	os.Exit(cliSuite.Run(m))
}
