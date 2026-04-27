package libkrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyFileWithContextTimeout(t *testing.T) {
	// Skip on macOS since GNU cp flags are not supported.
	if _, err := exec.LookPath("cp"); err == nil {
		// Check if cp supports --reflink (GNU cp)
		cmd := exec.Command("cp", "--reflink=always", "--sparse=always", "/dev/null", "/dev/null")
		if err := cmd.Run(); err != nil {
			t.Skip("GNU cp not available, skipping copyFileWithContext test")
		}
	}

	// Create a temp source file.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(srcDir, "dst.txt")

	// Test normal copy.
	ctx := context.Background()
	if err := copyFileWithContext(ctx, src, dst); err != nil {
		t.Fatalf("copyFileWithContext failed: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("dst not created: %v", err)
	}

	// Test with cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst2 := filepath.Join(srcDir, "dst2.txt")
	if err := copyFileWithContext(ctx, src, dst2); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestCreateDockerDataImageWithContextTimeout(t *testing.T) {
	// Skip on macOS since mkfs.ext4 is not available.
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not found, skipping")
	}

	dir := t.TempDir()
	img := filepath.Join(dir, "docker-data.ext4")

	// Test normal creation.
	ctx := context.Background()
	if err := createDockerDataImageWithContext(ctx, img); err != nil {
		t.Fatalf("createDockerDataImageWithContext failed: %v", err)
	}
	if _, err := os.Stat(img); err != nil {
		t.Fatalf("image not created: %v", err)
	}

	// Test with short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	img2 := filepath.Join(dir, "docker-data2.ext4")
	if err := createDockerDataImageWithContext(ctx, img2); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestBuildBaseImageWithContextTimeout(t *testing.T) {
	// Skip on macOS since mkfs.ext4 is not available.
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not found, skipping")
	}

	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	img := filepath.Join(dir, "base.ext4")

	// Test normal build.
	ctx := context.Background()
	if err := buildBaseImageWithContext(ctx, repoRoot, img); err != nil {
		t.Fatalf("buildBaseImageWithContext failed: %v", err)
	}
	if _, err := os.Stat(img); err != nil {
		t.Fatalf("image not created: %v", err)
	}

	// Test with short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	img2 := filepath.Join(dir, "base2.ext4")
	if err := buildBaseImageWithContext(ctx, repoRoot, img2); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestEnsureBaseImageCacheAndLock(t *testing.T) {
	// Skip on macOS since mkfs.ext4 is not available.
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not found, skipping")
	}

	basesDir := t.TempDir()
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestHash := "test-manifest"

	// First call should build.
	path1, err := EnsureBaseImage(context.Background(), repoRoot, basesDir, manifestHash)
	if err != nil {
		t.Fatalf("EnsureBaseImage first call failed: %v", err)
	}

	// Second call should hit cache.
	path2, err := EnsureBaseImage(context.Background(), repoRoot, basesDir, manifestHash)
	if err != nil {
		t.Fatalf("EnsureBaseImage second call failed: %v", err)
	}
	if path1 != path2 {
		t.Fatalf("cache miss: %s != %s", path1, path2)
	}

	// Different manifest hash should miss cache and build a new image.
	path3, err := EnsureBaseImage(context.Background(), repoRoot, basesDir, "different-manifest")
	if err != nil {
		t.Fatalf("EnsureBaseImage different manifest call failed: %v", err)
	}
	if path1 == path3 {
		t.Fatalf("expected different cache key for different manifest: %s == %s", path1, path3)
	}
}
