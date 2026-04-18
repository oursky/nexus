package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPrintProjectUsageContainsSubcommands(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	printProjectUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf)

	for _, want := range []string{"list", "create", "get", "remove"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected usage to contain %q, got:\n%s", want, got)
		}
	}
}

func TestPrintProjectUsageContainsFlagsDescription(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	printProjectUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf)

	if !strings.Contains(got, "--name") {
		t.Errorf("expected usage to contain --name flag, got:\n%s", got)
	}
	if !strings.Contains(got, "--json") {
		t.Errorf("expected usage to contain --json flag, got:\n%s", got)
	}
	if !strings.Contains(got, "--force") {
		t.Errorf("expected usage to contain --force flag, got:\n%s", got)
	}
}

// runSubprocess runs the test binary itself with a special env var so we can
// exercise os.Exit paths without killing the parent test process.
func runSubprocess(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "NEXUS_SUBPROCESS_TEST=1")

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout, stderr, exitCode
}

func TestProjectCreateRequiresNameFlag(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runProjectCreateCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestProjectCreateRequiresNameFlag")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --name, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--name is required") {
		t.Errorf("expected stderr to mention --name is required, got:\n%s", stderr)
	}
}

func TestProjectGetRequiresIDArgument(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runProjectGetCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestProjectGetRequiresIDArgument")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing id arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus project get") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestProjectRemoveRequiresIDArgument(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runProjectRemoveCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestProjectRemoveRequiresIDArgument")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing id arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus project remove") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestProjectCommandNoArgsExits(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runProjectCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestProjectCommandNoArgsExits")
	if code != 2 {
		t.Fatalf("expected exit code 2 for no args, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus project") {
		t.Errorf("expected stderr to include usage, got:\n%s", stderr)
	}
}

func TestProjectCommandUnknownSubcommandExits(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runProjectCommand([]string{"unknowncmd"})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestProjectCommandUnknownSubcommandExits")
	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown subcommand, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "unknown project subcommand") {
		t.Errorf("expected stderr to mention unknown subcommand, got:\n%s", stderr)
	}
}
