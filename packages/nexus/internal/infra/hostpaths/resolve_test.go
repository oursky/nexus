package hostpaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLocalDirOnHost_ExistingDir(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveLocalDirOnHost(proj)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(proj) {
		t.Fatalf("got %q want %q", got, filepath.Clean(proj))
	}
}

func TestIsRemoteGitLocation(t *testing.T) {
	if !IsRemoteGitLocation("https://a/b.git") {
		t.Fatal("expected https remote")
	}
	if !IsRemoteGitLocation("git@github.com:o/r.git") {
		t.Fatal("expected ssh remote")
	}
	if IsRemoteGitLocation("/Users/me/r") {
		t.Fatal("expected local path")
	}
}
