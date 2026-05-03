//go:build e2e

package harness

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeEnv is a test double for EnvReader.
type fakeEnv struct {
	env      map[string]string
	existing map[string]bool // paths that "exist" on the fake filesystem
	goos     string
}

func newFakeEnv(goos string) *fakeEnv {
	return &fakeEnv{
		env:      make(map[string]string),
		existing: make(map[string]bool),
		goos:     goos,
	}
}

func (f *fakeEnv) Getenv(key string) string { return f.env[key] }
func (f *fakeEnv) GOOS() string             { return f.goos }
func (f *fakeEnv) Stat(path string) (os.FileInfo, error) {
	if f.existing[path] {
		return nil, nil // non-nil FileInfo not needed for the checks
	}
	return nil, fmt.Errorf("stat %s: no such file or directory", path)
}
func (f *fakeEnv) LookPath(file string) (string, error) {
	if p, ok := f.env["PATH:"+file]; ok {
		return p, nil
	}
	return "", fmt.Errorf("%s: not found", file)
}

// helpers to set fake values
func (f *fakeEnv) setEnv(k, v string) *fakeEnv         { f.env[k] = v; return f }
func (f *fakeEnv) setInPath(bin, path string) *fakeEnv { f.env["PATH:"+bin] = path; return f }
func (f *fakeEnv) setExists(paths ...string) *fakeEnv {
	for _, p := range paths {
		f.existing[p] = true
	}
	return f
}

// ─── checkTooling ────────────────────────────────────────────────────────────

func TestCheckTooling_GoOnPath(t *testing.T) {
	env := newFakeEnv("linux").setInPath("go", "/usr/local/go/bin/go")
	r := checkTooling(env)
	if r.Status != PreflightReady {
		t.Errorf("expected READY, got %s: %s", r.Status, r.Observed)
	}
}

func TestCheckTooling_MiseOnPath(t *testing.T) {
	env := newFakeEnv("linux").setInPath("mise", "/home/user/.local/bin/mise")
	r := checkTooling(env)
	if r.Status != PreflightReady {
		t.Errorf("expected READY with mise, got %s: %s", r.Status, r.Observed)
	}
}

func TestCheckTooling_NeitherOnPath(t *testing.T) {
	env := newFakeEnv("linux")
	r := checkTooling(env)
	if r.Status != PreflightMisconfigured {
		t.Errorf("expected MISCONFIGURED, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

// ─── checkRuntimeArtifacts ───────────────────────────────────────────────────

func TestCheckRuntimeArtifacts_BothSetAndExist(t *testing.T) {
	env := newFakeEnv("linux").
		setEnv("NEXUS_VM_KERNEL", "/data/nexus/vm/vmlinux.bin").
		setEnv("NEXUS_VM_ROOTFS", "/data/nexus/vm/rootfs.ext4").
		setExists("/data/nexus/vm/vmlinux.bin", "/data/nexus/vm/rootfs.ext4")
	r := checkRuntimeArtifacts(env)
	if r.Status != PreflightReady {
		t.Errorf("expected READY, got %s: %s", r.Status, r.Observed)
	}
}

func TestCheckRuntimeArtifacts_BothUnset(t *testing.T) {
	env := newFakeEnv("linux")
	r := checkRuntimeArtifacts(env)
	if r.Status != PreflightMisconfigured {
		t.Errorf("expected MISCONFIGURED, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

func TestCheckRuntimeArtifacts_KernelUnset(t *testing.T) {
	env := newFakeEnv("linux").setEnv("NEXUS_VM_ROOTFS", "/data/nexus/vm/rootfs.ext4")
	r := checkRuntimeArtifacts(env)
	if r.Status != PreflightMisconfigured {
		t.Errorf("expected MISCONFIGURED, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

func TestCheckRuntimeArtifacts_PathsSetButMissing(t *testing.T) {
	env := newFakeEnv("linux").
		setEnv("NEXUS_VM_KERNEL", "/data/nexus/vm/vmlinux.bin").
		setEnv("NEXUS_VM_ROOTFS", "/data/nexus/vm/rootfs.ext4")
	// Paths NOT added to existing — simulate missing files.
	r := checkRuntimeArtifacts(env)
	if r.Status != PreflightMisconfigured {
		t.Errorf("expected MISCONFIGURED, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

// ─── checkDaemonDiscoverability ──────────────────────────────────────────────

func TestCheckDaemonDiscoverability_BinaryEnvSet(t *testing.T) {
	env := newFakeEnv("linux").
		setEnv("NEXUS_E2E_BINARY", "/home/user/.local/bin/nexus").
		setExists("/home/user/.local/bin/nexus")
	r := checkDaemonDiscoverability(env)
	if r.Status != PreflightReady {
		t.Errorf("expected READY, got %s: %s", r.Status, r.Observed)
	}
}

func TestCheckDaemonDiscoverability_BinaryOnPath(t *testing.T) {
	env := newFakeEnv("linux").
		setInPath("nexus", "/home/user/.local/bin/nexus").
		setExists("/home/user/.local/bin/nexus")
	r := checkDaemonDiscoverability(env)
	if r.Status != PreflightReady {
		t.Errorf("expected READY via PATH, got %s: %s", r.Status, r.Observed)
	}
}

func TestCheckDaemonDiscoverability_NoBinary(t *testing.T) {
	env := newFakeEnv("linux")
	r := checkDaemonDiscoverability(env)
	if r.Status != PreflightMisconfigured {
		t.Errorf("expected MISCONFIGURED, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

func TestCheckDaemonDiscoverability_BinaryPathMissing(t *testing.T) {
	env := newFakeEnv("linux").
		setEnv("NEXUS_E2E_BINARY", "/home/user/.local/bin/nexus")
	// Path not in existing.
	r := checkDaemonDiscoverability(env)
	if r.Status != PreflightMisconfigured {
		t.Errorf("expected MISCONFIGURED, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

// ─── checkVMBackend ──────────────────────────────────────────────────────────

func TestCheckVMBackend_Darwin(t *testing.T) {
	env := newFakeEnv("darwin")
	r := checkVMBackend(env)
	if r.Status != PreflightUnsupportedHost {
		t.Errorf("expected UNSUPPORTED_HOST on darwin, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

func TestCheckVMBackend_LinuxWithKVM(t *testing.T) {
	env := newFakeEnv("linux").setExists("/dev/kvm")
	r := checkVMBackend(env)
	if r.Status != PreflightReady {
		t.Errorf("expected READY on linux+kvm, got %s: %s", r.Status, r.Observed)
	}
}

func TestCheckVMBackend_LinuxNoKVM(t *testing.T) {
	env := newFakeEnv("linux")
	r := checkVMBackend(env)
	if r.Status != PreflightUnsupportedHost {
		t.Errorf("expected UNSUPPORTED_HOST without kvm, got %s", r.Status)
	}
	requireDiagnosticFields(t, r)
}

// ─── RunPreflight aggregation ────────────────────────────────────────────────

func TestRunPreflight_AllReady(t *testing.T) {
	env := newFakeEnv("linux").
		setInPath("go", "/usr/local/go/bin/go").
		setEnv("NEXUS_VM_KERNEL", "/k").
		setEnv("NEXUS_VM_ROOTFS", "/r").
		setEnv("NEXUS_E2E_BINARY", "/bin/nexus").
		setExists("/k", "/r", "/bin/nexus", "/dev/kvm")
	// Note: checkDirectoryWritability uses real os.CreateTemp — /tmp is always writable in CI.
	result := RunPreflight(env)
	if result.Status != PreflightReady {
		t.Errorf("expected READY, got %s", result.Status)
		for _, c := range result.Checks {
			if c.Status != PreflightReady {
				t.Logf("  failing check [%s]: observed=%q expected=%q remediation=%q",
					c.ID, c.Observed, c.Expected, c.Remediation)
			}
		}
	}
	if len(result.Checks) != 5 {
		t.Errorf("expected 5 checks, got %d", len(result.Checks))
	}
}

func TestRunPreflight_WorstCaseStatusWins(t *testing.T) {
	// Only vm-backend and tooling will fail; vm-backend gives UNSUPPORTED_HOST (worse).
	env := newFakeEnv("darwin")
	// runtime artifacts are unset → MISCONFIGURED
	// vm-backend is darwin → UNSUPPORTED_HOST
	result := RunPreflight(env)
	if result.Status != PreflightUnsupportedHost {
		t.Errorf("expected UNSUPPORTED_HOST (worst-case wins), got %s", result.Status)
	}
}

// ─── PrintPreflightReport ────────────────────────────────────────────────────

func TestPrintPreflightReport_Ready(t *testing.T) {
	r := PreflightResult{Status: PreflightReady}
	var buf bytes.Buffer
	PrintPreflightReport(&buf, r)
	if !strings.Contains(buf.String(), "READY") {
		t.Errorf("expected READY in output, got: %s", buf.String())
	}
}

func TestPrintPreflightReport_Failing(t *testing.T) {
	r := PreflightResult{
		Status: PreflightMisconfigured,
		Checks: []CheckResult{
			{
				ID:          "runtime-artifacts",
				Status:      PreflightMisconfigured,
				Observed:    `NEXUS_VM_KERNEL="" NEXUS_VM_ROOTFS=""`,
				Expected:    "both env vars set and paths must exist on disk",
				Remediation: "export NEXUS_VM_KERNEL=... NEXUS_VM_ROOTFS=...",
			},
		},
	}
	var buf bytes.Buffer
	PrintPreflightReport(&buf, r)
	out := buf.String()
	for _, want := range []string{"MISCONFIGURED", "runtime-artifacts", "observed:", "expected:", "remediation:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in report output, got:\n%s", want, out)
		}
	}
}

// ─── fakeTB ──────────────────────────────────────────────────────────────────

// fakeTB is a test double for preflightTB that records the last call made.
type fakeTB struct {
	skipped bool
	fataled bool
	msg     string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Skipf(format string, args ...any) {
	f.skipped = true
	f.msg = fmt.Sprintf(format, args...)
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fataled = true
	f.msg = fmt.Sprintf(format, args...)
}

// ─── EnforcePreflightPolicy ─────────────────────────────────────────────────

func TestEnforcePreflightPolicy_Ready(t *testing.T) {
	// READY → no skip, no fatal; test continues normally.
	r := PreflightResult{Status: PreflightReady}
	EnforcePreflightPolicy(t, r) // must not call t.Skip or t.Fatal
}

// TestEnforcePreflightPolicy_UnsupportedHost verifies that an UNSUPPORTED_HOST
// result causes the sub-test to be marked as skipped.
func TestEnforcePreflightPolicy_UnsupportedHost(t *testing.T) {
	r := PreflightResult{
		Status: PreflightUnsupportedHost,
		Checks: []CheckResult{
			{
				ID:          "vm-backend",
				Status:      PreflightUnsupportedHost,
				Observed:    "GOOS=darwin",
				Expected:    "linux host with /dev/kvm",
				Remediation: "use a Linux host",
			},
		},
	}
	// Run in a sub-test so the skip is isolated.
	t.Run("policy-skip", func(tt *testing.T) {
		EnforcePreflightPolicy(tt, r)
		// If we reach here EnforcePreflightPolicy did not skip — fail outer.
		t.Error("EnforcePreflightPolicy should have called t.Skip for UNSUPPORTED_HOST")
	})
}

// TestEnforcePreflightPolicy_Misconfigured verifies that a MISCONFIGURED result
// causes t.Fatalf to be called (fixable environment issue).
func TestEnforcePreflightPolicy_Misconfigured(t *testing.T) {
	r := PreflightResult{
		Status: PreflightMisconfigured,
		Checks: []CheckResult{
			{
				ID:          "runtime-artifacts",
				Status:      PreflightMisconfigured,
				Observed:    `NEXUS_VM_KERNEL="" NEXUS_VM_ROOTFS=""`,
				Expected:    "both env vars set and paths must exist on disk",
				Remediation: "export NEXUS_VM_KERNEL=... NEXUS_VM_ROOTFS=...",
			},
		},
	}
	tb := &fakeTB{}
	EnforcePreflightPolicy(tb, r)
	if !tb.fataled {
		t.Error("EnforcePreflightPolicy should have called Fatalf for MISCONFIGURED")
	}
	if tb.skipped {
		t.Error("EnforcePreflightPolicy must not call Skipf for MISCONFIGURED")
	}
	if !strings.Contains(tb.msg, "MISCONFIGURED") {
		t.Errorf("expected MISCONFIGURED in fatal message, got: %s", tb.msg)
	}
}

// TestEnforcePreflightPolicy_BootstrapFailed verifies that a BOOTSTRAP_FAILED result
// causes t.Fatalf to be called (daemon/profile bootstrap is unhealthy).
func TestEnforcePreflightPolicy_BootstrapFailed(t *testing.T) {
	r := PreflightResult{
		Status: PreflightBootstrapFailed,
		Checks: []CheckResult{
			{
				ID:          "daemon-health",
				Status:      PreflightBootstrapFailed,
				Observed:    "daemon did not become healthy within 30s",
				Expected:    "daemon healthy within 30s",
				Remediation: "check daemon logs with `nexus logs`",
			},
		},
	}
	tb := &fakeTB{}
	EnforcePreflightPolicy(tb, r)
	if !tb.fataled {
		t.Error("EnforcePreflightPolicy should have called Fatalf for BOOTSTRAP_FAILED")
	}
	if tb.skipped {
		t.Error("EnforcePreflightPolicy must not call Skipf for BOOTSTRAP_FAILED")
	}
	if !strings.Contains(tb.msg, "BOOTSTRAP_FAILED") {
		t.Errorf("expected BOOTSTRAP_FAILED in fatal message, got: %s", tb.msg)
	}
}

// TestEnforcePreflightPolicy_ReportEmittedOnFailure verifies that a non-READY
// result writes diagnostic text to stderr before the skip/fatal action.
// We validate this indirectly by confirming PrintPreflightReport produces the
// expected tokens for a MISCONFIGURED result (unit-level; no real t.Fatal).
func TestEnforcePreflightPolicy_ReportEmittedOnFailure(t *testing.T) {
	r := PreflightResult{
		Status: PreflightMisconfigured,
		Checks: []CheckResult{
			{
				ID:          "runtime-artifacts",
				Status:      PreflightMisconfigured,
				Observed:    `NEXUS_VM_KERNEL="" NEXUS_VM_ROOTFS=""`,
				Expected:    "both env vars set and paths must exist on disk",
				Remediation: "export NEXUS_VM_KERNEL=...",
			},
		},
	}
	// Validate that PrintPreflightReport (called by EnforcePreflightPolicy) produces
	// correct output; we do not call EnforcePreflightPolicy directly here because
	// it would fatalf the outer test.
	var buf bytes.Buffer
	PrintPreflightReport(&buf, r)
	out := buf.String()
	for _, want := range []string{"MISCONFIGURED", "runtime-artifacts", "observed:", "remediation:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in preflight report, got:\n%s", want, out)
		}
	}
}

// ─── helper ──────────────────────────────────────────────────────────────────

// requireDiagnosticFields asserts that all four A1 diagnostic fields are non-empty
// for a non-READY check result.
func requireDiagnosticFields(t *testing.T, r CheckResult) {
	t.Helper()
	if r.ID == "" {
		t.Error("CheckResult.ID must not be empty")
	}
	if r.Observed == "" {
		t.Errorf("[%s] CheckResult.Observed must not be empty (status=%s)", r.ID, r.Status)
	}
	if r.Expected == "" {
		t.Errorf("[%s] CheckResult.Expected must not be empty (status=%s)", r.ID, r.Status)
	}
	if r.Remediation == "" {
		t.Errorf("[%s] CheckResult.Remediation must not be empty (status=%s)", r.ID, r.Status)
	}
}
