package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
	"github.com/oursky/nexus/packages/nexus/internal/infra/dockercompose"
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

func TestParseRequiredPorts(t *testing.T) {
	ports, err := parseRequiredPorts("5173, 5174,5173,8000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int{5173, 5174, 8000}
	if !reflect.DeepEqual(ports, expected) {
		t.Fatalf("expected %v, got %v", expected, ports)
	}
}

func TestParseRequiredPortsInvalid(t *testing.T) {
	if _, err := parseRequiredPorts("abc"); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

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

func TestMissingRequiredPorts(t *testing.T) {
	required := []int{5173, 5174, 8000}
	discovered := []dockercompose.PublishedPort{{HostPort: 5173}, {HostPort: 8000}}
	missing := missingRequiredPorts(required, discovered)
	expected := []int{5174}
	if !reflect.DeepEqual(missing, expected) {
		t.Fatalf("expected %v, got %v", expected, missing)
	}
}

func TestAssertNoManualACP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "start.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nset -euo pipefail\ndocker compose up -d\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := assertNoManualACP(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertNoManualACPFindsCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "start-acp.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nopencode serve --port 4096\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := assertNoManualACP(dir); err == nil {
		t.Fatal("expected error when manual ACP startup command exists")
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

func TestRunConfiguredProbesRequiredFailure(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "host")
	opts := options{projectRoot: t.TempDir()}
	_, err := runConfiguredProbes(opts, []config.DoctorCommandProbe{{
		Name:     "failing-required",
		Command:  "bash",
		Args:     []string{"-lc", "exit 1"},
		Required: true,
	}})
	if err == nil {
		t.Fatal("expected required probe failure")
	}
}

func TestRunConfiguredProbesOptionalFailure(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "host")
	opts := options{projectRoot: t.TempDir()}
	results, err := runConfiguredProbes(opts, []config.DoctorCommandProbe{{
		Name:     "failing-optional",
		Command:  "bash",
		Args:     []string{"-lc", "exit 1"},
		Required: false,
	}})
	if err != nil {
		t.Fatalf("did not expect optional probe error, got %v", err)
	}
	if len(results) != 1 || results[0].Status != "failed_optional" {
		t.Fatalf("expected one failed_optional result, got %+v", results)
	}
}

func TestRunConfiguredProbesRunsAllProbes(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "host")
	opts := options{projectRoot: t.TempDir()}
	results, err := runConfiguredProbes(opts, []config.DoctorCommandProbe{
		{Name: "first", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: true},
		{Name: "second", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 probe results, got %d", len(results))
	}
}

func TestMarkProbesNotRun(t *testing.T) {
	probes := []config.DoctorCommandProbe{
		{Name: "probe-a", Required: true},
		{Name: "probe-b", Required: false},
	}
	results := markProbesNotRun(probes, "skip reason")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status != "not_run" || results[0].Phase != "probe" || results[0].SkipReason != "skip reason" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[1].Status != "not_run" || results[1].Phase != "probe" || results[1].SkipReason != "skip reason" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}

func TestRunBuiltInRuntimeBackendCheckLibkrunPasses(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	result, err := runBuiltInRuntimeBackendCheck()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("expected passed status, got %+v", result)
	}
}

func TestRunBuiltInRuntimeBackendCheckUnsupportedBackendFails(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "host")
	result, err := runBuiltInRuntimeBackendCheck()
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
	if result.Status != "failed_required" {
		t.Fatalf("expected failed_required status, got %+v", result)
	}
}

func TestRunBuiltInOpencodeSessionCheckSkipsVMBackend(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	result, err := runBuiltInOpencodeSessionCheck(t.TempDir())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != "not_run" {
		t.Fatalf("expected not_run status, got %+v", result)
	}
	if !strings.Contains(result.SkipReason, "runtime backend") {
		t.Fatalf("expected runtime backend skip reason, got %+v", result)
	}
}

func TestRunDoctorLifecycleStartRunsLifecycleScript(t *testing.T) {
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorCheckCommandRunner = originalRunner
	})

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".nexus", "lifecycles"), 0o755); err != nil {
		t.Fatalf("mkdir lifecycles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".nexus", "lifecycles", "start.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write start.sh: %v", err)
	}

	called := false
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		called = true
		if command != "bash" {
			t.Fatalf("expected bash command, got %q", command)
		}
		if len(args) != 1 || args[0] != ".nexus/lifecycles/start.sh" {
			t.Fatalf("unexpected args: %v", args)
		}
		return "ok", nil
	}

	if err := runDoctorLifecycleStart(root, doctorExecContext{backend: "docker"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("expected lifecycle start command to be executed")
	}
}

func TestRunDoctorLifecycleStartFallsBackToCompose(t *testing.T) {
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorCheckCommandRunner = originalRunner
	})

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:\n  app:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}

	called := false
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		called = true
		if command != "sh" {
			t.Fatalf("expected shell command, got %q", command)
		}
		if len(args) != 2 || args[0] != "-lc" {
			t.Fatalf("unexpected args: %v", args)
		}
		if !strings.Contains(args[1], "export UID=1000; export GID=1000;") {
			t.Fatalf("expected compose fallback to export UID/GID defaults, got %q", args[1])
		}
		if !strings.Contains(args[1], "docker compose build --progress=plain") || !strings.Contains(args[1], "docker compose up -d --no-build") {
			t.Fatalf("expected compose build+up commands in shell script, got %q", args[1])
		}
		return "ok", nil
	}

	if err := runDoctorLifecycleStart(root, doctorExecContext{backend: "libkrun"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("expected compose fallback to execute")
	}
}

func TestRunDoctorLifecycleStartPrefersMakeStartOverLifecycleScript(t *testing.T) {
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorCheckCommandRunner = originalRunner
	})

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".nexus", "lifecycles"), 0o755); err != nil {
		t.Fatalf("mkdir lifecycles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".nexus", "lifecycles", "start.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write start.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("start:\n\t@echo make-start\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	called := false
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		called = true
		if command != "sh" {
			t.Fatalf("expected sh command, got %q", command)
		}
		if len(args) != 2 || args[0] != "-lc" {
			t.Fatalf("expected sh -lc args, got %v", args)
		}
		if !strings.Contains(args[1], "make start") {
			t.Fatalf("expected make start in lifecycle start script, got %q", args[1])
		}
		if !strings.Contains(args[1], "export UID=1000; export GID=1000;") {
			t.Fatalf("expected UID/GID export prefix in lifecycle start script, got %q", args[1])
		}
		if name != "lifecycle-start-make" {
			t.Fatalf("expected lifecycle-start-make context, got %q", name)
		}
		return "ok", nil
	}

	if err := runDoctorLifecycleStart(root, doctorExecContext{backend: "libkrun"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("expected make start command to be executed")
	}
}

func TestRunDoctorLifecycleSetupSkipsWhenMakeStartTargetExists(t *testing.T) {
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorCheckCommandRunner = originalRunner
	})

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".nexus", "lifecycles"), 0o755); err != nil {
		t.Fatalf("mkdir lifecycles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".nexus", "lifecycles", "setup.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write setup.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("start:\n\t@echo make-start\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	called := false
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		called = true
		return "ok", nil
	}

	if err := runDoctorLifecycleSetup(root, doctorExecContext{backend: "libkrun"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if called {
		t.Fatal("expected lifecycle setup to be skipped when make start target exists")
	}
}

func TestResolveDoctorLifecycleStartCommandReturnsSummary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("start:\n\t@echo make-start\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	command, args, contextLabel, summary, found, err := resolveDoctorLifecycleStartCommand(root)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !found {
		t.Fatal("expected startup command to be resolved")
	}
	if command != "sh" || len(args) != 2 || args[0] != "-lc" || !strings.Contains(args[1], "make start") {
		t.Fatalf("unexpected command resolution: command=%q args=%v", command, args)
	}
	if contextLabel != "lifecycle-start-make" {
		t.Fatalf("unexpected context label: %q", contextLabel)
	}
	if !strings.Contains(summary, "make start") {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func TestRunBootstrapInstallCommandDoesNotInstallOpencode(t *testing.T) {
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorCheckCommandRunner = originalRunner
	})

	var gotCommand string
	var gotArgs []string
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		gotCommand = command
		gotArgs = append([]string(nil), args...)
		return "", nil
	}

	_, err := runBootstrapInstallCommand(context.Background(), t.TempDir(), 30*time.Second, doctorExecContext{backend: "libkrun"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if gotCommand != "sh" {
		t.Fatalf("expected command sh, got %q", gotCommand)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "-lc" {
		t.Fatalf("expected args [-lc <script>], got %v", gotArgs)
	}
	if strings.Contains(gotArgs[1], "npm i -g opencode-ai") {
		t.Fatalf("unexpected opencode install in bootstrap install command: %q", gotArgs[1])
	}
}

func TestRunBootstrapInstallCommandVerifiesMakeIsInstalled(t *testing.T) {
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorCheckCommandRunner = originalRunner
	})

	var installScript string
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		if command == "sh" && len(args) == 2 && args[0] == "-lc" {
			installScript = args[1]
		}
		return "", nil
	}

	_, err := runBootstrapInstallCommand(context.Background(), t.TempDir(), 30*time.Second, doctorExecContext{backend: "libkrun"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if installScript == "" {
		t.Fatal("expected install script to be captured")
	}
	if !strings.Contains(installScript, "command -v make >/dev/null 2>&1 || exit 1") {
		t.Fatalf("expected install script to verify make availability, got: %q", installScript)
	}
}

func TestWriteReport(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "reports", "doctor.json")
	results := []checkResult{{Name: "runtime", Phase: "probe", Status: "passed", Required: true, Attempts: 1}}
	if err := writeReport(reportPath, results); err != nil {
		t.Fatalf("unexpected error writing report: %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("unable to read report: %v", err)
	}
	var parsed []checkResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid report JSON: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Name != "runtime" || parsed[0].Phase != "probe" {
		t.Fatalf("unexpected report contents: %+v", parsed)
	}
}

func TestValidateLifecycleEntrypointsRequiresStartWhenNoMakeTarget(t *testing.T) {
	root := t.TempDir()
	lifecycleDir := filepath.Join(root, ".nexus", "lifecycles")
	if err := os.MkdirAll(lifecycleDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := validateLifecycleEntrypoints(root)
	if err == nil {
		t.Fatal("expected error when start entrypoint is missing")
	}
	if !strings.Contains(err.Error(), "missing startup entrypoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateLifecycleEntrypointsAllowsComposeWithoutLifecycleDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:\n  app:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateLifecycleEntrypoints(root); err != nil {
		t.Fatalf("expected compose-only startup entrypoint to be allowed, got %v", err)
	}
}

func TestAssertNoManualACPIgnoresMissingLifecycleDir(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, ".nexus", "lifecycles")
	if err := assertNoManualACP(missing); err != nil {
		t.Fatalf("expected missing lifecycle directory to be ignored, got %v", err)
	}
}

func TestValidateLifecycleEntrypointsAllowsMakefileStartTarget(t *testing.T) {
	root := t.TempDir()
	lifecycleDir := filepath.Join(root, ".nexus", "lifecycles")
	if err := os.MkdirAll(lifecycleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("start:\n\t@echo start\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateLifecycleEntrypoints(root); err != nil {
		t.Fatalf("expected make start target to satisfy startup entrypoint, got %v", err)
	}
}

func TestValidateLifecycleEntrypointsAllowsMissingTeardownAndSetup(t *testing.T) {
	root := t.TempDir()
	lifecycleDir := filepath.Join(root, ".nexus", "lifecycles")
	if err := os.MkdirAll(lifecycleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	startPath := filepath.Join(lifecycleDir, "start.sh")
	if err := os.WriteFile(startPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := validateLifecycleEntrypoints(root); err != nil {
		t.Fatalf("expected optional setup/teardown to be allowed, got %v", err)
	}
}

func TestDiscoverDoctorScriptsOrdersNumericThenLexical(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".nexus", "probe"))
	mustMkdirAll(t, filepath.Join(root, ".nexus", "check"))

	mustWriteExec(t, filepath.Join(root, ".nexus", "probe", "10-z.sh"), "#!/usr/bin/env bash\nexit 0\n")
	mustWriteExec(t, filepath.Join(root, ".nexus", "probe", "01-a.sh"), "#!/usr/bin/env bash\nexit 0\n")
	mustWriteExec(t, filepath.Join(root, ".nexus", "probe", "misc.sh"), "#!/usr/bin/env bash\nexit 0\n")

	probes, checks, warnings, err := discoverDoctorScripts(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks, got %d", len(checks))
	}
	if len(probes) != 3 {
		t.Fatalf("expected 3 probes, got %d", len(probes))
	}

	got := []string{probes[0].Name, probes[1].Name, probes[2].Name}
	want := []string{"a", "z", "misc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected probe order: got %v want %v", got, want)
	}

	if len(warnings) == 0 {
		t.Fatal("expected warning for non-prefixed discovery script")
	}
}

func TestResolveDoctorChecksFallsBackToWorkspaceConfigWhenNoDiscoveredScripts(t *testing.T) {
	root := t.TempDir()

	cfgProbes := []config.DoctorCommandProbe{{
		Name:     "cfg-probe",
		Command:  "bash",
		Args:     []string{"-lc", "exit 0"},
		Required: true,
	}}
	cfgTests := []config.DoctorCommandCheck{{
		Name:     "cfg-test",
		Command:  "bash",
		Args:     []string{"-lc", "exit 0"},
		Required: true,
	}}

	probes, tests, warnings, err := resolveDoctorChecks(root, cfgProbes, cfgTests)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings when discovery folders are absent")
	}
	if len(probes) != 1 || probes[0].Name != "cfg-probe" {
		t.Fatalf("unexpected probes: %+v", probes)
	}
	if len(tests) != 1 || tests[0].Name != "cfg-test" {
		t.Fatalf("unexpected tests: %+v", tests)
	}

}

func TestRunInitCreatesNexusWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	orig := initRuntimeBootstrapRunner
	t.Cleanup(func() { initRuntimeBootstrapRunner = orig })
	initRuntimeBootstrapRunner = func(projectRoot, runtimeName string) error {
		if runtimeName != "libkrun" {
			t.Fatalf("expected libkrun runtime, got %q", runtimeName)
		}
		return nil
	}

	if err := runInit(initOptions{projectRoot: root}); err != nil {
		t.Fatalf("expected init success, got %v", err)
	}

	workspacePath := filepath.Join(root, ".nexus", "workspace.json")
	data, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatalf("expected workspace config file, got %v", err)
	}
	if !strings.Contains(string(data), `"version": 1`) {
		t.Fatalf("expected version in workspace.json, got:\n%s", string(data))
	}

	for _, rel := range []string{
		".nexus/lifecycles/setup.sh",
		".nexus/lifecycles/start.sh",
		".nexus/lifecycles/teardown.sh",
		".nexus/probe/01-runtime-backend.sh",
		".nexus/check/20-tooling-runtime.sh",
		".nexus/e2e/run.sh",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected %s to be created: %v", rel, err)
		}
	}
}

func TestRunInitDoesNotOverwriteExistingFilesWithoutForce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".nexus", "lifecycles"), 0o755); err != nil {
		t.Fatal(err)
	}

	custom := filepath.Join(root, ".nexus", "lifecycles", "start.sh")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\necho custom\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runInit(initOptions{projectRoot: root}); err != nil {
		t.Fatalf("expected init success, got %v", err)
	}

	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "custom") {
		t.Fatalf("expected existing file content to be preserved, got:\n%s", string(data))
	}
}

func TestRunInitCallsRuntimeBootstrapForLibkrun(t *testing.T) {
	root := t.TempDir()
	called := false
	orig := initRuntimeBootstrapRunner
	t.Cleanup(func() { initRuntimeBootstrapRunner = orig })
	initRuntimeBootstrapRunner = func(projectRoot, runtimeName string) error {
		called = true
		if runtimeName != "libkrun" {
			t.Fatalf("expected libkrun runtime, got %q", runtimeName)
		}
		return nil
	}
	if err := runInit(initOptions{projectRoot: root}); err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}
	if !called {
		t.Fatal("expected init runtime bootstrap runner to be called")
	}
}

func TestRunInitCallsRuntimeBootstrapByDefault(t *testing.T) {
	root := t.TempDir()
	called := false
	orig := initRuntimeBootstrapRunner
	t.Cleanup(func() { initRuntimeBootstrapRunner = orig })
	initRuntimeBootstrapRunner = func(projectRoot, runtimeName string) error {
		called = true
		if runtimeName != "libkrun" {
			t.Fatalf("expected libkrun runtime, got %q", runtimeName)
		}
		return nil
	}
	if err := runInit(initOptions{projectRoot: root}); err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}
	if !called {
		t.Fatal("expected init runtime bootstrap runner to be called by default")
	}
}

func TestRunInitBootstrapFailureReturnsManualStepsInNonInteractiveMode(t *testing.T) {
	origRunner := initRuntimeBootstrapRunner
	t.Cleanup(func() { initRuntimeBootstrapRunner = origRunner })
	initRuntimeBootstrapRunner = func(projectRoot, runtimeName string) error {
		return fmt.Errorf("runtime setup failed: bootstrap setup failed\n\nmanual next steps:\n  sudo -E nexus init --project-root %s", projectRoot)
	}

	err := runInit(initOptions{projectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("expected init to continue when bootstrap fails, got: %v", err)
	}
}

func TestRunInitRuntimeBootstrapReturnsFastErrorInNonInteractiveNoSudoNonRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fast-fail bootstrap path is Linux-specific")
	}
	origIsRoot := initRuntimeBootstrapIsRootFn
	origSudoOK := initRuntimeBootstrapSudoOKFn
	origIsTTY := initRuntimeBootstrapIsTTYFn
	origSkipFastFail := initRuntimeBootstrapSkipFastFailFn
	t.Cleanup(func() {
		initRuntimeBootstrapIsRootFn = origIsRoot
		initRuntimeBootstrapSudoOKFn = origSudoOK
		initRuntimeBootstrapIsTTYFn = origIsTTY
		initRuntimeBootstrapSkipFastFailFn = origSkipFastFail
	})

	initRuntimeBootstrapIsRootFn = func() bool { return false }
	initRuntimeBootstrapSudoOKFn = func() bool { return false }
	initRuntimeBootstrapIsTTYFn = func(f *os.File) bool { return false }
	initRuntimeBootstrapSkipFastFailFn = nil

	err := runInitRuntimeBootstrap(t.TempDir(), "libkrun")
	if err == nil {
		t.Fatal("expected fast-fail error in non-interactive no-sudo non-root scenario")
	}
	if !strings.Contains(err.Error(), "manual next steps") {
		t.Fatalf("expected error to contain 'manual next steps', got: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo -E nexus init") {
		t.Fatalf("expected error to contain 'sudo -E nexus init' command, got: %v", err)
	}
}
func TestRunInitRuntimeBootstrapIgnoresKVMRefreshNeededWhenPrivileged(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("kvm refresh bootstrap behavior is Linux-specific")
	}

	origIsRoot := initRuntimeBootstrapIsRootFn
	origSudoOK := initRuntimeBootstrapSudoOKFn
	origIsTTY := initRuntimeBootstrapIsTTYFn
	origSkipFastFail := initRuntimeBootstrapSkipFastFailFn
	origVerify := setupVerifyFn
	origKVMReexec := setupKVMGroupReexecFn
	t.Cleanup(func() {
		initRuntimeBootstrapIsRootFn = origIsRoot
		initRuntimeBootstrapSudoOKFn = origSudoOK
		initRuntimeBootstrapIsTTYFn = origIsTTY
		initRuntimeBootstrapSkipFastFailFn = origSkipFastFail
		setupVerifyFn = origVerify
		setupKVMGroupReexecFn = origKVMReexec
	})

	initRuntimeBootstrapIsRootFn = func() bool { return true }
	initRuntimeBootstrapSudoOKFn = func() bool { return true }
	initRuntimeBootstrapIsTTYFn = func(f *os.File) bool { return false }
	initRuntimeBootstrapSkipFastFailFn = func() bool { return true }

	setupVerifyFn = func() error { return errKVMGroupRefreshNeeded }
	setupKVMGroupReexecFn = func(commandPath string) error { return errors.New("simulated sg failure") }

	err := runInitRuntimeBootstrap(t.TempDir(), "libkrun")
	if err != nil {
		t.Fatalf("expected nil error when privileged and only kvm refresh is needed, got: %v", err)
	}
}
func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setupDoctorTestWorkspace(t *testing.T, doctorConfig config.DoctorConfig) string {
	root := t.TempDir()
	nexusDir := filepath.Join(root, ".nexus")
	lifecycleDir := filepath.Join(nexusDir, "lifecycles")
	if err := os.MkdirAll(lifecycleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"setup.sh", "start.sh", "teardown.sh"} {
		path := filepath.Join(lifecycleDir, name)
		if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wsCfg := config.WorkspaceConfig{
		Version: 1,
		Doctor:  doctorConfig,
	}
	cfgData, err := json.Marshal(wsCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nexusDir, "workspace.json"), cfgData, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDoctor_StillRunsTestsWhenRequiredProbeFails(t *testing.T) {
	t.Setenv("NEXUS_DOCTOR_DISABLE_BUILTIN_CHECKS", "1")
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	originalBootstrap := doctorExecBootstrapRunner
	originalLifecycleSetup := doctorLifecycleSetupRunner
	originalLifecycleStart := doctorLifecycleStartRunner
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorExecBootstrapRunner = originalBootstrap
		doctorLifecycleSetupRunner = originalLifecycleSetup
		doctorLifecycleStartRunner = originalLifecycleStart
		doctorCheckCommandRunner = originalRunner
	})
	doctorExecBootstrapRunner = func(projectRoot string) error { return nil }
	doctorLifecycleSetupRunner = func(projectRoot string, execCtx doctorExecContext) error { return nil }
	doctorLifecycleStartRunner = func(projectRoot string, execCtx doctorExecContext) error { return nil }
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		switch name {
		case "failing-probe", "still-runs-and-fails":
			return "", errors.New("simulated failure")
		default:
			return "ok", nil
		}
	}

	workspaceRoot := setupDoctorTestWorkspace(t, config.DoctorConfig{
		Probes: []config.DoctorCommandProbe{
			{Name: "failing-probe", Command: "bash", Args: []string{"-lc", "exit 1"}, Required: true},
		},
		Tests: []config.DoctorCommandCheck{
			{Name: "still-runs-and-fails", Command: "bash", Args: []string{"-lc", "exit 1"}, Required: true},
			{Name: "session-built-in-placeholder", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: false},
		},
	})

	reportPath := filepath.Join(t.TempDir(), "report.json")
	err := run(options{
		projectRoot: workspaceRoot,
		suite:       "test-suite",
		reportJSON:  reportPath,
	})

	if err == nil {
		t.Fatal("expected error due to required probe failure")
	}
	if !strings.Contains(err.Error(), "required probes failed") {
		t.Fatalf("expected combined error to include required probe failure, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "required tests failed") {
		t.Fatalf("expected combined error to include required test failure, got %q", err.Error())
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("unable to read report: %v", err)
	}
	var results []checkResult
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("invalid report JSON: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results (configured probe + tests), got %d", len(results))
	}

	var probeResult, requiredTestResult checkResult
	for _, r := range results {
		if r.Phase == "probe" {
			probeResult = r
		} else if r.Phase == "test" && r.Name == "still-runs-and-fails" {
			requiredTestResult = r
		}
	}

	if probeResult.Status != "failed_required" {
		t.Fatalf("expected probe status 'failed_required', got %q", probeResult.Status)
	}
	if requiredTestResult.Status != "failed_required" {
		t.Fatalf("expected test status 'failed_required', got %q", requiredTestResult.Status)
	}
	if requiredTestResult.SkipReason != "" {
		t.Fatalf("expected test skipReason to be empty, got %q", requiredTestResult.SkipReason)
	}

}

func TestDoctor_ProbesPassThenTestsRun(t *testing.T) {
	t.Setenv("NEXUS_DOCTOR_DISABLE_BUILTIN_CHECKS", "1")
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	originalBootstrap := doctorExecBootstrapRunner
	originalLifecycleSetup := doctorLifecycleSetupRunner
	originalLifecycleStart := doctorLifecycleStartRunner
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorExecBootstrapRunner = originalBootstrap
		doctorLifecycleSetupRunner = originalLifecycleSetup
		doctorLifecycleStartRunner = originalLifecycleStart
		doctorCheckCommandRunner = originalRunner
	})
	doctorExecBootstrapRunner = func(projectRoot string) error { return nil }
	doctorLifecycleSetupRunner = func(projectRoot string, execCtx doctorExecContext) error { return nil }
	doctorLifecycleStartRunner = func(projectRoot string, execCtx doctorExecContext) error { return nil }
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		return "ok", nil
	}

	workspaceRoot := setupDoctorTestWorkspace(t, config.DoctorConfig{
		Probes: []config.DoctorCommandProbe{
			{Name: "passing-probe", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: true},
		},
		Tests: []config.DoctorCommandCheck{
			{Name: "passing-test", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: true},
			{Name: "second-passing-test", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: false},
		},
	})

	reportPath := filepath.Join(t.TempDir(), "report.json")
	err := run(options{
		projectRoot: workspaceRoot,
		suite:       "test-suite",
		reportJSON:  reportPath,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("unable to read report: %v", err)
	}
	var results []checkResult
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("invalid report JSON: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results (configured probe + tests), got %d", len(results))
	}

	for _, r := range results {
		if r.Status != "passed" {
			t.Fatalf("expected status 'passed', got %q for %s", r.Status, r.Name)
		}
		if r.Phase == "" {
			t.Fatalf("expected non-empty phase, got empty for %s", r.Name)
		}
	}
}

func TestDoctor_RequiredTestFailureReturnsError(t *testing.T) {
	t.Setenv("NEXUS_DOCTOR_DISABLE_BUILTIN_CHECKS", "1")
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	originalBootstrap := doctorExecBootstrapRunner
	originalLifecycleSetup := doctorLifecycleSetupRunner
	originalLifecycleStart := doctorLifecycleStartRunner
	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() {
		doctorExecBootstrapRunner = originalBootstrap
		doctorLifecycleSetupRunner = originalLifecycleSetup
		doctorLifecycleStartRunner = originalLifecycleStart
		doctorCheckCommandRunner = originalRunner
	})
	doctorExecBootstrapRunner = func(projectRoot string) error { return nil }
	doctorLifecycleSetupRunner = func(projectRoot string, execCtx doctorExecContext) error { return nil }
	doctorLifecycleStartRunner = func(projectRoot string, execCtx doctorExecContext) error { return nil }
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
		switch name {
		case "failing-test":
			return "", errors.New("simulated failure")
		default:
			return "ok", nil
		}
	}

	workspaceRoot := setupDoctorTestWorkspace(t, config.DoctorConfig{
		Probes: []config.DoctorCommandProbe{
			{Name: "passing-probe", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: true},
		},
		Tests: []config.DoctorCommandCheck{
			{Name: "failing-test", Command: "bash", Args: []string{"-lc", "exit 1"}, Required: true},
			{Name: "passing-test", Command: "bash", Args: []string{"-lc", "exit 0"}, Required: false},
		},
	})

	reportPath := filepath.Join(t.TempDir(), "report.json")
	err := run(options{
		projectRoot: workspaceRoot,
		suite:       "test-suite",
		reportJSON:  reportPath,
	})

	if err == nil {
		t.Fatal("expected error due to required test failure")
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("unable to read report: %v", err)
	}
	var results []checkResult
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("invalid report JSON: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results (configured probe + tests), got %d", len(results))
	}

	var requiredTestResult checkResult
	for _, r := range results {
		if r.Phase == "test" && r.Name == "failing-test" {
			requiredTestResult = r
		}
	}

	if requiredTestResult.Status != "failed_required" {
		t.Fatalf("expected test status 'failed_required', got %q", requiredTestResult.Status)
	}
}

func TestCombineCheckErrors(t *testing.T) {
	probeErr := errors.New("required probes failed: startup")
	testErr := errors.New("required tests failed: auth")
	err := combineCheckErrors(probeErr, testErr)
	if err == nil {
		t.Fatal("expected combined error")
	}
	if !strings.Contains(err.Error(), probeErr.Error()) {
		t.Fatalf("expected probe error in combined error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), testErr.Error()) {
		t.Fatalf("expected test error in combined error, got %q", err.Error())
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
	if !reflect.DeepEqual(args, []string{"compose", "ps"}) {
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
	if !reflect.DeepEqual(args, []string{"compose", "ps"}) {
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

func TestRunExecUnsupportedBackendFails(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "host")

	root := t.TempDir()
	err := runExec(execOptions{
		projectRoot: root,
		timeout:     15 * time.Second,
		command:     "bash",
		args:        []string{"-lc", "printf exec-local-ok"},
	})
	if err == nil {
		t.Fatal("expected unsupported backend to fail")
	}
	if !strings.Contains(err.Error(), "unsupported runtime backend") {
		t.Fatalf("expected unsupported backend error, got %v", err)
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
	nexusDir := filepath.Join(root, ".nexus")
	if err := os.MkdirAll(nexusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nexusDir, "workspace.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	originalRunner := doctorCheckCommandRunner
	t.Cleanup(func() { doctorCheckCommandRunner = originalRunner })

	called := false
	doctorCheckCommandRunner = func(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
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

func TestRunDoctorReexecsWithSGKVMOnKVMPermissionError(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	t.Setenv(execKVMGroupReexecEnv, "")
	t.Setenv("NEXUS_DOCTOR_DISABLE_BUILTIN_CHECKS", "1")
	addFakeSGToPath(t)

	workspaceRoot := setupDoctorTestWorkspace(t, config.DoctorConfig{})

	originalBootstrap := doctorExecBootstrapRunner
	originalReexec := execKVMGroupReexecRunner
	originalArgs := os.Args
	t.Cleanup(func() {
		doctorExecBootstrapRunner = originalBootstrap
		execKVMGroupReexecRunner = originalReexec
		os.Args = originalArgs
	})

	doctorExecBootstrapRunner = func(projectRoot string) error {
		return errors.New("libkrun requires read/write access to /dev/kvm")
	}

	called := false
	execKVMGroupReexecRunner = func(commandPath string, args []string) error {
		called = true
		if commandPath == "" {
			t.Fatal("expected command path")
		}
		if len(args) == 0 || args[0] != "doctor" {
			t.Fatalf("expected doctor reexec args, got %v", args)
		}
		return nil
	}

	os.Args = []string{"nexus", "doctor", "--project-root", workspaceRoot, "--suite", "local"}

	err := run(options{projectRoot: workspaceRoot, suite: "local"})
	if err != nil {
		t.Fatalf("expected run to return nil after successful sg kvm reexec, got %v", err)
	}
	if !called {
		t.Fatal("expected sg kvm reexec for doctor command")
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

func TestApplyRuntimeBackendFromWorkspaceUsesInitHintWhenPresent(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "")

	root := t.TempDir()
	runDir := filepath.Join(root, ".nexus", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "nexus-init-env"), []byte("NEXUS_RUNTIME_BACKEND=libkrun\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := applyRuntimeBackendFromWorkspace(root); err != nil {
		t.Fatalf("expected init hint backend to apply, got %v", err)
	}
	if got := os.Getenv("NEXUS_RUNTIME_BACKEND"); got != "libkrun" {
		t.Fatalf("expected init hint to set libkrun, got %q", got)
	}
}

func TestApplyRuntimeBackendFromWorkspaceRejectsInvalidInitHint(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "")

	root := t.TempDir()
	runDir := filepath.Join(root, ".nexus", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "nexus-init-env"), []byte("NEXUS_RUNTIME_BACKEND=dind\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := applyRuntimeBackendFromWorkspace(root)
	if err == nil {
		t.Fatal("expected invalid init hint to fail")
	}
	if !strings.Contains(err.Error(), "invalid NEXUS_RUNTIME_BACKEND value") {
		t.Fatalf("expected invalid hint error, got %v", err)
	}
}

func TestApplyRuntimeBackendFromWorkspaceRejectsDind(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "dind")

	root := t.TempDir()
	nexusDir := filepath.Join(root, ".nexus")
	if err := os.MkdirAll(nexusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nexusDir, "workspace.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	applyErr := applyRuntimeBackendFromWorkspace(root)
	if applyErr == nil {
		t.Fatal("expected error when NEXUS_RUNTIME_BACKEND=dind")
	}
	if !strings.Contains(applyErr.Error(), "unsupported runtime backend") {
		t.Fatalf("expected unsupported backend error, got: %v", applyErr)
	}
}

func TestBootstrapDoctorExecContextLibkrunIsNoOp(t *testing.T) {
	t.Setenv("NEXUS_RUNTIME_BACKEND", "libkrun")
	if err := bootstrapDoctorExecContext(t.TempDir()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
