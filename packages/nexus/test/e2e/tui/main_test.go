//go:build e2e

package tui_test

import (
	"fmt"
	"os"
	"testing"
)

var shared *daemonEnv

func TestMain(m *testing.M) {
	if err := prepLinuxEmbed(); err != nil {
		fmt.Fprintf(os.Stderr, "tui e2e embed prep: %v\n", err)
		os.Exit(1)
	}
	bin, err := resolveNexusBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui e2e build: %v\n", err)
		os.Exit(1)
	}
	env, err := startDaemon(bin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui e2e daemon: %v\n", err)
		os.Exit(1)
	}
	shared = env
	code := m.Run()
	shared.Close()
	os.Exit(code)
}
