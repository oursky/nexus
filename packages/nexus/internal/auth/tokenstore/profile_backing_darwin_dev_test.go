//go:build darwin && dev

package tokenstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileVsDaemonBackingDistinctPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ds := probe()
	ps := profileBackingStore()

	daemonPath := filepath.Join(home, ".config", "nexus-dev", "daemon-token")
	profilePath := filepath.Join(home, ".config", "nexus-dev", "profile-bearer-token")

	if err := ds.Save("aaa"); err != nil {
		t.Fatalf("daemon save: %v", err)
	}
	if err := ps.Save("bbb"); err != nil {
		t.Fatalf("profile save: %v", err)
	}

	d, err := os.ReadFile(daemonPath)
	if err != nil {
		t.Fatalf("read daemon token file: %v", err)
	}
	if string(d) != "aaa" {
		t.Fatalf("daemon file: got %q want aaa", d)
	}
	p, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile token file: %v", err)
	}
	if string(p) != "bbb" {
		t.Fatalf("profile file: got %q want bbb", p)
	}
}
