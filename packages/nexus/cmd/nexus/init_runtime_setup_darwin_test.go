//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinBootstrapIsNoOpForNonLibkrunRuntime(t *testing.T) {
	err := runInitRuntimeBootstrapDarwin(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("expected no error for local runtime, got: %v", err)
	}
}

func TestDarwinBootstrapWritesSeatbeltForLibkrunRuntime(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".nexus"), 0o755); err != nil {
		t.Fatalf("create .nexus: %v", err)
	}

	if err := runInitRuntimeBootstrapDarwin(projectRoot, "libkrun"); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(projectRoot, ".nexus", "run", "nexus-init-env"))
	if err != nil {
		t.Fatalf("read nexus-init-env: %v", err)
	}
	if !strings.Contains(string(data), "NEXUS_RUNTIME_BACKEND=seatbelt") {
		t.Fatalf("expected seatbelt backend hint, got: %q", string(data))
	}
}
