package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

const (
	defaultPreflightFirecrackerVersion = "1.15.1"
	defaultPreflightLimaVersion        = "2.1.1"
)

func preflightSkipAutoinstall() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NEXUS_PREFLIGHT_SKIP_AUTOINSTALL")))
	return v == "1" || v == "true" || v == "yes"
}

func preflightToolVersion(envKey, fallback string) string {
	s := strings.TrimSpace(os.Getenv(envKey))
	if s != "" {
		return strings.TrimPrefix(s, "v")
	}
	return fallback
}

func MaybeAutoinstallPreflightHostTools() error {
	if preflightSkipAutoinstall() {
		return nil
	}
	switch goruntime.GOOS {
	case "linux":
		if _, err := exec.LookPath("firecracker"); err == nil {
			return nil
		}
		return autoinstallFirecrackerLinux()
	case "darwin":
		if _, err := exec.LookPath("limactl"); err == nil {
			return nil
		}
		return autoinstallLimaDarwin()
	default:
		return nil
	}
}

func toolCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "nexus", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func prependPathEntry(dir string) {
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func autoinstallFirecrackerLinux() error {
	ver := preflightToolVersion("NEXUS_FIRECRACKER_VERSION", defaultPreflightFirecrackerVersion)
	var fcSuffix string
	switch goruntime.GOARCH {
	case "amd64":
		fcSuffix = "x86_64"
	case "arm64":
		fcSuffix = "aarch64"
	default:
		return fmt.Errorf("unsupported GOARCH for Firecracker autoinstall: %s", goruntime.GOARCH)
	}

	cache, err := toolCacheDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(cache, "firecracker", ver, "bin")
	fcBin := filepath.Join(binDir, "firecracker")
	if st, err := os.Stat(fcBin); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
		prependPathEntry(binDir)
		return nil
	}

	asset := fmt.Sprintf("firecracker-v%s-%s.tgz", ver, fcSuffix)
	url := fmt.Sprintf(
		"https://github.com/firecracker-microvm/firecracker/releases/download/v%s/%s",
		ver, asset,
	)

	staging := filepath.Join(cache, "firecracker", ver, "staging")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := downloadExtractTarGzPreflight(url, staging); err != nil {
		return err
	}

	src, err := findFirecrackerBinaryUnder(staging)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := copyFileMode(fcBin, src, 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(staging)
	prependPathEntry(binDir)
	return nil
}

func findFirecrackerBinaryUnder(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		if strings.HasPrefix(base, "firecracker-v") && !strings.Contains(base, ".debug") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("firecracker binary not found under %s", root)
	}
	return found, nil
}

func autoinstallLimaDarwin() error {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	ver := preflightToolVersion("NEXUS_LIMA_VERSION", defaultPreflightLimaVersion)
	var darwinArch string
	switch goruntime.GOARCH {
	case "amd64":
		darwinArch = "x86_64"
	case "arm64":
		darwinArch = "arm64"
	default:
		return fmt.Errorf("unsupported GOARCH for Lima autoinstall: %s", goruntime.GOARCH)
	}

	cache, err := toolCacheDir()
	if err != nil {
		return err
	}
	dest := filepath.Join(cache, "lima", ver)
	limactl := filepath.Join(dest, "bin", "limactl")
	if st, err := os.Stat(limactl); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
		prependPathEntry(filepath.Join(dest, "bin"))
		return nil
	}

	if err := os.RemoveAll(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	asset := fmt.Sprintf("lima-%s-Darwin-%s.tar.gz", ver, darwinArch)
	url := fmt.Sprintf(
		"https://github.com/lima-vm/lima/releases/download/v%s/%s",
		ver, asset,
	)
	if err := downloadExtractTarGzPreflight(url, dest); err != nil {
		return err
	}
	if _, err := os.Stat(limactl); err != nil {
		return fmt.Errorf("limactl not found after extract: %w", err)
	}
	prependPathEntry(filepath.Join(dest, "bin"))
	return nil
}

func copyFileMode(dst, src string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func downloadExtractTarGzPreflight(url string, destDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if name == "" || name == "." {
			continue
		}
		target := filepath.Join(destDir, filepath.FromSlash(name))
		cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest) && filepath.Clean(target) != filepath.Clean(destDir) {
			return fmt.Errorf("unsafe tar path %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, tarFileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, tarFileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.RemoveAll(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			continue
		}
	}
	return nil
}

func tarFileMode(m int64) os.FileMode {
	return os.FileMode(m) & 0o777
}
