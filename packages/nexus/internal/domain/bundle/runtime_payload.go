package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
)

const (
	smolvmRuntimeVersion = "v0.5.17"

	envRuntimeCacheDir = "NEXUS_RUNTIME_CACHE_DIR"
	envRuntimeOffline  = "NEXUS_RUNTIME_OFFLINE"
)

type runtimePayloadSpec struct {
	Platform string
	URL      string
	SHA256   string
}

var runtimePayloadSpecs = map[string]runtimePayloadSpec{
	"darwin-arm64": {
		Platform: "darwin-arm64",
		URL:      "https://github.com/smol-machines/smolvm/releases/download/" + smolvmRuntimeVersion + "/smolvm-0.5.17-darwin-arm64.tar.gz",
		SHA256:   "aeb8e77b4c07c2d1996910b7bff44514c463982901aba2e50f62d7bacaee0e9c",
	},
	"linux-amd64": {
		Platform: "linux-amd64",
		URL:      "https://github.com/smol-machines/smolvm/releases/download/" + smolvmRuntimeVersion + "/smolvm-0.5.17-linux-x86_64.tar.gz",
		SHA256:   "803811fb93138a7a30816de0e6b0284e0f982fda1eb1839c0d239f31e90098fe",
	},
}

func allRuntimePayloadSpecs() []runtimePayloadSpec {
	specs := make([]runtimePayloadSpec, 0, len(runtimePayloadSpecs))
	for _, s := range runtimePayloadSpecs {
		specs = append(specs, s)
	}
	return specs
}

func resolveAllPlatformLibkrunAssets(ctx context.Context) (map[string][]string, error) {
	cacheRoot, err := runtimePayloadCacheDir()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]string)
	for _, spec := range allRuntimePayloadSpecs() {
		payloadDir := filepath.Join(cacheRoot, spec.Platform)
		libDir := filepath.Join(payloadDir, "lib")

		if paths, ok := existingRuntimeLibPaths(libDir); ok {
			result[spec.Platform] = paths
			continue
		}

		if os.Getenv(envRuntimeOffline) == "1" {
			return nil, fmt.Errorf("bundle: runtime payload not cached for %s and %s=1 prevents downloading", spec.Platform, envRuntimeOffline)
		}

		if err := os.MkdirAll(payloadDir, 0o755); err != nil {
			return nil, fmt.Errorf("bundle: create runtime cache dir for %s: %w", spec.Platform, err)
		}
		tarballPath := filepath.Join(payloadDir, "dist.tar.gz")
		if err := ensureRuntimeTarball(ctx, tarballPath, spec); err != nil {
			return nil, fmt.Errorf("bundle: download runtime for %s: %w", spec.Platform, err)
		}
		if err := extractRuntimeLibs(tarballPath, libDir); err != nil {
			return nil, fmt.Errorf("bundle: extract runtime for %s: %w", spec.Platform, err)
		}

		if paths, ok := existingRuntimeLibPaths(libDir); ok {
			result[spec.Platform] = paths
			continue
		}
		return nil, fmt.Errorf("bundle: runtime payload extracted but required libkrun files are missing for %s in %s", spec.Platform, libDir)
	}
	return result, nil
}

func runtimePayloadCacheDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(envRuntimeCacheDir)); override != "" {
		return filepath.Join(filepath.Clean(override), "runtime", smolvmRuntimeVersion), nil
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "runtime", smolvmRuntimeVersion), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("bundle: resolve home dir for runtime cache: %w", err)
	}
	return filepath.Join(home, ".cache", "nexus", "runtime", smolvmRuntimeVersion), nil
}

func existingRuntimeLibPaths(libDir string) ([]string, bool) {
	libkrunPath, fwPath := libkrun.LibPaths(libDir)
	paths := []string{libkrunPath, fwPath}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return tryCrossPlatformLibPaths(libDir)
		}
	}
	return paths, true
}

// tryCrossPlatformLibPaths checks for .so files when running on macOS
// (checking a linux-amd64 cache dir) or .dylib files on Linux.
func tryCrossPlatformLibPaths(libDir string) ([]string, bool) {
	var altNames []string
	switch {
	case strings.Contains(libDir, "linux-"):
		altNames = []string{"libkrun.so", "libkrunfw.so"}
	case strings.Contains(libDir, "darwin-"):
		altNames = []string{"libkrun.dylib", "libkrunfw.dylib"}
	default:
		return nil, false
	}
	paths := []string{
		filepath.Join(libDir, altNames[0]),
		filepath.Join(libDir, altNames[1]),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return nil, false
		}
	}
	return paths, true
}

func ensureRuntimeTarball(ctx context.Context, tarballPath string, spec runtimePayloadSpec) error {
	if ok, err := fileMatchesSHA256(tarballPath, spec.SHA256); err == nil && ok {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return fmt.Errorf("bundle: build runtime payload request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bundle: download runtime payload %s: %w", spec.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bundle: download runtime payload %s: unexpected status %s", spec.URL, resp.Status)
	}

	tmp := tarballPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bundle: create runtime payload temp file: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("bundle: write runtime payload: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("bundle: close runtime payload file: %w", err)
	}
	if err := os.Rename(tmp, tarballPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("bundle: finalize runtime payload file: %w", err)
	}

	ok, err := fileMatchesSHA256(tarballPath, spec.SHA256)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bundle: runtime payload checksum mismatch for %s", tarballPath)
	}
	return nil
}

func fileMatchesSHA256(path, expectedHex string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("bundle: hash runtime payload %s: %w", path, err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	return strings.EqualFold(actual, expectedHex), nil
}

func extractRuntimeLibs(tarballPath, libDir string) error {
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return fmt.Errorf("bundle: create runtime lib dir: %w", err)
	}

	in, err := os.Open(tarballPath)
	if err != nil {
		return fmt.Errorf("bundle: open runtime payload: %w", err)
	}
	defer in.Close()

	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("bundle: open runtime payload gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("bundle: read runtime payload tar: %w", err)
		}

		name := hdr.Name
		parts := strings.SplitN(name, "/", 3)
		if len(parts) < 3 || parts[1] != "lib" {
			continue
		}
		base := filepath.Base(name)
		if base == "" || base == "." || base == ".." {
			continue
		}

		outPath := filepath.Join(libDir, base)
		switch hdr.Typeflag {
		case tar.TypeReg:
			out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec
			if err != nil {
				return fmt.Errorf("bundle: create runtime lib %s: %w", outPath, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return fmt.Errorf("bundle: write runtime lib %s: %w", outPath, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("bundle: close runtime lib %s: %w", outPath, err)
			}
		case tar.TypeSymlink:
			_ = os.Remove(outPath)
			if err := os.Symlink(hdr.Linkname, outPath); err != nil {
				return fmt.Errorf("bundle: create runtime lib symlink %s -> %s: %w", outPath, hdr.Linkname, err)
			}
		}
	}

	return nil
}
