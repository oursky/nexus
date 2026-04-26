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
