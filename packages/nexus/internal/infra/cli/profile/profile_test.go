package profile

import (
	"testing"
)

func TestSaveAndLoadDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)  // profile DB
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // token file (tokenstore FileStore)

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
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	_, err := LoadDefault()
	if err == nil {
		t.Fatal("expected error when profile does not exist")
	}
}

func TestLoadDefaultEnvLocalTokenAndPort(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("NEXUS_DAEMON_TOKEN", "env-token")
	t.Setenv("NEXUS_DAEMON_PORT", "8811")

	p, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if p.Name != "env-local" || p.Host != "127.0.0.1" || p.Port != 8811 || p.Token != "env-token" {
		t.Fatalf("unexpected profile: %+v", p)
	}
}

func TestDeleteDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	p := &Profile{Name: "default", Host: "host", Port: 1234}
	if err := SaveDefault(p); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	if err := DeleteDefault(); err != nil {
		t.Fatalf("DeleteDefault: %v", err)
	}

	// After deletion LoadDefault must return an error.
	if _, err := LoadDefault(); err == nil {
		t.Error("expected error after DeleteDefault, got nil")
	}

	// Idempotent — second delete must not error.
	if err := DeleteDefault(); err != nil {
		t.Errorf("DeleteDefault on missing profile should not error: %v", err)
	}
}
