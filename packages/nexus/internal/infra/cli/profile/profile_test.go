package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	p := &Profile{
		Name:    "default",
		Host:    "newman@linuxbox",
		Port:    7777,
		Token:   "secret-token",
		SSHPort: 2222,
	}

	if err := SaveDefault(p); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	got, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}

	if got.Name != p.Name || got.Host != p.Host || got.Port != p.Port ||
		got.Token != p.Token || got.SSHPort != p.SSHPort {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, p)
	}
}

func TestLoadDefaultMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	_, err := LoadDefault()
	if err == nil {
		t.Fatal("expected error when profile does not exist")
	}
}

func TestDeleteDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	p := &Profile{Name: "default", Host: "host", Port: 1234}
	if err := SaveDefault(p); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	if err := DeleteDefault(); err != nil {
		t.Fatalf("DeleteDefault: %v", err)
	}

	path := filepath.Join(tmpDir, "nexus", "profiles", "default.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed")
	}

	if err := DeleteDefault(); err != nil {
		t.Errorf("DeleteDefault on missing file should not error: %v", err)
	}
}
