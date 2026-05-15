//go:build e2e

package harness

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// PreflightStatus is the result taxonomy from the A1 Lima E2E preflight contract.
type PreflightStatus string

const (
	// PreflightReady means all checks passed and the test environment is ready.
	PreflightReady PreflightStatus = "READY"
	// PreflightMisconfigured means a required env var, path, or tooling setting
	// is missing or invalid. The user must fix their local configuration.
	PreflightMisconfigured PreflightStatus = "MISCONFIGURED"
	// PreflightUnsupportedHost means the host/backend combination cannot
	// satisfy the target VM behavior (e.g. darwin, no /dev/kvm).
	PreflightUnsupportedHost PreflightStatus = "UNSUPPORTED_HOST"
	// PreflightBootstrapFailed means the daemon/profile bootstrap was attempted
	// but did not become healthy.
	PreflightBootstrapFailed PreflightStatus = "BOOTSTRAP_FAILED"
)

// preflightSeverity returns a numeric severity for status ordering.
// Higher = worse. RunPreflight uses the worst-case status across all checks.
func preflightSeverity(s PreflightStatus) int {
	switch s {
	case PreflightBootstrapFailed:
		return 4
	case PreflightUnsupportedHost:
		return 3
	case PreflightMisconfigured:
		return 2
	case PreflightReady:
		return 1
	default:
		return 0
	}
}

// CheckResult is the result of a single A1 preflight check.
// When Status == PreflightReady all other fields may be empty.
// When Status != PreflightReady all four fields MUST be populated.
type CheckResult struct {
	// ID is a short stable identifier for the check (e.g. "tooling").
	ID string
	// Status is the check outcome.
	Status PreflightStatus
	// Observed is what the check found (e.g. env var values, missing path).
	Observed string
	// Expected is what the check required.
	Expected string
	// Remediation is a single actionable sentence the user can act on immediately.
	Remediation string
}

// PreflightResult is the aggregate result of RunPreflight.
type PreflightResult struct {
	// Status is the worst-case status across all checks.
	// READY only when every check passed.
	Status PreflightStatus
	// Checks contains one entry per executed check, in run order.
	Checks []CheckResult
}

// EnvReader is the seam that RunPreflight uses to access the OS environment.
// Inject a fake in tests; use RealEnvReader{} in production.
type EnvReader interface {
	Getenv(key string) string
	Stat(path string) (os.FileInfo, error)
	LookPath(file string) (string, error)
	GOOS() string
}

// RealEnvReader delegates to the real OS APIs.
type RealEnvReader struct{}

func (RealEnvReader) Getenv(key string) string              { return os.Getenv(key) }
func (RealEnvReader) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (RealEnvReader) LookPath(file string) (string, error)  { return exec.LookPath(file) }
func (RealEnvReader) GOOS() string                          { return runtime.GOOS }

// e2eDarwinVMMode is true when macOS E2E targets the Hypervisor.framework VM backend.
func e2eDarwinVMMode(env EnvReader) bool {
	return env.GOOS() == "darwin" &&
		strings.EqualFold(strings.TrimSpace(env.Getenv("NEXUS_E2E_DRIVER")), "vm")
}

// RunPreflight executes all A1 contract checks and returns the aggregate result.
// env is the EnvReader to use; pass RealEnvReader{} in non-test code.
func RunPreflight(env EnvReader) PreflightResult {
	checks := []CheckResult{
		checkTooling(env),
		checkRuntimeArtifacts(env),
		checkDaemonDiscoverability(env),
		checkVMBackend(env),
		checkDirectoryWritability(env),
	}

	worst := PreflightReady
	for _, c := range checks {
		if preflightSeverity(c.Status) > preflightSeverity(worst) {
			worst = c.Status
		}
	}
	return PreflightResult{Status: worst, Checks: checks}
}

// preflightTB is the minimal subset of testing.TB used by EnforcePreflightPolicy.
// *testing.T satisfies it; test fakes can too.
type preflightTB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// EnforcePreflightPolicy maps a PreflightResult to a test action:
//   - READY             → no-op; test continues.
//   - UNSUPPORTED_HOST  → prints report to stderr and calls t.Skipf (platform cannot run VM tests).
//   - MISCONFIGURED     → prints report to stderr and calls t.Fatalf (environment is fixable).
//   - BOOTSTRAP_FAILED  → prints report to stderr and calls t.Fatalf (bootstrap attempted but unhealthy).
//
// Call this immediately after RunPreflight to gate any test that requires a VM backend.
func EnforcePreflightPolicy(t preflightTB, r PreflightResult) {
	t.Helper()
	if r.Status == PreflightReady {
		return
	}
	PrintPreflightReport(os.Stderr, r)
	switch r.Status {
	case PreflightUnsupportedHost:
		t.Skipf("[preflight] UNSUPPORTED_HOST: this host cannot run VM-backed tests (see report above)")
	case PreflightMisconfigured:
		t.Fatalf("[preflight] MISCONFIGURED: fix the configuration issues above and retry")
	case PreflightBootstrapFailed:
		t.Fatalf("[preflight] BOOTSTRAP_FAILED: daemon or profile bootstrap is unhealthy (see report above)")
	default:
		t.Fatalf("[preflight] unknown status %q — treat as fatal", r.Status)
	}
}

// PrintPreflightReport writes a human-readable report to w.
// For READY results it prints a single summary line.
// For non-READY results it prints a table of failing checks with diagnostics.
func PrintPreflightReport(w io.Writer, r PreflightResult) {
	if r.Status == PreflightReady {
		fmt.Fprintf(w, "[preflight] READY: all checks passed\n")
		return
	}
	fmt.Fprintf(w, "[preflight] status=%s\n", r.Status)
	for _, c := range r.Checks {
		if c.Status == PreflightReady {
			continue
		}
		fmt.Fprintf(w, "  [%s] status=%s\n", c.ID, c.Status)
		fmt.Fprintf(w, "    observed:    %s\n", c.Observed)
		fmt.Fprintf(w, "    expected:    %s\n", c.Expected)
		fmt.Fprintf(w, "    remediation: %s\n", c.Remediation)
	}
}

// ─── individual checks ────────────────────────────────────────────────────────

// checkTooling verifies that a Go toolchain is reachable in the test guest.
// Accepts either a plain `go` binary or a `mise` shim that resolves to one.
func checkTooling(env EnvReader) CheckResult {
	const id = "tooling"
	goPath, err := env.LookPath("go")
	if err == nil && goPath != "" {
		return CheckResult{ID: id, Status: PreflightReady}
	}
	// Fallback: mise shim may satisfy the requirement even if `go` is not on PATH.
	misePath, _ := env.LookPath("mise")
	if misePath != "" {
		// Presence of mise is enough to satisfy the shim check; the actual
		// resolution is validated at runtime by the test harness.
		return CheckResult{ID: id, Status: PreflightReady}
	}
	return CheckResult{
		ID:          id,
		Status:      PreflightMisconfigured,
		Observed:    "go not found in PATH; mise not found in PATH",
		Expected:    "go executable reachable via PATH, or mise shim installed",
		Remediation: "install Go (https://go.dev/dl/) or mise (https://mise.jdx.dev/) and ensure your PATH is configured",
	}
}

// checkRuntimeArtifacts verifies NEXUS_VM_KERNEL and NEXUS_VM_ROOTFS are set
// and the referenced paths exist on disk.
func checkRuntimeArtifacts(env EnvReader) CheckResult {
	const id = "runtime-artifacts"
	if e2eDarwinVMMode(env) {
		root := strings.TrimSpace(env.Getenv("NEXUS_VM_ROOTFS"))
		if root == "" {
			root = strings.TrimSpace(env.Getenv("NEXUS_E2E_ROOTFS"))
		}
		if root == "" {
			return CheckResult{
				ID:          id,
				Status:      PreflightMisconfigured,
				Observed:    "NEXUS_VM_ROOTFS and NEXUS_E2E_ROOTFS are unset",
				Expected:    "path to a pre-built arm64 guest ext4 (e.g. ~/.cache/nexus/vm/rootfs.ext4)",
				Remediation: "export NEXUS_VM_ROOTFS=$HOME/.cache/nexus/vm/rootfs.ext4 or run scripts/ci/ensure-macos-e2e-rootfs.sh",
			}
		}
		if _, err := env.Stat(root); err != nil {
			return CheckResult{
				ID:          id,
				Status:      PreflightMisconfigured,
				Observed:    fmt.Sprintf("NEXUS_VM_ROOTFS=%q (not found)", root),
				Expected:    "ext4 rootfs file must exist",
				Remediation: "build the cache with scripts/ci/ensure-macos-e2e-rootfs.sh or set NEXUS_VM_ROOTFS to a valid path",
			}
		}
		return CheckResult{ID: id, Status: PreflightReady}
	}

	kernel := env.Getenv("NEXUS_VM_KERNEL")
	rootfs := env.Getenv("NEXUS_VM_ROOTFS")

	missing := ""
	if kernel == "" && rootfs == "" {
		missing = "NEXUS_VM_KERNEL and NEXUS_VM_ROOTFS are unset"
	} else if kernel == "" {
		missing = "NEXUS_VM_KERNEL is unset"
	} else if rootfs == "" {
		missing = "NEXUS_VM_ROOTFS is unset"
	}
	if missing != "" {
		return CheckResult{
			ID:          id,
			Status:      PreflightMisconfigured,
			Observed:    fmt.Sprintf("NEXUS_VM_KERNEL=%q NEXUS_VM_ROOTFS=%q", kernel, rootfs),
			Expected:    "both env vars set and paths must exist on disk",
			Remediation: "export NEXUS_VM_KERNEL=\"$HOME/.local/share/nexus/vm/vmlinux.bin\" NEXUS_VM_ROOTFS=\"$HOME/.local/share/nexus/vm/rootfs.ext4\" (or under $XDG_DATA_HOME/nexus/vm); nexusd mirrors them under each instance's --workdir-root/.nexus-vm",
		}
	}

	// Both env vars are set — now verify the paths exist.
	var notFound []string
	if _, err := env.Stat(kernel); err != nil {
		notFound = append(notFound, fmt.Sprintf("NEXUS_VM_KERNEL=%q (not found)", kernel))
	}
	if _, err := env.Stat(rootfs); err != nil {
		notFound = append(notFound, fmt.Sprintf("NEXUS_VM_ROOTFS=%q (not found)", rootfs))
	}
	if len(notFound) > 0 {
		observed := ""
		for i, s := range notFound {
			if i > 0 {
				observed += "; "
			}
			observed += s
		}
		return CheckResult{
			ID:          id,
			Status:      PreflightMisconfigured,
			Observed:    observed,
			Expected:    "both paths must exist on disk",
			Remediation: "provision the VM assets with `task provision` or download them to the configured paths",
		}
	}
	return CheckResult{ID: id, Status: PreflightReady}
}

// checkDaemonDiscoverability verifies that the daemon socket or profile path is
// discoverable. In practice, NEXUS_E2E_BINARY or XDG_STATE_HOME/nexus must be
// reachable.
func checkDaemonDiscoverability(env EnvReader) CheckResult {
	const id = "daemon-bootstrap"
	binary := env.Getenv("NEXUS_E2E_BINARY")
	if binary == "" {
		// Fallback: check if `nexus` binary is on PATH.
		p, _ := env.LookPath("nexus")
		binary = p
	}
	if binary == "" {
		return CheckResult{
			ID:          id,
			Status:      PreflightMisconfigured,
			Observed:    "NEXUS_E2E_BINARY is unset; nexus not found in PATH",
			Expected:    "NEXUS_E2E_BINARY set to a valid nexus binary path, or nexus binary on PATH",
			Remediation: "export NEXUS_E2E_BINARY=$HOME/.local/bin/nexus or add the binary to your PATH",
		}
	}
	if _, err := env.Stat(binary); err != nil {
		return CheckResult{
			ID:          id,
			Status:      PreflightMisconfigured,
			Observed:    fmt.Sprintf("binary path %q does not exist", binary),
			Expected:    "binary path must exist and be executable",
			Remediation: fmt.Sprintf("build and deploy the nexus binary to %s (run `task dev:remote`)", binary),
		}
	}
	return CheckResult{ID: id, Status: PreflightReady}
}

// checkVMBackend verifies that the host can support VM-backed tests.
// darwin succeeds only for NEXUS_E2E_DRIVER=vm (macOS VM / Hypervisor.framework).
// Linux requires /dev/kvm for libkrun VMs.
func checkVMBackend(env EnvReader) CheckResult {
	const id = "vm-backend"
	goos := env.GOOS()
	if e2eDarwinVMMode(env) {
		return CheckResult{ID: id, Status: PreflightReady}
	}
	if goos == "darwin" {
		return CheckResult{
			ID:          id,
			Status:      PreflightUnsupportedHost,
			Observed:    "GOOS=darwin (set NEXUS_E2E_DRIVER=vm for macOS VM E2E)",
			Expected:    "linux host with /dev/kvm, or macOS with NEXUS_E2E_DRIVER=vm",
			Remediation: "export NEXUS_E2E_DRIVER=vm and NEXUS_VM_ROOTFS, or run on Linux with KVM",
		}
	}
	if _, err := env.Stat("/dev/kvm"); err != nil {
		return CheckResult{
			ID:          id,
			Status:      PreflightUnsupportedHost,
			Observed:    "/dev/kvm not accessible",
			Expected:    "/dev/kvm exists and is accessible by the test user",
			Remediation: "enable KVM on the host (modprobe kvm) and ensure the user is in the kvm group",
		}
	}
	return CheckResult{ID: id, Status: PreflightReady}
}

// checkDirectoryWritability verifies that directories used by E2E tests are writable.
// Checks /tmp (always needed) and /data/nexus (used when available for large VM files).
func checkDirectoryWritability(env EnvReader) CheckResult {
	const id = "directory-writability"
	dirs := []string{"/tmp"}
	// /data/nexus is optional but preferred for large test artifacts.
	if _, err := env.Stat("/data/nexus"); err == nil {
		dirs = append(dirs, "/data/nexus")
	}

	for _, dir := range dirs {
		f, err := os.CreateTemp(dir, ".nexus-preflight-probe-*")
		if err != nil {
			return CheckResult{
				ID:          id,
				Status:      PreflightMisconfigured,
				Observed:    fmt.Sprintf("directory %q is not writable: %v", dir, err),
				Expected:    "test directories must be writable by the current user",
				Remediation: fmt.Sprintf("fix permissions on %s: chmod o+w %s or run as a user with write access", dir, dir),
			}
		}
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	return CheckResult{ID: id, Status: PreflightReady}
}
