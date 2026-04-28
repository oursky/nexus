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
