//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	dir, err := os.MkdirTemp("", "nexus-e2e-workdir-*")
	if err != nil {
		t.Fatalf("TempWorkdir: mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
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
