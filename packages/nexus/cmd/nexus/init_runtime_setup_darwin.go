//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var initRuntimeBootstrapRunner func(projectRoot, runtimeName string) error = runInitRuntimeBootstrapDarwin

var (
	initRuntimeBootstrapIsRootFn                   = func() bool { return os.Geteuid() == 0 }
	initRuntimeBootstrapSudoOKFn                   = func() bool { return false }
	initRuntimeBootstrapIsTTYFn                    = isTerminalDarwin
	initRuntimeBootstrapSkipFastFailFn func() bool = nil
)

// runInitRuntimeBootstrapDarwin handles darwin init bootstrap.
// libkrun is Linux-only; on darwin we write seatbelt as the runtime backend
// hint so nexus exec falls back to the seatbelt sandbox.
func runInitRuntimeBootstrapDarwin(projectRoot, runtimeName string) error {
	if runtimeName != "libkrun" {
		return nil
	}
	_ = writeNexusInitEnv(projectRoot, map[string]string{
		"NEXUS_RUNTIME_BACKEND": "seatbelt",
	})
	return nil
}

func writeNexusInitEnv(projectRoot string, kvPairs map[string]string) error {
	runDir := filepath.Join(projectRoot, ".nexus", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create nexus run dir: %w", err)
	}
	envPath := filepath.Join(runDir, "nexus-init-env")
	var sb strings.Builder
	for k, v := range kvPairs {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(v)
		sb.WriteString("\n")
	}
	if err := os.WriteFile(envPath, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write nexus-init-env: %w", err)
	}
	return nil
}

func isTerminalDarwin(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
