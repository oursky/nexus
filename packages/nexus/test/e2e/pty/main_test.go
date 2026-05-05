//go:build e2e

package pty_test

import (
	"os"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

var suite *harness.Suite

func TestMain(m *testing.M) {
	suite = harness.NewSuite()
	os.Exit(suite.Run(m))
}
