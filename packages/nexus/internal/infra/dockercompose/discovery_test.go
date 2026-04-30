package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverPublishedPorts_NoComposeFile(t *testing.T) {
	root := t.TempDir()

	_, err := DiscoverPublishedPorts(context.Background(), root)
	if !errors.Is(err, ErrComposeFileNotFound) {
		t.Fatalf("expected ErrComposeFileNotFound, got %v", err)
	}
}

func TestDiscoverPublishedPorts_DetectsYMLAndParsesPublishedPorts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := runComposeCommand
	t.Cleanup(func() { runComposeCommand = orig })
	runComposeCommand = mockComposeCommandWithPorts

	ports, err := DiscoverPublishedPorts(context.Background(), root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "api", hostPort: 8000, targetPort: 8000},
		{service: "student", hostPort: 5173, targetPort: 5173},
		{service: "student", hostPort: 6006, targetPort: 6006},
	})
}

func mockComposeCommandWithPorts(_ context.Context, _ string, args ...string) (composeCommandOutput, error) {
	if len(args) >= 4 && args[len(args)-2] == "--format" && args[len(args)-1] == "json" {
		return composeCommandOutput{stdout: []byte(`{
  "services": {
    "student": {
      "ports": [
        {"target": 5173, "published": 5173, "protocol": "tcp", "host_ip": "127.0.0.1"},
        "6006:6006"
      ]
    },
    "api": {
      "ports": ["127.0.0.1:8000:8000/tcp"]
    }
  }
}`)}, nil
	}
	return composeCommandOutput{}, nil
}

type portMappingExpectation struct {
	service    string
	hostPort   int
	targetPort int
}

func assertPortMappings(t *testing.T, ports []PublishedPort, expected []portMappingExpectation) {
	t.Helper()
	if len(ports) != len(expected) {
		t.Fatalf("expected %d ports, got %d", len(expected), len(ports))
	}
	for i, exp := range expected {
		p := ports[i]
		if p.Service != exp.service || p.HostPort != exp.hostPort || p.TargetPort != exp.targetPort {
			t.Fatalf("unexpected mapping %d: %+v", i, p)
		}
	}
}

func TestDiscoverPublishedPorts_DetectsYAMLComposeFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yaml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := runComposeCommand
	t.Cleanup(func() { runComposeCommand = orig })
	runComposeCommand = func(_ context.Context, _ string, args ...string) (composeCommandOutput, error) {
		if len(args) > 2 && args[0] == "-f" && filepath.Base(args[1]) != "docker-compose.yaml" {
			t.Fatalf("expected docker-compose.yaml to be selected, got args=%v", args)
		}
		return composeCommandOutput{stdout: []byte(`{"services":{"web":{"ports":["5173:5173"]}}}`)}, nil
	}

	ports, err := DiscoverPublishedPorts(context.Background(), root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}
	if len(ports) != 1 || ports[0].HostPort != 5173 || ports[0].TargetPort != 5173 {
		t.Fatalf("unexpected ports: %+v", ports)
	}
}

func TestDiscoverPublishedPorts_ReturnsComposeJSONUnsupportedOnFormatFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := runComposeCommand
	t.Cleanup(func() { runComposeCommand = orig })
	runComposeCommand = func(_ context.Context, _ string, args ...string) (composeCommandOutput, error) {
		if len(args) >= 4 && args[len(args)-2] == "--format" && args[len(args)-1] == "json" {
			return composeCommandOutput{}, errors.New("unknown flag: --format")
		}
		return composeCommandOutput{stdout: []byte("services:\n  web:\n    ports:\n      - \"5173:5173\"\n")}, nil
	}

	_, err := DiscoverPublishedPorts(context.Background(), root)
	if !errors.Is(err, ErrComposeJSONUnsupported) {
		t.Fatalf("expected ErrComposeJSONUnsupported, got %v", err)
	}
}

func TestDiscoverPublishedPorts_ReturnsComposeJSONUnsupportedOnNonJSONStdout(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := runComposeCommand
	t.Cleanup(func() { runComposeCommand = orig })
	runComposeCommand = func(_ context.Context, _ string, args ...string) (composeCommandOutput, error) {
		if len(args) >= 4 && args[len(args)-2] == "--format" && args[len(args)-1] == "json" {
			return composeCommandOutput{stdout: []byte("invalid true")}, nil
		}
		return composeCommandOutput{stdout: []byte("services:\n  web:\n    ports:\n      - \"5173:5173\"\n")}, nil
	}

	_, err := DiscoverPublishedPorts(context.Background(), root)
	if !errors.Is(err, ErrComposeJSONUnsupported) {
		t.Fatalf("expected ErrComposeJSONUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "non-json output") {
		t.Fatalf("expected non-json hint, got %v", err)
	}
}

// TestFindComposeFiles_SubdirSearch verifies that a compose file in a one-level
// subdirectory is found when the workspace root has none.
func TestFindComposeFiles_SubdirSearch(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "backend")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, found, err := findComposeFiles(root)
	if err != nil {
		t.Fatalf("findComposeFiles: %v", err)
	}
	if !found {
		t.Fatal("expected compose file to be found in subdir")
	}
	if len(files) != 1 || filepath.Dir(files[0]) != subdir {
		t.Fatalf("expected file in %s, got %v", subdir, files)
	}
}

// TestFindComposeFiles_OverrideIncluded verifies that the override file is
// appended after the primary when it exists alongside.
func TestFindComposeFiles_OverrideIncluded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docker-compose.override.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, found, err := findComposeFiles(root)
	if err != nil {
		t.Fatalf("findComposeFiles: %v", err)
	}
	if !found {
		t.Fatal("expected compose files to be found")
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files (primary + override), got %v", files)
	}
	if filepath.Base(files[0]) != "docker-compose.yml" {
		t.Fatalf("expected primary first, got %s", files[0])
	}
	if filepath.Base(files[1]) != "docker-compose.override.yml" {
		t.Fatalf("expected override second, got %s", files[1])
	}
}

// TestFindComposeFiles_RootPreferredOverSubdir verifies that a root compose
// file wins over a compose file in a subdirectory.
func TestFindComposeFiles_RootPreferredOverSubdir(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "backend")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "docker-compose.yml"), []byte("services:{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, found, err := findComposeFiles(root)
	if err != nil {
		t.Fatalf("findComposeFiles: %v", err)
	}
	if !found {
		t.Fatal("expected compose file to be found")
	}
	if len(files) < 1 || filepath.Dir(files[0]) != root {
		t.Fatalf("expected root compose file to be preferred, got %v", files)
	}
}
