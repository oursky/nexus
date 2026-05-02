package dockercompose

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPublishedPortsFromYAML_NoComposeFile(t *testing.T) {
	root := t.TempDir()

	_, err := DiscoverPublishedPortsFromYAML(root)
	if !errors.Is(err, ErrComposeFileNotFound) {
		t.Fatalf("expected ErrComposeFileNotFound, got %v", err)
	}
}

func TestDiscoverPublishedPortsFromYAML_StringPorts(t *testing.T) {
	root := t.TempDir()
	content := `services:
  web:
    ports:
      - "3000:3000"
      - "127.0.0.1:8080:80/tcp"
  api:
    ports:
      - "8000:8000"
`
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := DiscoverPublishedPortsFromYAML(root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "api", hostPort: 8000, targetPort: 8000},
		{service: "web", hostPort: 3000, targetPort: 3000},
		{service: "web", hostPort: 8080, targetPort: 80},
	})
}

func TestDiscoverPublishedPortsFromYAML_ObjectPorts(t *testing.T) {
	root := t.TempDir()
	content := `services:
  app:
    ports:
      - target: 5173
        published: 5173
        protocol: tcp
        host_ip: 127.0.0.1
      - target: 6006
        published: 6006
`
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := DiscoverPublishedPortsFromYAML(root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "app", hostPort: 5173, targetPort: 5173},
		{service: "app", hostPort: 6006, targetPort: 6006},
	})
}

func TestDiscoverPublishedPortsFromYAML_MixedPorts(t *testing.T) {
	root := t.TempDir()
	content := `services:
  web:
    ports:
      - "3000:3000"
      - target: 8080
        published: 8080
        protocol: tcp
`
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := DiscoverPublishedPortsFromYAML(root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "web", hostPort: 3000, targetPort: 3000},
		{service: "web", hostPort: 8080, targetPort: 8080},
	})
}

func TestDiscoverPublishedPortsFromYAML_SkipsExposeOnly(t *testing.T) {
	root := t.TempDir()
	content := `services:
  web:
    ports:
      - "3000"
      - "3001:3001"
`
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := DiscoverPublishedPortsFromYAML(root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "web", hostPort: 3001, targetPort: 3001},
	})
}

func TestDiscoverPublishedPortsFromYAML_Subdir(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "backend")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `services:
  api:
    ports:
      - "4000:4000"
`
	if err := os.WriteFile(filepath.Join(subdir, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := DiscoverPublishedPortsFromYAML(root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "api", hostPort: 4000, targetPort: 4000},
	})
}

func TestDiscoverPublishedPortsFromYAML_OverrideMerged(t *testing.T) {
	root := t.TempDir()
	primary := `services:
  web:
    ports:
      - "3000:3000"
`
	override := `services:
  web:
    ports:
      - "3000:3000"
      - "8080:8080"
`
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(primary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docker-compose.override.yml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	ports, err := DiscoverPublishedPortsFromYAML(root)
	if err != nil {
		t.Fatalf("discover ports: %v", err)
	}

	assertPortMappings(t, ports, []portMappingExpectation{
		{service: "web", hostPort: 3000, targetPort: 3000},
		{service: "web", hostPort: 8080, targetPort: 8080},
	})
}
