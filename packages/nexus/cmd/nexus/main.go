package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
	"github.com/oursky/nexus/packages/nexus/internal/infra/dockercompose"

	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
	projectcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/project"
	spotlightcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/spotlight"
	workspacecmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/workspace"
)

type options struct {
	projectRoot       string
	suite             string
	composeFile       string
	requiredHostPorts []int
	reportJSON        string
}

type execOptions struct {
	projectRoot string
	timeout     time.Duration
	command     string
	args        []string
}

type initOptions struct {
	projectRoot string
	force       bool
}

const execKVMGroupReexecEnv = "NEXUS_EXEC_KVM_GROUP_REEXEC"

// extraCommands holds optional cobra commands registered by platform-specific
// init() functions (e.g. the hidden "libkrun-vm" subcommand on Linux+libkrun).
var extraCommands []*cobra.Command

func main() {
	root := &cobra.Command{
		Use:           "nexus",
		Short:         "Nexus remote workspace CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		daemoncmd.Command(),
		workspacecmd.Command(),
		spotlightcmd.Command(),
		projectcmd.Command(),
		initCommand(),
		execCommand(),
		doctorCommand(),
	)
	for _, cmd := range extraCommands {
		root.AddCommand(cmd)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func initCommand() *cobra.Command {
	var projectRoot string
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize nexus workspace metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(initOptions{
				projectRoot: projectRoot,
				force:       force,
			})
		},
	}

	cmd.Flags().StringVar(&projectRoot, "project-root", "", "absolute path to project repository")
	_ = cmd.MarkFlagRequired("project-root")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing .nexus files")
	return cmd
}

func execCommand() *cobra.Command {
	var projectRoot string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "exec",
		Short: "Execute a command in the workspace runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("command is required")
			}
			return runExec(execOptions{
				projectRoot: projectRoot,
				timeout:     timeout,
				command:     args[0],
				args:        args[1:],
			})
		},
	}

	cmd.Flags().StringVar(&projectRoot, "project-root", "", "absolute path to downstream project repository")
	_ = cmd.MarkFlagRequired("project-root")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "command timeout")
	return cmd
}

func doctorCommand() *cobra.Command {
	var projectRoot string
	var suite string
	var composeFile string
	var requiredPorts string
	var reportJSON string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run doctor probes and tests for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			var ports []int
			if strings.TrimSpace(requiredPorts) != "" {
				parsedPorts, err := parseRequiredPorts(requiredPorts)
				if err != nil {
					return err
				}
				ports = parsedPorts
			}

			return run(options{
				projectRoot:       projectRoot,
				suite:             suite,
				composeFile:       composeFile,
				requiredHostPorts: ports,
				reportJSON:        strings.TrimSpace(reportJSON),
			})
		},
	}

	cmd.Flags().StringVar(&projectRoot, "project-root", "", "absolute path to downstream project repository")
	_ = cmd.MarkFlagRequired("project-root")
	cmd.Flags().StringVar(&suite, "suite", "", "doctor suite name")
	_ = cmd.MarkFlagRequired("suite")
	cmd.Flags().StringVar(&composeFile, "compose-file", "docker-compose.yml", "compose file path relative to project root")
	cmd.Flags().StringVar(&requiredPorts, "required-host-ports", "", "comma-separated required published host ports")
	cmd.Flags().StringVar(&reportJSON, "report-json", "", "optional path to write doctor probe results as JSON")
	return cmd
}

func runInit(opts initOptions) error {
	if !filepath.IsAbs(opts.projectRoot) {
		return fmt.Errorf("project root must be absolute: %s", opts.projectRoot)
	}

	nexusDir := filepath.Join(opts.projectRoot, ".nexus")
	if err := os.MkdirAll(nexusDir, 0o755); err != nil {
		return fmt.Errorf("create .nexus directory: %w", err)
	}

	for _, dir := range []string{"lifecycles", "probe", "check", "e2e"} {
		if err := os.MkdirAll(filepath.Join(nexusDir, dir), 0o755); err != nil {
			return fmt.Errorf("create .nexus/%s directory: %w", dir, err)
		}
	}

	workspaceCfg := config.WorkspaceConfig{
		Schema:  "https://raw.githubusercontent.com/oursky/nexus/main/schemas/workspace.v1.schema.json",
		Version: 1,
		Doctor: config.DoctorConfig{
			Probes: []config.DoctorCommandProbe{{
				Name:     "runtime-backend",
				Command:  "bash",
				Args:     []string{".nexus/probe/01-runtime-backend.sh"},
				Required: true,
			}},
			Tests: []config.DoctorCommandCheck{{
				Name:     "tooling-runtime",
				Command:  "bash",
				Args:     []string{".nexus/check/20-tooling-runtime.sh"},
				Required: true,
			}},
		},
	}

	workspaceJSON, err := json.MarshalIndent(workspaceCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace config: %w", err)
	}
	workspaceJSON = append(workspaceJSON, '\n')

	files := map[string]string{
		filepath.Join(nexusDir, "workspace.json"):                 string(workspaceJSON),
		filepath.Join(nexusDir, "lifecycles", "setup.sh"):         "#!/usr/bin/env bash\nset -euo pipefail\necho 'setup: no-op'\n",
		filepath.Join(nexusDir, "lifecycles", "start.sh"):         "#!/usr/bin/env bash\nset -euo pipefail\necho 'start: no-op'\n",
		filepath.Join(nexusDir, "lifecycles", "teardown.sh"):      "#!/usr/bin/env bash\nset -euo pipefail\necho 'teardown: no-op'\n",
		filepath.Join(nexusDir, "probe", "01-runtime-backend.sh"): "#!/usr/bin/env bash\nset -euo pipefail\necho \"runtime-backend probe: backend=${NEXUS_RUNTIME_BACKEND:-unknown}\"\n",
		filepath.Join(nexusDir, "check", "20-tooling-runtime.sh"): "#!/usr/bin/env bash\nset -euo pipefail\ncommand -v bash >/dev/null 2>&1\ncommand -v curl >/dev/null 2>&1 || true\necho 'tooling-runtime check passed'\n",
		filepath.Join(nexusDir, "e2e", "run.sh"):                  "#!/usr/bin/env bash\nset -euo pipefail\necho 'e2e: no-op'\n",
	}

	for path, content := range files {
		if !opts.force {
			if _, err := os.Stat(path); err == nil {
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", path, err)
			}
		}

		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	if err := initRuntimeBootstrapRunner(opts.projectRoot, "libkrun"); err != nil {
		fmt.Printf("init warning: runtime bootstrap unavailable, continuing (%v)\n", err)
	}

	fmt.Printf("initialized nexus workspace metadata at %s\n", nexusDir)
	return nil
}

func runExec(opts execOptions) error {
	if !filepath.IsAbs(opts.projectRoot) {
		return fmt.Errorf("project root must be absolute: %s", opts.projectRoot)
	}
	if err := applyRuntimeBackendFromWorkspace(opts.projectRoot); err != nil {
		return err
	}
	if opts.command == "" {
		return errors.New("command is required")
	}
	if opts.timeout <= 0 {
		return fmt.Errorf("timeout must be > 0: %s", opts.timeout)
	}

	defer func() {
		if cleanupErr := runDoctorExecContextCleanup(); cleanupErr != nil {
			fmt.Printf("exec warning: cleanup failed: %v\n", cleanupErr)
		}
	}()
	execCtx := loadDoctorExecContext()
	if err := execCommandBootstrapRunner(opts.projectRoot); err != nil {
		if shouldReexecExecWithKVMGroup(execCtx.backend, err) {
			cmdPath := setupCommandPath()
			reexecArgs := make([]string, 0, len(opts.args)+8)
			reexecArgs = append(reexecArgs, "exec", "--project-root", opts.projectRoot, "--timeout", opts.timeout.String(), "--", opts.command)
			reexecArgs = append(reexecArgs, opts.args...)
			if reexecErr := execKVMGroupReexecRunner(cmdPath, reexecArgs); reexecErr == nil {
				return nil
			} else {
				return fmt.Errorf("%w; sg kvm reexec failed: %v", err, reexecErr)
			}
		}
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	fmt.Printf("exec exec: %s (attempt %d/%d, timeout=%s, context=%s): %s\n", opts.command, 1, 1, opts.timeout, execCtx.backend, formatCommand(opts.command, opts.args))
	out, err := execCheckCommandRunner(ctx, opts.projectRoot, "exec", opts.command, 1, 1, opts.timeout, opts.command, opts.args, execCtx)

	if strings.TrimSpace(out) != "" {
		fmt.Println(strings.TrimSpace(out))
	}
	if err != nil {
		return fmt.Errorf("exec failed: %w", err)
	}

	return nil
}

func shouldReexecExecWithKVMGroup(backend string, runErr error) bool {
	if runErr == nil {
		return false
	}
	if backend != "libkrun" {
		return false
	}
	if os.Getenv(execKVMGroupReexecEnv) == "1" {
		return false
	}
	if _, err := exec.LookPath("sg"); err != nil {
		return false
	}
	return strings.Contains(runErr.Error(), "/dev/kvm")
}

func runExecWithKVMGroupReexec(commandPath string, args []string) error {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(commandPath))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	inner := strings.Join(parts, " ")

	cmd := exec.Command("sg", "kvm", "-c", inner)
	cmd.Env = append(os.Environ(), execKVMGroupReexecEnv+"=1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func run(opts options) error {
	if !filepath.IsAbs(opts.projectRoot) {
		return fmt.Errorf("project root must be absolute: %s", opts.projectRoot)
	}
	if err := applyRuntimeBackendFromWorkspace(opts.projectRoot); err != nil {
		return err
	}

	execCtx := loadDoctorExecContext()

	if opts.composeFile == "" {
		opts.composeFile = "docker-compose.yml"
	}

	requiredFiles := []string{
		filepath.Join(opts.projectRoot, ".nexus", "workspace.json"),
	}

	for _, p := range requiredFiles {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("missing required file: %s", p)
			}
			return fmt.Errorf("stat %s: %w", p, err)
		}
	}

	if err := validateLifecycleEntrypoints(opts.projectRoot); err != nil {
		return err
	}

	if err := assertNoManualACP(filepath.Join(opts.projectRoot, ".nexus", "lifecycles")); err != nil {
		return err
	}

	if err := ensureDotEnv(opts.projectRoot); err != nil {
		return err
	}

	workspaceConfig, _, err := config.LoadWorkspaceConfig(opts.projectRoot)
	if err != nil {
		return fmt.Errorf("invalid workspace config: %w", err)
	}

	probesToRun, testsToRun, warnings, err := resolveDoctorChecks(opts.projectRoot, workspaceConfig.Doctor.Probes, workspaceConfig.Doctor.Tests)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		fmt.Printf("doctor warning: %s\n", warning)
	}

	defer func() {
		if cleanupErr := runDoctorExecContextCleanup(); cleanupErr != nil {
			fmt.Printf("doctor warning: cleanup failed: %v\n", cleanupErr)
		}
	}()
	if err := doctorExecBootstrapRunner(opts.projectRoot); err != nil {
		execCtx := loadDoctorExecContext()
		if shouldReexecExecWithKVMGroup(execCtx.backend, err) {
			cmdPath := setupCommandPath()
			reexecArgs := append([]string(nil), os.Args[1:]...)
			if reexecErr := execKVMGroupReexecRunner(cmdPath, reexecArgs); reexecErr == nil {
				return nil
			} else {
				return fmt.Errorf("%w; sg kvm reexec failed: %v", err, reexecErr)
			}
		}
		return err
	}

	if err := doctorLifecycleSetupRunner(opts.projectRoot, execCtx); err != nil {
		return err
	}

	if err := doctorLifecycleStartRunner(opts.projectRoot, execCtx); err != nil {
		return err
	}

	opts = applyDoctorConfigDefaults(opts, workspaceConfig.Doctor)

	publishedPorts := make([]dockercompose.PublishedPort, 0)
	composePath := filepath.Join(opts.projectRoot, opts.composeFile)
	discoverCtx, discoverCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer discoverCancel()
	if ports, discoverErr := dockercompose.DiscoverPublishedPorts(discoverCtx, opts.projectRoot); discoverErr != nil {
		fmt.Printf("doctor warning: compose port discovery failed for %s: %v\n", composePath, discoverErr)
	} else {
		publishedPorts = ports
	}

	probeResults, probeErr := runConfiguredProbes(opts, probesToRun)

	var allResults []checkResult

	if os.Getenv("NEXUS_DOCTOR_DISABLE_BUILTIN_CHECKS") != "1" {
		runtimeResult, runtimeErr := runBuiltInRuntimeBackendCheck()
		allResults = append(allResults, runtimeResult)
		probeErr = combineCheckErrors(runtimeErr, probeErr)
	}

	allResults = append(allResults, probeResults...)

	testResults, testErr := runConfiguredTests(opts, testsToRun)
	allResults = append(allResults, testResults...)

	if os.Getenv("NEXUS_DOCTOR_DISABLE_BUILTIN_CHECKS") != "1" {
		builtinResult, builtinErr := runBuiltInOpencodeSessionCheck(opts.projectRoot)
		allResults = append(allResults, builtinResult)
		testErr = combineCheckErrors(testErr, builtinErr)
	}

	if err := writeReport(opts.reportJSON, allResults); err != nil {
		return err
	}

	err = combineCheckErrors(probeErr, testErr)
	if err != nil {
		return err
	}

	fmt.Printf("doctor suite passed: %s (discovered %d compose ports)\n", opts.suite, len(publishedPorts))
	return nil
}

func applyDoctorConfigDefaults(opts options, doctorCfg config.DoctorConfig) options {
	if len(opts.requiredHostPorts) == 0 && len(doctorCfg.RequiredHostPorts) > 0 {
		opts.requiredHostPorts = append([]int(nil), doctorCfg.RequiredHostPorts...)
	}
	return opts
}

type checkResult struct {
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	Status     string `json:"status"`
	Required   bool   `json:"required"`
	Attempts   int    `json:"attempts"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
	SkipReason string `json:"skipReason,omitempty"`
}

type doctorExecContext struct {
	backend string
}

var doctorCheckCommandRunner = runCheckCommandWithExecContext

var execCheckCommandRunner = runCheckCommandWithExecContext

var bootstrapInstallCommandRunner = runBootstrapInstallCommand

var doctorExecBootstrapRunner = bootstrapDoctorExecContext

var execCommandBootstrapRunner = bootstrapExecCommandContext

var doctorLifecycleStartRunner = runDoctorLifecycleStart

var doctorLifecycleSetupRunner = runDoctorLifecycleSetup

var execKVMGroupReexecRunner = runExecWithKVMGroupReexec

var doctorExecCleanup func() error

var hostDockerSocketStat = os.Stat

func runBootstrapInstallCommand(ctx context.Context, projectRoot string, timeout time.Duration, execCtx doctorExecContext) (string, error) {
	aptOpts := "-o Acquire::Retries=1 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15"
	installCmd := "chmod 1777 /tmp; apt-get clean >/dev/null 2>&1 || true; rm -rf /var/lib/apt/lists/*; mkdir -p /var/cache/apt/archives/partial /var/lib/apt/lists/partial; apt-get " + aptOpts + " update && DEBIAN_FRONTEND=noninteractive apt-get -o Dpkg::Options::=--force-confold " + aptOpts + " install -y bash docker.io curl make python3 git nodejs npm iptables docker-compose-v2 docker-buildx-plugin || DEBIAN_FRONTEND=noninteractive apt-get -o Dpkg::Options::=--force-confold " + aptOpts + " install -y bash docker.io curl make python3 git nodejs npm iptables docker-compose-v2 || DEBIAN_FRONTEND=noninteractive apt-get -o Dpkg::Options::=--force-confold " + aptOpts + " install -y bash docker.io curl make python3 git nodejs npm iptables && DEBIAN_FRONTEND=noninteractive apt-get -o Dpkg::Options::=--force-confold " + aptOpts + " install -y docker-compose-v2 docker-buildx-plugin || DEBIAN_FRONTEND=noninteractive apt-get -o Dpkg::Options::=--force-confold " + aptOpts + " install -y docker-compose-v2 || true; command -v make >/dev/null 2>&1 || exit 1"
	return doctorCheckCommandRunner(ctx, projectRoot, "probe", "runtime-backend-capabilities", 1, 1, timeout, "sh", []string{"-lc", installCmd}, execCtx)
}

func setDoctorExecContextCleanup(cleanup func() error) {
	doctorExecCleanup = cleanup
}

func runDoctorExecContextCleanup() error {
	if doctorExecCleanup == nil {
		return nil
	}
	cleanup := doctorExecCleanup
	doctorExecCleanup = nil
	return cleanup()
}

func detectHostDockerSocket() string {
	candidates := make([]string, 0, 4)

	raw := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if strings.HasPrefix(raw, "unix://") {
		candidate := strings.TrimPrefix(raw, "unix://")
		if candidate != "" {
			candidates = append(candidates, candidate)
			if !strings.HasPrefix(candidate, "/var/lib/snapd/hostfs/") {
				candidates = append(candidates, "/var/lib/snapd/hostfs"+candidate)
			}
		}
	}

	candidates = append(candidates, "/var/lib/snapd/hostfs/var/run/docker.sock", "/var/run/docker.sock")

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if info, err := hostDockerSocketStat(candidate); err == nil && (info.Mode()&os.ModeSocket) != 0 {
			return candidate
		}
	}

	return ""
}

func runConfiguredProbes(opts options, probes []config.DoctorCommandProbe) ([]checkResult, error) {
	results := make([]checkResult, 0, len(probes))
	requiredFailures := make([]string, 0)

	for _, probe := range probes {
		timeout := 10 * time.Minute
		if probe.TimeoutMs > 0 {
			timeout = time.Duration(probe.TimeoutMs) * time.Millisecond
		}
		attempts := probe.Retries + 1
		start := time.Now()
		lastErr := ""

		for attempt := 1; attempt <= attempts; attempt++ {
			probeCtx, cancel := context.WithTimeout(context.Background(), timeout)
			out, err := runCheckCommand(probeCtx, opts.projectRoot, "probe", probe.Name, attempt, attempts, timeout, probe.Command, probe.Args)
			cancel()

			if err == nil {
				fmt.Printf("probe passed: %s (attempt %d/%d)\n", probe.Name, attempt, attempts)
				results = append(results, checkResult{
					Name:       probe.Name,
					Phase:      "probe",
					Status:     "passed",
					Required:   probe.Required,
					Attempts:   attempt,
					DurationMs: time.Since(start).Milliseconds(),
				})
				lastErr = ""
				break
			}

			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			lastErr = msg
			if attempt < attempts {
				fmt.Printf("probe retrying: %s (attempt %d/%d)\n", probe.Name, attempt+1, attempts)
			}
		}

		if lastErr != "" {
			status := "failed_optional"
			if probe.Required {
				status = "failed_required"
				requiredFailures = append(requiredFailures, probe.Name)
			}
			results = append(results, checkResult{
				Name:       probe.Name,
				Phase:      "probe",
				Status:     status,
				Required:   probe.Required,
				Attempts:   attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      lastErr,
			})
			if probe.Required {
				fmt.Printf("required probe failed: %s: %s\n", probe.Name, lastErr)
			} else {
				fmt.Printf("optional probe failed: %s: %s\n", probe.Name, lastErr)
			}
		}
	}

	if len(requiredFailures) > 0 {
		return results, fmt.Errorf("required probes failed: %s", strings.Join(requiredFailures, ", "))
	}

	return results, nil
}

func runConfiguredTests(opts options, tests []config.DoctorCommandCheck) ([]checkResult, error) {
	results := make([]checkResult, 0, len(tests))
	requiredFailures := make([]string, 0)

	for _, test := range tests {
		timeout := 10 * time.Minute
		if test.TimeoutMs > 0 {
			timeout = time.Duration(test.TimeoutMs) * time.Millisecond
		}
		attempts := test.Retries + 1
		start := time.Now()
		lastErr := ""

		for attempt := 1; attempt <= attempts; attempt++ {
			testCtx, cancel := context.WithTimeout(context.Background(), timeout)
			out, err := runCheckCommand(testCtx, opts.projectRoot, "test", test.Name, attempt, attempts, timeout, test.Command, test.Args)
			cancel()

			if err == nil {
				fmt.Printf("test passed: %s (attempt %d/%d)\n", test.Name, attempt, attempts)
				results = append(results, checkResult{
					Name:       test.Name,
					Phase:      "test",
					Status:     "passed",
					Required:   test.Required,
					Attempts:   attempt,
					DurationMs: time.Since(start).Milliseconds(),
				})
				lastErr = ""
				break
			}

			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			lastErr = msg
			if attempt < attempts {
				fmt.Printf("test retrying: %s (attempt %d/%d)\n", test.Name, attempt+1, attempts)
			}
		}

		if lastErr != "" {
			status := "failed_optional"
			if test.Required {
				status = "failed_required"
				requiredFailures = append(requiredFailures, test.Name)
			}
			results = append(results, checkResult{
				Name:       test.Name,
				Phase:      "test",
				Status:     status,
				Required:   test.Required,
				Attempts:   attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      lastErr,
			})
			if test.Required {
				fmt.Printf("required test failed: %s: %s\n", test.Name, lastErr)
			} else {
				fmt.Printf("optional test failed: %s: %s\n", test.Name, lastErr)
			}
		}
	}

	if len(requiredFailures) > 0 {
		return results, fmt.Errorf("required tests failed: %s", strings.Join(requiredFailures, ", "))
	}

	return results, nil
}

func runDoctorLifecycleStart(projectRoot string, execCtx doctorExecContext) error {
	command, args, contextLabel, summary, found, err := resolveDoctorLifecycleStartCommand(projectRoot)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	fmt.Printf("doctor lifecycle start selected command: %s\n", summary)

	timeout := 10 * time.Minute
	if rawTimeout := strings.TrimSpace(os.Getenv("NEXUS_DOCTOR_START_TIMEOUT_MS")); rawTimeout != "" {
		if ms, err := strconv.Atoi(rawTimeout); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := doctorCheckCommandRunner(ctx, projectRoot, "probe", contextLabel, 1, 1, timeout, command, args, execCtx)
	if err != nil {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("doctor lifecycle start failed: %s", detail)
	}

	fmt.Printf("doctor lifecycle started (%s)\n", contextLabel)
	return nil
}

func resolveDoctorLifecycleStartCommand(projectRoot string) (command string, args []string, contextLabel string, summary string, found bool, err error) {
	if hasMakeTarget(projectRoot, "start") {
		makeStartCmd := "export UID=1000; export GID=1000; make start"
		return "sh", []string{"-lc", makeStartCmd}, "lifecycle-start-make", "make start", true, nil
	}

	if hasComposeTarget(projectRoot) {
		composeCmd := "if [ -f Makefile ] && command -v make >/dev/null 2>&1; then if grep -q '^secret:' Makefile; then make secret; fi; fi; export BUILDKIT_PROGRESS=plain; export UID=1000; export GID=1000; docker compose build --progress=plain; docker compose up -d --no-build"
		return "sh", []string{"-lc", composeCmd}, "lifecycle-start-compose", "docker compose build --progress=plain && docker compose up -d --no-build", true, nil
	}

	startPath := filepath.Join(projectRoot, ".nexus", "lifecycles", "start.sh")
	startExists, err := isExecutableFile(startPath)
	if err != nil {
		return "", nil, "", "", false, err
	}
	if startExists {
		return "bash", []string{".nexus/lifecycles/start.sh"}, "lifecycle-start-script", "bash .nexus/lifecycles/start.sh", true, nil
	}

	return "", nil, "", "", false, nil
}

func hasComposeTarget(projectRoot string) bool {
	candidates := []string{
		"docker-compose.yml",
		"docker-compose.yaml",
		"compose.yml",
		"compose.yaml",
	}
	for _, name := range candidates {
		if stat, err := os.Stat(filepath.Join(projectRoot, name)); err == nil && !stat.IsDir() {
			return true
		}
	}
	return false
}

func runDoctorLifecycleSetup(projectRoot string, execCtx doctorExecContext) error {
	if hasMakeTarget(projectRoot, "start") {
		fmt.Println("doctor lifecycle setup skipped (startup handled by Makefile target: make start)")
		return nil
	}

	setupPath := filepath.Join(projectRoot, ".nexus", "lifecycles", "setup.sh")
	command := ""
	args := []string(nil)
	contextLabel := "lifecycle-setup"

	if setupExists, err := isExecutableFile(setupPath); err != nil {
		return err
	} else if setupExists {
		command = "bash"
		args = []string{".nexus/lifecycles/setup.sh"}
		contextLabel = "lifecycle-setup-script"
	} else {
		return nil
	}

	timeout := 10 * time.Minute
	if rawTimeout := strings.TrimSpace(os.Getenv("NEXUS_DOCTOR_SETUP_TIMEOUT_MS")); rawTimeout != "" {
		if ms, err := strconv.Atoi(rawTimeout); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := doctorCheckCommandRunner(ctx, projectRoot, "probe", contextLabel, 1, 1, timeout, command, args, execCtx)
	if err != nil {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("doctor lifecycle setup failed: %s", detail)
	}

	fmt.Printf("doctor lifecycle setup completed (%s)\n", contextLabel)
	return nil
}

func runBuiltInOpencodeSessionCheck(projectRoot string) (checkResult, error) {
	const checkName = "tooling-opencode-session"
	start := time.Now()

	return checkResult{
		Name:       checkName,
		Phase:      "test",
		Status:     "not_run",
		Required:   true,
		Attempts:   0,
		DurationMs: time.Since(start).Milliseconds(),
		SkipReason: "opencode session check is skipped for runtime backend checks",
	}, nil
}

func runBuiltInRuntimeBackendCheck() (checkResult, error) {
	const checkName = "runtime-backend-capabilities"
	start := time.Now()
	result := checkResult{
		Name:     checkName,
		Phase:    "probe",
		Required: true,
		Attempts: 1,
	}

	backend := strings.TrimSpace(os.Getenv("NEXUS_RUNTIME_BACKEND"))
	if backend != "libkrun" && backend != "seatbelt" {
		result.Status = "failed_required"
		result.DurationMs = time.Since(start).Milliseconds()
		result.Error = fmt.Sprintf("unsupported runtime backend %q: doctor command only supports libkrun or seatbelt", backend)
		return result, fmt.Errorf("required probes failed: %s", checkName)
	}

	result.Status = "passed"
	result.DurationMs = time.Since(start).Milliseconds()
	fmt.Printf("probe passed: %s (attempt 1/1)\n", checkName)
	return result, nil
}

func bootstrapDoctorExecContext(projectRoot string) error {
	setDoctorExecContextCleanup(nil)
	execCtx := loadDoctorExecContext()
	switch execCtx.backend {
	case "libkrun":
		// libkrun workspaces run on the remote daemon; no local bootstrap needed.
		return nil
	case "seatbelt":
		return nil
	default:
		return fmt.Errorf("unsupported runtime backend %q: doctor command only supports libkrun or seatbelt", execCtx.backend)
	}
}

func bootstrapExecCommandContext(projectRoot string) error {
	setDoctorExecContextCleanup(nil)
	execCtx := loadDoctorExecContext()
	switch execCtx.backend {
	case "libkrun":
		// libkrun workspaces run on the remote daemon; no local bootstrap needed.
		return nil
	case "seatbelt":
		return nil
	default:
		return fmt.Errorf("unsupported runtime backend %q: exec command only supports libkrun or seatbelt", execCtx.backend)
	}
}

func markChecksNotRun(tests []config.DoctorCommandCheck, skipReason string) []checkResult {
	results := make([]checkResult, 0, len(tests))
	for _, test := range tests {
		results = append(results, checkResult{
			Name:       test.Name,
			Phase:      "test",
			Status:     "not_run",
			Required:   test.Required,
			Attempts:   0,
			DurationMs: 0,
			SkipReason: skipReason,
		})
	}
	return results
}

func markProbesNotRun(probes []config.DoctorCommandProbe, skipReason string) []checkResult {
	results := make([]checkResult, 0, len(probes))
	for _, probe := range probes {
		results = append(results, checkResult{
			Name:       probe.Name,
			Phase:      "probe",
			Status:     "not_run",
			Required:   probe.Required,
			Attempts:   0,
			DurationMs: 0,
			SkipReason: skipReason,
		})
	}
	return results
}

func runCheckCommand(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string) (string, error) {
	execCtx := loadDoctorExecContext()
	return runCheckCommandWithExecContext(ctx, projectRoot, phase, name, attempt, attempts, timeout, command, args, execCtx)
}

func runCheckCommandWithExecContext(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext) (string, error) {
	return runHostCheckCommandWithExecContext(ctx, projectRoot, phase, name, attempt, attempts, timeout, command, args, execCtx, "")
}

func runHostCheckCommandWithExecContext(ctx context.Context, projectRoot, phase, name string, attempt, attempts int, timeout time.Duration, command string, args []string, execCtx doctorExecContext, contextOverride string) (string, error) {
	cmdName, cmdArgs, cmdEnv, contextLabel := resolveCheckCommand(projectRoot, command, args, execCtx)
	if strings.TrimSpace(contextOverride) != "" {
		contextLabel = contextOverride
	}

	fmt.Printf("%s exec: %s (attempt %d/%d, timeout=%s, context=%s): %s\n", phase, name, attempt, attempts, timeout, contextLabel, formatCommand(cmdName, cmdArgs))

	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	cmd.Dir = projectRoot
	env := append(os.Environ(), cmdEnv...)
	if !hasEnvKey(env, "UID") {
		env = append(env, fmt.Sprintf("UID=%d", os.Getuid()))
	}
	if !hasEnvKey(env, "GID") {
		env = append(env, fmt.Sprintf("GID=%d", os.Getgid()))
	}
	cmd.Env = env

	var output bytes.Buffer
	writer := io.MultiWriter(os.Stdout, &output)
	cmd.Stdout = writer
	cmd.Stderr = writer

	err := cmd.Run()
	out := strings.TrimSpace(output.String())
	if out == "" && err != nil {
		out = err.Error()
	}

	return out, err
}

func hasEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func loadDoctorExecContext() doctorExecContext {
	backend := strings.TrimSpace(os.Getenv("NEXUS_RUNTIME_BACKEND"))
	if backend == "" {
		backend = selectRuntimeBackend(nil)
		if backend == "" {
			backend = "seatbelt"
		}
	}
	return doctorExecContext{
		backend: backend,
	}
}

func applyRuntimeBackendFromWorkspace(projectRoot string) error {
	if rawBackend := strings.TrimSpace(os.Getenv("NEXUS_RUNTIME_BACKEND")); rawBackend != "" {
		backend, ok := normalizeRuntimeBackend(rawBackend)
		if !ok {
			return fmt.Errorf("unsupported runtime backend %q: supported: libkrun, seatbelt", rawBackend)
		}
		if err := os.Setenv("NEXUS_RUNTIME_BACKEND", backend); err != nil {
			return fmt.Errorf("set runtime backend env: %w", err)
		}
		return nil
	}

	if hintedBackend, err := loadRuntimeBackendHint(projectRoot); err != nil {
		return err
	} else if hintedBackend != "" {
		if err := os.Setenv("NEXUS_RUNTIME_BACKEND", hintedBackend); err != nil {
			return fmt.Errorf("set runtime backend env: %w", err)
		}
		return nil
	}

	backend := selectRuntimeBackend(nil)
	if backend == "" {
		return fmt.Errorf("no supported runtime found; doctor/exec support libkrun or seatbelt")
	}

	if err := os.Setenv("NEXUS_RUNTIME_BACKEND", backend); err != nil {
		return fmt.Errorf("set runtime backend env: %w", err)
	}

	return nil
}

func loadRuntimeBackendHint(projectRoot string) (string, error) {
	hintPath := filepath.Join(projectRoot, ".nexus", "run", "nexus-init-env")
	data, err := os.ReadFile(hintPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read init runtime hint: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "NEXUS_RUNTIME_BACKEND" {
			continue
		}
		backend, valid := normalizeRuntimeBackend(value)
		if !valid {
			return "", fmt.Errorf("invalid NEXUS_RUNTIME_BACKEND value %q in %s", strings.TrimSpace(value), hintPath)
		}
		return backend, nil
	}

	return "", nil
}

func selectRuntimeBackend(required []string) string {
	if len(required) == 0 {
		required = []string{"darwin", "linux"}
	}

	for _, candidate := range required {
		trimmed := strings.ToLower(strings.TrimSpace(candidate))
		switch trimmed {
		case "darwin":
			if runtime.GOOS == "darwin" {
				return "seatbelt"
			}
		case "linux":
			if runtime.GOOS == "linux" {
				return "libkrun"
			}
		default:
			if backend, ok := normalizeRuntimeBackend(candidate); ok {
				return backend
			}
		}
	}

	if runtime.GOOS == "darwin" {
		return "seatbelt"
	}
	if runtime.GOOS == "linux" {
		return "libkrun"
	}

	return ""
}

func normalizeRuntimeBackend(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "libkrun":
		return "libkrun", true
	case "seatbelt":
		return "seatbelt", true
	default:
		return "", false
	}
}

func resolveCheckCommand(projectRoot, command string, args []string, execCtx doctorExecContext) (string, []string, []string, string) {
	_, _ = projectRoot, execCtx
	return command, args, nil, "host"
}

func combineCheckErrors(probeErr, testErr error) error {
	if probeErr == nil {
		return testErr
	}
	if testErr == nil {
		return probeErr
	}
	return fmt.Errorf("%w; %v", probeErr, testErr)
}

func formatCommand(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(command))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\r\"'`$\\") {
		return strconv.Quote(value)
	}
	return value
}

func writeReport(reportPath string, results []checkResult) error {
	if strings.TrimSpace(reportPath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal doctor report: %w", err)
	}
	if err := os.WriteFile(reportPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write doctor report: %w", err)
	}
	fmt.Printf("doctor report written: %s\n", reportPath)
	return nil
}

func parseRequiredPorts(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	ports := make([]int, 0, len(parts))
	seen := map[int]bool{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		port, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid required host port %q", trimmed)
		}
		if port <= 0 || port > 65535 {
			return nil, fmt.Errorf("required host port out of range: %d", port)
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("no required host ports provided")
	}
	return ports, nil
}

func assertNoManualACP(lifecycleDir string) error {
	entries, err := os.ReadDir(lifecycleDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read lifecycle dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(lifecycleDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read lifecycle script %s: %w", path, err)
		}
		if strings.Contains(string(data), "opencode serve") {
			return fmt.Errorf("manual ACP startup found in lifecycle scripts: %s", path)
		}
	}

	return nil
}

func validateLifecycleEntrypoints(projectRoot string) error {
	lifecycleDir := filepath.Join(projectRoot, ".nexus", "lifecycles")
	startPath := filepath.Join(lifecycleDir, "start.sh")

	startExists, err := isExecutableFile(startPath)
	if err != nil {
		return err
	}
	if !startExists && !hasComposeTarget(projectRoot) && !hasMakeTarget(projectRoot, "start") {
		return fmt.Errorf("missing startup entrypoint: expected executable %s or Makefile target 'start'", startPath)
	}

	for _, name := range []string{"setup.sh", "teardown.sh"} {
		path := filepath.Join(lifecycleDir, name)
		_, err := isExecutableFile(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func resolveDoctorChecks(projectRoot string, cfgProbes []config.DoctorCommandProbe, cfgTests []config.DoctorCommandCheck) ([]config.DoctorCommandProbe, []config.DoctorCommandCheck, []string, error) {
	probes, tests, warnings, err := discoverDoctorScripts(projectRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(probes) > 0 || len(tests) > 0 {
		return probes, tests, warnings, nil
	}

	fallbackWarnings := append([]string{}, warnings...)
	fallbackWarnings = append(fallbackWarnings, "no discovery scripts found under .nexus/probe or .nexus/check; falling back to workspace.json doctor.probes/tests")

	return cfgProbes, cfgTests, fallbackWarnings, nil
}

func discoverDoctorScripts(projectRoot string) ([]config.DoctorCommandProbe, []config.DoctorCommandCheck, []string, error) {
	probeDir := filepath.Join(projectRoot, ".nexus", "probe")
	checkDir := filepath.Join(projectRoot, ".nexus", "check")

	probeFiles, probeWarnings, err := collectDiscoveryScripts(probeDir)
	if err != nil {
		return nil, nil, nil, err
	}
	checkFiles, checkWarnings, err := collectDiscoveryScripts(checkDir)
	if err != nil {
		return nil, nil, nil, err
	}

	warnings := append(probeWarnings, checkWarnings...)

	probes := make([]config.DoctorCommandProbe, 0, len(probeFiles))
	for _, file := range probeFiles {
		probes = append(probes, config.DoctorCommandProbe{
			Name:     discoveryScriptName(file),
			Command:  "bash",
			Args:     []string{filepath.ToSlash(filepath.Join(".nexus", "probe", file))},
			Required: true,
		})
	}

	tests := make([]config.DoctorCommandCheck, 0, len(checkFiles))
	for _, file := range checkFiles {
		tests = append(tests, config.DoctorCommandCheck{
			Name:     discoveryScriptName(file),
			Command:  "bash",
			Args:     []string{filepath.ToSlash(filepath.Join(".nexus", "check", file))},
			Required: true,
		})
	}

	return probes, tests, warnings, nil
}

func collectDiscoveryScripts(dir string) ([]string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, []string{fmt.Sprintf("discovery directory not found (optional): %s", dir)}, nil
		}
		return nil, nil, fmt.Errorf("read discovery dir %s: %w", dir, err)
	}

	files := make([]string, 0)
	nonPrefixed := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sh") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		execOK, execErr := isExecutableFile(fullPath)
		if execErr != nil {
			return nil, nil, execErr
		}
		if !execOK {
			continue
		}
		if !hasNumericPrefix(name) {
			nonPrefixed = append(nonPrefixed, name)
		}
		files = append(files, name)
	}

	sortDiscoveryScripts(files)

	warnings := make([]string, 0, len(nonPrefixed))
	for _, file := range nonPrefixed {
		warnings = append(warnings, fmt.Sprintf("discovery script without numeric prefix: %s", filepath.Join(dir, file)))
	}

	return files, warnings, nil
}

func hasNumericPrefix(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return regexp.MustCompile(`^\d+-`).MatchString(base)
}

func sortDiscoveryScripts(files []string) {
	sort.Slice(files, func(i, j int) bool {
		aPrefix, aNum := discoveryPrefix(files[i])
		bPrefix, bNum := discoveryPrefix(files[j])

		if aPrefix && bPrefix {
			if aNum != bNum {
				return aNum < bNum
			}
			return files[i] < files[j]
		}
		if aPrefix != bPrefix {
			return aPrefix
		}
		return files[i] < files[j]
	})
}

func discoveryPrefix(name string) (bool, int) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	parts := strings.SplitN(base, "-", 2)
	if len(parts) < 2 {
		return false, 0
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return false, 0
	}
	return true, n
}

func discoveryScriptName(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	if prefixed, _ := discoveryPrefix(base); prefixed {
		parts := strings.SplitN(base, "-", 2)
		if len(parts) == 2 {
			return parts[1]
		}
	}
	return base
}

func isExecutableFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return false, fmt.Errorf("lifecycle script is not executable: %s", path)
	}
	return true, nil
}

func hasMakeTarget(projectRoot, target string) bool {
	makefilePath := filepath.Join(projectRoot, "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		return false
	}
	pattern := fmt.Sprintf("(?m)^%s\\s*:", regexp.QuoteMeta(target))
	re := regexp.MustCompile(pattern)
	return re.Match(contents)
}

func ensureDotEnv(projectRoot string) error {
	dotEnvPath := filepath.Join(projectRoot, ".env")
	if _, err := os.Stat(dotEnvPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat .env: %w", err)
	}

	dotEnvExamplePath := filepath.Join(projectRoot, ".env.example")
	if _, err := os.Stat(dotEnvExamplePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat .env.example: %w", err)
	}

	data, err := os.ReadFile(dotEnvExamplePath)
	if err != nil {
		return fmt.Errorf("read .env.example: %w", err)
	}
	if err := os.WriteFile(dotEnvPath, data, 0o600); err != nil {
		return fmt.Errorf("write .env from .env.example: %w", err)
	}
	return nil
}

func missingRequiredPorts(required []int, discovered []dockercompose.PublishedPort) []int {
	found := map[int]bool{}
	for _, p := range discovered {
		found[p.HostPort] = true
	}
	missing := make([]int, 0)
	for _, p := range required {
		if !found[p] {
			missing = append(missing, p)
		}
	}
	return missing
}
