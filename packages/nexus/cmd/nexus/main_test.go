package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeSocketFileInfo struct {
	name string
}

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("linux-only test")
	}
}

func addFakeSGToPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sg")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake sg: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (f fakeSocketFileInfo) Name() string       { return f.name }
func (f fakeSocketFileInfo) Size() int64        { return 0 }
func (f fakeSocketFileInfo) Mode() fs.FileMode  { return os.ModeSocket | 0o666 }
func (f fakeSocketFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeSocketFileInfo) IsDir() bool        { return false }
func (f fakeSocketFileInfo) Sys() any           { return nil }

func TestDetectHostDockerSocketPrefersSnapHostfsSocket(t *testing.T) {
	originalStat := hostDockerSocketStat
	t.Cleanup(func() {
		hostDockerSocketStat = originalStat
	})

	t.Setenv("DOCKER_HOST", "")

	hostDockerSocketStat = func(path string) (os.FileInfo, error) {
		switch path {
		case "/var/lib/snapd/hostfs/var/run/docker.sock", "/var/run/docker.sock":
			return fakeSocketFileInfo{name: filepath.Base(path)}, nil
		default:
			return nil, os.ErrNotExist
		}
	}

	got := detectHostDockerSocket()
	if got != "/var/lib/snapd/hostfs/var/run/docker.sock" {
		t.Fatalf("expected hostfs docker socket, got %q", got)
	}
}

func TestEnsureDotEnvCopiesFromExample(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureDotEnv(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FOO=bar\n" {
		t.Fatalf("unexpected .env content: %q", string(data))
	}
}

func TestFormatCommand(t *testing.T) {
	actual := formatCommand("bash", []string{"-lc", "echo hi"})
	expected := "bash -lc \"echo hi\""
	if actual != expected {
		t.Fatalf("expected %q, got %q", expected, actual)
	}
}

func TestRunCheckCommandCapturesOutput(t *testing.T) {
	output, err := runCheckCommandWithExecContext(context.Background(), t.TempDir(), "probe", "example", 1, 1, 30*time.Second, "bash", []string{"-lc", "printf 'hello world'"}, doctorExecContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "hello world" {
		t.Fatalf("expected captured output, got %q", output)
	}
}

func TestResolveCheckCommandNonLibkrunFallsBackToHost(t *testing.T) {
	cmd, args, env, label := resolveCheckCommand("/tmp/project", "bash", []string{"-lc", "echo ok"}, doctorExecContext{
		backend: "host",
	})

	if cmd != "bash" {
		t.Fatalf("expected bash command, got %q", cmd)
	}
	if label != "host" {
		t.Fatalf("expected label host, got %q", label)
	}
	if len(env) != 0 {
		t.Fatalf("expected no extra env, got %v", env)
	}
	if len(args) != 2 {
		t.Fatalf("expected original args, got %v", args)
	}
}

func TestResolveCheckCommandLibkrunUsesHost(t *testing.T) {
	cmd, args, env, label := resolveCheckCommand("/tmp/project", "bash", []string{"-lc", "echo ok"}, doctorExecContext{
		backend: "libkrun",
	})

	if cmd != "bash" {
		t.Fatalf("expected bash command, got %q", cmd)
	}
	if label != "host" {
		t.Fatalf("expected label host, got %q", label)
	}
	if len(env) != 0 {
		t.Fatalf("expected no extra env, got %v", env)
	}
	if len(args) != 2 {
		t.Fatalf("expected original args, got %v", args)
	}
}

func TestResolveCheckCommandDindNoLongerSupported(t *testing.T) {
	cmd, args, env, label := resolveCheckCommand("/tmp/project", "docker", []string{"compose", "ps"}, doctorExecContext{
		backend: "dind",
	})

	if cmd != "docker" {
		t.Fatalf("expected docker command (dind no longer supported), got %q", cmd)
	}
	if label != "host" {
		t.Fatalf("expected label host, got %q", label)
	}
	if !strings.Contains(strings.Join(args, " "), "compose ps") {
		t.Fatalf("unexpected args: %v", args)
	}
	if len(env) != 0 {
		t.Fatalf("expected empty env, got %v", env)
	}
}

func TestResolveCheckCommandDindWithoutDockerHostNoLongerSupported(t *testing.T) {
	cmd, args, env, label := resolveCheckCommand("/tmp/project", "docker", []string{"compose", "ps"}, doctorExecContext{
		backend: "dind",
	})

	if cmd != "docker" {
		t.Fatalf("expected docker command (dind no longer supported), got %q", cmd)
	}
	if label != "host" {
		t.Fatalf("expected label host, got %q", label)
	}
	if !strings.Contains(strings.Join(args, " "), "compose ps") {
		t.Fatalf("unexpected args: %v", args)
	}
	if len(env) != 0 {
		t.Fatalf("expected empty env, got %v", env)
	}
}

func TestRunCheckCommandWithExecContextHostExportsUIDAndGID(t *testing.T) {
	out, err := runCheckCommandWithExecContext(
		context.Background(),
		t.TempDir(),
		"probe",
		"uid-gid-export",
		1,
		1,
		30*time.Second,
		"bash",
		[]string{"-lc", "env | grep -E '^(UID|GID)=' | sort"},
		doctorExecContext{backend: "host"},
	)
	if err != nil {
		t.Fatalf("expected env probe to succeed, got: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, fmt.Sprintf("UID=%d", os.Getuid())) {
		t.Fatalf("expected UID export in command env, got %q", out)
	}
	if !strings.Contains(out, fmt.Sprintf("GID=%d", os.Getgid())) {
		t.Fatalf("expected GID export in command env, got %q", out)
	}
}

func TestRunExecHostBackendNoLongerSupported(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "host")

	root := t.TempDir()
	err := runExec(execOptions{
		projectRoot: root,
		timeout:     15 * time.Second,
		command:     "bash",
		args:        []string{"-lc", "printf exec-ok"},
	})
	if err == nil {
		t.Fatal("expected error when NEXUS_RUNTIME_BACKEND=host")
	}
	if !strings.Contains(err.Error(), "unsupported runtime backend") {
		t.Fatalf("expected unsupported backend error, got: %v", err)
	}
}

func TestRunExecLibkrunUsesHostRunner(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	root := t.TempDir()

	err := runExec(execOptions{
		projectRoot: root,
		timeout:     15 * time.Second,
		command:     "pwd",
		args:        nil,
	})
	if err != nil {
		t.Fatalf("expected runExec to succeed, got %v", err)
	}
}

func TestRunExecSelectsLibkrunFromWorkspaceWhenEnvUnsetOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("default backend selection for linux is libkrun")
	}
	t.Setenv("NEXUS_RUNTIME_BACKEND", "")

	root := t.TempDir()

	originalRunner := execCheckCommandRunner
	t.Cleanup(func() { execCheckCommandRunner = originalRunner })

	called := false
	execCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		called = true
		if execCtx.backend != "libkrun" {
			t.Fatalf("expected libkrun backend from default selection, got %q", execCtx.backend)
		}
		if projectRoot != root {
			t.Fatalf("expected project root %q, got %q", root, projectRoot)
		}
		return root + "\n", nil
	}

	err := runExec(execOptions{
		projectRoot: root,
		timeout:     15 * time.Second,
		command:     "pwd",
	})
	if err != nil {
		t.Fatalf("expected runExec to succeed, got %v", err)
	}
	if !called {
		t.Fatal("expected exec runner to be called")
	}
}

func TestSelectRuntimeBackendLinuxRequirementMapsToSeatbeltOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific default")
	}
	if got := selectRuntimeBackend([]string{"linux"}); got != "seatbelt" {
		t.Fatalf("expected linux->seatbelt on darwin, got %q", got)
	}
}

func TestRunExecReexecsWithSGKVMOnKVMPermissionError(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	t.Setenv(execKVMGroupReexecEnv, "")
	addFakeSGToPath(t)

	originalBootstrap := execCommandBootstrapRunner
	originalReexec := execKVMGroupReexecRunner
	t.Cleanup(func() {
		execCommandBootstrapRunner = originalBootstrap
		execKVMGroupReexecRunner = originalReexec
	})

	execCommandBootstrapRunner = func(projectRoot string) error {
		return errors.New("libkrun requires read/write access to /dev/kvm")
	}
	called := false
	var gotArgs []string
	execKVMGroupReexecRunner = func(commandPath string, args []string) error {
		called = true
		gotArgs = append([]string(nil), args...)
		return nil
	}

	err := runExec(execOptions{
		projectRoot: t.TempDir(),
		timeout:     15 * time.Second,
		command:     "pwd",
	})
	if err != nil {
		t.Fatalf("expected runExec to succeed via sg kvm reexec, got %v", err)
	}
	if !called {
		t.Fatal("expected sg kvm reexec to be attempted")
	}
	if len(gotArgs) < 7 || gotArgs[0] != "exec" {
		t.Fatalf("unexpected reexec args: %v", gotArgs)
	}
}

func TestRunExecDoesNotReexecWithSGKVMWhenAlreadyReexeced(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	t.Setenv(execKVMGroupReexecEnv, "1")

	originalBootstrap := execCommandBootstrapRunner
	originalReexec := execKVMGroupReexecRunner
	t.Cleanup(func() {
		execCommandBootstrapRunner = originalBootstrap
		execKVMGroupReexecRunner = originalReexec
	})

	execCommandBootstrapRunner = func(projectRoot string) error {
		return errors.New("libkrun requires read/write access to /dev/kvm")
	}
	called := false
	execKVMGroupReexecRunner = func(commandPath string, args []string) error {
		called = true
		return nil
	}

	err := runExec(execOptions{
		projectRoot: t.TempDir(),
		timeout:     15 * time.Second,
		command:     "pwd",
	})
	if err == nil {
		t.Fatal("expected runExec to return original kvm error without reexec")
	}
	if called {
		t.Fatal("did not expect sg kvm reexec when already in reexec environment")
	}
}

func TestSelectRuntimeBackend(t *testing.T) {
	if got := selectRuntimeBackend([]string{"libkrun"}); got != "libkrun" {
		t.Fatalf("expected libkrun->libkrun, got %q", got)
	}
	if got := selectRuntimeBackend([]string{"seatbelt"}); got != "seatbelt" {
		t.Fatalf("expected seatbelt->seatbelt, got %q", got)
	}
}

func TestApplyRuntimeBackendFromWorkspaceRejectsDind(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "dind")

	root := t.TempDir()

	applyErr := applyRuntimeBackendFromWorkspace(root)
	if applyErr == nil {
		t.Fatal("expected error when NEXUS_RUNTIME_BACKEND=dind")
	}
	if !strings.Contains(applyErr.Error(), "unsupported runtime backend") {
		t.Fatalf("expected unsupported backend error, got: %v", applyErr)
	}
}
