//go:build e2e

package harness

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// RequireVM skips the test when the libkrun VM backend is not available.
//
//   - macOS/darwin: libkrun VM is not supported on macOS — always skip.
//   - Linux: skip when NEXUS_VM_KERNEL or NEXUS_VM_ROOTFS are unset.
//
// Call this at the top of every test that needs a real VM backend.
// Tests that work with the sandbox backend should NOT call this.
func RequireVM(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("VM backend is not supported on macOS; skipping VM-specific test")
	}
	kernel := os.Getenv("NEXUS_VM_KERNEL")
	rootfs := os.Getenv("NEXUS_VM_ROOTFS")
	if kernel == "" || rootfs == "" {
		t.Skip("VM not configured (NEXUS_VM_KERNEL/ROOTFS not set); skipping VM-specific test")
	}
}

// SkipIfVMBoot skips the test when running in short mode (-short flag).
// Call this at the top of any test that calls workspace.start (which triggers
// mkfs.ext4 + VM boot and takes 3-5 minutes each).
func SkipIfVMBoot(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("slow: VM boot — run without -short to include")
	}
}

func TempDB(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nexus-e2e-db-*")
	if err != nil {
		t.Fatalf("TempDB: mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "nexus.db")
}

func TempSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nexus-e2e-sock-*")
	if err != nil {
		t.Fatalf("TempSocket: mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "nexusd.sock")
}

func TempWorkdir(t *testing.T) string {
	t.Helper()
	base := ""
	if _, err := os.Stat("/data/nexus"); err == nil {
		base = "/data/nexus/libkrun-vms-e2e"
		_ = os.MkdirAll(base, 0o755)
	}
	dir, err := os.MkdirTemp(base, "nexus-e2e-workdir-*")
	if err != nil {
		t.Fatalf("TempWorkdir: mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// FreePort returns a free TCP port on localhost.
func FreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// MakeLocalGitRepo creates a temp directory with an initialized git repo on branch main.
func MakeLocalGitRepo(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nexus-e2e-repo-"+name+"-*")
	if err != nil {
		t.Fatalf("MakeLocalGitRepo: mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("MakeLocalGitRepo %v: %v\n%s", args, err, out)
		}
	}

	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		cmd2 := exec.Command("git", "init")
		cmd2.Dir = dir
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			t.Fatalf("MakeLocalGitRepo git init: %v\n%s", err2, out2)
		}
		_ = out
		run("git", "checkout", "-b", "main")
	}
	run("git", "config", "user.email", "test@test.local")
	run("git", "config", "user.name", "Test")
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# test\n"), 0644); err != nil {
		t.Fatalf("MakeLocalGitRepo: write README: %v", err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "init")

	return dir
}

// MakeGitRepoWithContent creates a temp git repo on branch main with the given files.
// files is a map of relative path → content.
func MakeGitRepoWithContent(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := MakeLocalGitRepo(t, name)
	for relPath, content := range files {
		fullPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MakeGitRepoWithContent: mkdir %s: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("MakeGitRepoWithContent: write %s: %v", relPath, err)
		}
	}
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("MakeGitRepoWithContent git add: %v\n%s", err, out)
	}
	if len(files) > 0 {
		cmd2 := exec.Command("git", "commit", "-m", "add test files")
		cmd2.Dir = dir
		if out, err := cmd2.CombinedOutput(); err != nil {
			t.Fatalf("MakeGitRepoWithContent git commit: %v\n%s", err, out)
		}
	}
	return dir
}
