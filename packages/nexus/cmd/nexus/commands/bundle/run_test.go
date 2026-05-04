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

func TestSplitComposeBuildFromUp(t *testing.T) {
	bake, up := splitComposeBuildFromUp([]string{
		"DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 docker compose build --parallel",
		"DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 docker compose up",
		"echo ready",
	})

	if len(bake) != 1 {
		t.Fatalf("expected 1 bake command, got %d", len(bake))
	}
	if !strings.Contains(bake[0], "docker compose build") {
		t.Fatalf("expected compose build in bake, got %q", bake[0])
	}
	if strings.Contains(bake[0], "DOCKER_BUILDKIT=0") || strings.Contains(bake[0], "COMPOSE_DOCKER_CLI_BUILD=0") {
		t.Fatalf("bake compose build should not force legacy compose env vars: %q", bake[0])
	}
	if !strings.HasPrefix(bake[0], "COMPOSE_BAKE=false ") {
		t.Fatalf("bake compose build should set COMPOSE_BAKE=false: %q", bake[0])
	}
	if len(up) != 2 {
		t.Fatalf("expected 2 up commands, got %d", len(up))
	}
	if strings.Contains(strings.Join(up, "\n"), "docker compose build") {
		t.Fatalf("up commands should not include compose build: %v", up)
	}
}

func TestBuildBakeScriptStartsDockerForComposeBuild(t *testing.T) {
	script := buildBakeScript([]string{
		"DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 docker compose build --parallel",
	}, "/workspace/.nexus-baked")

	if !strings.Contains(script, "docker info >/dev/null 2>&1") {
		t.Fatalf("compose build in bake should ensure docker daemon is running: %q", script)
	}
	if !strings.Contains(script, "docker compose build --parallel") {
		t.Fatalf("expected compose build command in bake script: %q", script)
	}
	if !strings.Contains(script, "COMPOSE_BAKE=false") {
		t.Fatalf("expected COMPOSE_BAKE=false to avoid compose bake mode: %q", script)
	}
}

func TestNormalizeComposeBuildForBake(t *testing.T) {
	got := normalizeComposeBuildForBake("DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 docker compose build --parallel")
	if got != "COMPOSE_BAKE=false docker compose build --parallel" {
		t.Fatalf("unexpected normalized compose build: %q", got)
	}
}

func TestNormalizeRuntimeUpCommands(t *testing.T) {
	got := normalizeRuntimeUpCommands([]string{
		"docker compose up -d",
		"docker compose up --detach --build api",
		"echo ready",
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 runtime commands, got %d", len(got))
	}
	if got[0] != "docker compose up" {
		t.Fatalf("expected detached flag removed from simple compose up, got %q", got[0])
	}
	if got[1] != "docker compose up --build api" {
		t.Fatalf("expected --detach removed while preserving other args, got %q", got[1])
	}
	if got[2] != "echo ready" {
		t.Fatalf("non compose command should remain unchanged, got %q", got[2])
	}
}

func TestNormalizeComposeUpForegroundKeepsComplexShell(t *testing.T) {
	cmd := "docker compose up -d && docker compose ps"
	if got := normalizeComposeUpForeground(cmd); got != cmd {
		t.Fatalf("complex shell command should stay unchanged, got %q", got)
	}
}
