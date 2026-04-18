package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintSpotlightUsageContainsSubcommands(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	printSpotlightUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf)

	for _, want := range []string{"start", "list", "stop", "port"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected spotlight usage to contain %q, got:\n%s", want, got)
		}
	}
}

func TestPrintSpotlightUsageContainsRequiredFlags(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	printSpotlightUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf)

	if !strings.Contains(got, "--workspace") {
		t.Errorf("expected spotlight usage to contain --workspace, got:\n%s", got)
	}
	if !strings.Contains(got, "--local-port") {
		t.Errorf("expected spotlight usage to contain --local-port, got:\n%s", got)
	}
}

func TestPrintSpotlightPortUsageContainsSubcommands(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	printSpotlightPortUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf)

	for _, want := range []string{"list", "add", "remove"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected spotlight port usage to contain %q, got:\n%s", want, got)
		}
	}
}

func TestSpotlightStartRequiresWorkspaceAndLocalPort(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightStartCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightStartRequiresWorkspaceAndLocalPort")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing flags, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--workspace and --local-port are required") {
		t.Errorf("expected stderr to mention required flags, got:\n%s", stderr)
	}
}

func TestSpotlightStartRequiresLocalPort(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightStartCommand([]string{"--workspace", "ws-123"})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightStartRequiresLocalPort")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --local-port, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--workspace and --local-port are required") {
		t.Errorf("expected stderr to mention required flags, got:\n%s", stderr)
	}
}

func TestSpotlightListRequiresWorkspace(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightListCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightListRequiresWorkspace")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --workspace, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--workspace is required") {
		t.Errorf("expected stderr to mention --workspace is required, got:\n%s", stderr)
	}
}

func TestSpotlightCloseRequiresID(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightStopCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightCloseRequiresID")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing id, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus spotlight stop") {
		t.Errorf("expected stderr to mention usage, got:\n%s", stderr)
	}
}

func TestSpotlightCommandNoArgsExits(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightCommandNoArgsExits")
	if code != 2 {
		t.Fatalf("expected exit code 2 for no args, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus spotlight") {
		t.Errorf("expected stderr to include usage, got:\n%s", stderr)
	}
}

func TestSpotlightCommandUnknownSubcommandExits(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightCommand([]string{"unknowncmd"})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightCommandUnknownSubcommandExits")
	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown subcommand, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "unknown spotlight subcommand") {
		t.Errorf("expected stderr to mention unknown subcommand, got:\n%s", stderr)
	}
}

func TestSpotlightPortCommandNoArgsExits(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightPortCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightPortCommandNoArgsExits")
	if code != 2 {
		t.Fatalf("expected exit code 2 for no args, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus spotlight port") {
		t.Errorf("expected stderr to include usage, got:\n%s", stderr)
	}
}

func TestSpotlightPortCommandUnknownSubcommandExits(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightPortCommand([]string{"unknowncmd"})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightPortCommandUnknownSubcommandExits")
	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown subcommand, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "unknown spotlight port subcommand") {
		t.Errorf("expected stderr to mention unknown subcommand, got:\n%s", stderr)
	}
}

func TestSpotlightPortListRequiresWorkspace(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightPortListCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightPortListRequiresWorkspace")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --workspace, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--workspace is required") {
		t.Errorf("expected stderr to mention --workspace is required, got:\n%s", stderr)
	}
}

func TestSpotlightPortAddRequiresWorkspaceAndPort(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightPortAddCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightPortAddRequiresWorkspaceAndPort")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing flags, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--workspace and --port are required") {
		t.Errorf("expected stderr to mention required flags, got:\n%s", stderr)
	}
}

func TestSpotlightPortRemoveRequiresWorkspaceAndForwardID(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runSpotlightPortRemoveCommand([]string{})
		return
	}
	_, stderr, code := runSubprocess(t, "-test.run=TestSpotlightPortRemoveRequiresWorkspaceAndForwardID")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing flags, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--workspace and --forward-id are required") {
		t.Errorf("expected stderr to mention required flags, got:\n%s", stderr)
	}
}
