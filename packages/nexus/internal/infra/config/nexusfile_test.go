package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNexusfile_TOML(t *testing.T) {
	dir := t.TempDir()
	content := `"$schema" = "./schemas/nexusfile.schema.json"

[vm]
profile = "default"
`
	if err := os.WriteFile(filepath.Join(dir, "Nexusfile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, ok, err := LoadNexusfile(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected Nexusfile to be present")
	}
	if cfg.VM.Profile != "default" {
		t.Fatalf("expected profile=default, got %q", cfg.VM.Profile)
	}
}

func TestLoadNexusfile_JSONBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	content := `{"vm":{"profile":"minimal"}}`
	if err := os.WriteFile(filepath.Join(dir, "Nexusfile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, ok, err := LoadNexusfile(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected Nexusfile to be present")
	}
	if cfg.VM.Profile != "minimal" {
		t.Fatalf("expected profile=minimal, got %q", cfg.VM.Profile)
	}
}

func TestLoadNexusfile_TOMLOmittedProfile(t *testing.T) {
	dir := t.TempDir()
	content := `"$schema" = "./schemas/nexusfile.schema.json"`
	if err := os.WriteFile(filepath.Join(dir, "Nexusfile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, ok, err := LoadNexusfile(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected Nexusfile to be present")
	}
	if cfg.VM.Profile != "" {
		t.Fatalf("expected omitted profile to remain empty in config, got %q", cfg.VM.Profile)
	}
}

func TestLoadBaseNexusfile_Missing(t *testing.T) {
	// Use a temp dir as fake home so the base Nexusfile is missing.
	t.Setenv("HOME", t.TempDir())
	cfg, ok, err := LoadBaseNexusfile()
	if err != nil {
		t.Fatalf("expected no error for missing base nexusfile, got %v", err)
	}
	if ok {
		t.Fatal("expected missing base Nexusfile to return ok=false")
	}
	if cfg.VM.Profile != "" {
		t.Fatalf("expected empty config for missing file, got %+v", cfg)
	}
}

func TestLoadBaseNexusfile_Present(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	nexusDir := filepath.Join(fakeHome, ".config", "nexus")
	if err := os.MkdirAll(nexusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `[vm]
profile = "minimal"
`
	if err := os.WriteFile(filepath.Join(nexusDir, "Nexusfile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, ok, err := LoadBaseNexusfile()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected base Nexusfile to be present")
	}
	if cfg.VM.Profile != "minimal" {
		t.Fatalf("expected profile=minimal, got %q", cfg.VM.Profile)
	}
}

func TestComputeManifestHash(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base")
	projectPath := filepath.Join(dir, "project")

	// Same inputs should produce same hash.
	if err := os.WriteFile(basePath, []byte("base-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("project-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1 := ComputeManifestHash(basePath, projectPath, "v1")
	h2 := ComputeManifestHash(basePath, projectPath, "v1")
	if h1 != h2 {
		t.Fatalf("expected deterministic hash: %q != %q", h1, h2)
	}

	// Different bake version should produce different hash.
	h3 := ComputeManifestHash(basePath, projectPath, "v2")
	if h1 == h3 {
		t.Fatalf("expected different hash for different bake version: %q == %q", h1, h3)
	}

	// Missing files should still produce deterministic hashes.
	h4 := ComputeManifestHash("/nonexistent", "/nonexistent2", "v1")
	h5 := ComputeManifestHash("/nonexistent", "/nonexistent2", "v1")
	if h4 != h5 {
		t.Fatalf("expected deterministic hash for missing files: %q != %q", h4, h5)
	}
}
