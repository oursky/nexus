package tokenstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "token")
	s := NewFileStore(path)

	if err := s.Save("mytoken"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	token, found, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if token != "mytoken" {
		t.Fatalf("expected 'mytoken', got %q", token)
	}
}

func TestFileStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(filepath.Join(dir, "nonexistent"))

	token, found, err := s.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
	if token != "" {
		t.Fatalf("expected empty token, got %q", token)
	}
}

func TestFileStore_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	s := NewFileStore(path)

	if err := s.Save("secret"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600, got %04o", perm)
	}
}

func TestFileStore_TrimWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("  token123\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := NewFileStore(path)
	token, found, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if token != "token123" {
		t.Fatalf("expected 'token123', got %q", token)
	}
}
