package handlers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	cliPath, err := prepareHandlerTestNexusCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "handlers TestMain: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if cliPath != "" {
		_ = os.Remove(cliPath)
	}
	os.Exit(code)
}

func prepareHandlerTestNexusCLI() (cliPath string, err error) {
	if p := strings.TrimSpace(os.Getenv("NEXUS_CLI_PATH")); p != "" {
		st, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("NEXUS_CLI_PATH: %w", err)
		}
		if st.IsDir() {
			return "", fmt.Errorf("NEXUS_CLI_PATH is a directory")
		}
		return "", nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	modRoot, err := goModDir(wd)
	if err != nil {
		return "", err
	}
	cliPath = filepath.Join(os.TempDir(), fmt.Sprintf("nexus-handlers-cli-%d", os.Getpid()))
	build := exec.Command("go", "build", "-o", cliPath, "./cmd/nexus")
	build.Dir = modRoot
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("build nexus CLI: %w", err)
	}
	os.Setenv("NEXUS_CLI_PATH", cliPath)
	return cliPath, nil
}

func goModDir(start string) (string, error) {
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = start
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == "/dev/null" {
		return "", fmt.Errorf("not inside a Go module (cwd=%s)", start)
	}
	return filepath.Dir(mod), nil
}
