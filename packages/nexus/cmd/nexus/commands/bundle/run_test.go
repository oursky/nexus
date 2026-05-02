package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRunScript(t *testing.T) {
	tmpDir := t.TempDir()
	cmds := []string{
		ensureDockerDaemonCmd(),
		"DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 docker compose up",
	}

	guestPath, err := writeRunScript(tmpDir, cmds)
	if err != nil {
		t.Fatalf("writeRunScript: %v", err)
	}
	if want := "/workspace/.nexus-run.sh"; guestPath != want {
		t.Fatalf("guestPath = %q, want %q", guestPath, want)
	}

	hostPath := filepath.Join(tmpDir, ".nexus-run.sh")
	data, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := string(data)
	if !strings.HasPrefix(script, "#!/bin/sh\nset -e\n") {
		t.Fatalf("script missing shebang or set -e: %q", script)
	}
	if !strings.Contains(script, ensureDockerDaemonCmd()) {
		t.Fatalf("script missing docker daemon cmd")
	}
	if !strings.Contains(script, "docker compose up") {
		t.Fatalf("script missing up command")
	}
}

func TestEnsureDockerDaemonCmd(t *testing.T) {
	cmd := ensureDockerDaemonCmd()
	if cmd == "" {
		t.Fatal("ensureDockerDaemonCmd returned empty string")
	}
	if !strings.Contains(cmd, "dockerd") {
		t.Fatal("dockerd not found in ensureDockerDaemonCmd")
	}
	if !strings.Contains(cmd, "docker info") {
		t.Fatal("docker info check not found in ensureDockerDaemonCmd")
	}
}
