package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
	projectcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/project"
	spotlightcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/spotlight"
	vmcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/vm"
	workspacecmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/workspace"
)

type execOptions struct {
	projectRoot string
	timeout     time.Duration
	command     string
	args        []string
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
		vmcmd.Command(),
		execCommand(),
	)
	for _, cmd := range extraCommands {
		root.AddCommand(cmd)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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

func applyRuntimeBackendFromWorkspace(projectRoot string) error {
	_ = projectRoot
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

	backend := selectRuntimeBackend(nil)
	if backend == "" {
		return fmt.Errorf("no supported runtime found; exec supports libkrun or seatbelt")
	}

	if err := os.Setenv("NEXUS_RUNTIME_BACKEND", backend); err != nil {
		return fmt.Errorf("set runtime backend env: %w", err)
	}
	return nil
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

type doctorExecContext struct {
	backend string
}

var execCheckCommandRunner = runCheckCommandWithExecContext

var execCommandBootstrapRunner = bootstrapExecCommandContext

var execKVMGroupReexecRunner = runExecWithKVMGroupReexec

var doctorExecCleanup func() error

var hostDockerSocketStat = os.Stat

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

func resolveCheckCommand(projectRoot, command string, args []string, execCtx doctorExecContext) (string, []string, []string, string) {
	_, _ = projectRoot, execCtx
	return command, args, nil, "host"
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
