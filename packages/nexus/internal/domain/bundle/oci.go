// Package bundle provides OCI layer fetching via the OCI distribution spec.
//
// FetchLayers pulls image layers directly from the registry using the
// google/go-containerregistry library. No local Docker daemon or CLI is
// required — the image is streamed from the registry over HTTPS.
package bundle

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// DefaultBaseImage is the OCI image used as the VM rootfs when the workspace
// does not specify a custom image.
const DefaultBaseImage = "ubuntu:24.04"

// OCILayer holds a single OCI image layer.
type OCILayer struct {
	// Digest is the full sha256 digest, e.g. "sha256:abc123...".
	Digest string
	// Data is the raw, uncompressed layer tar bytes.
	Data []byte
}

// LayerBundlePath returns the bundle-internal path for a layer given its digest.
// It uses the first 12 hex characters of the digest (without "sha256:" prefix).
// Example: "sha256:abc123def456..." -> "payload/layers/abc123def456.tar"
func LayerBundlePath(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return "payload/layers/" + hex + ".tar"
}

// FetchLayers pulls image layers for imageRef directly from the OCI registry
// (no Docker daemon required). It resolves multi-arch manifest lists to the
// host platform automatically (via crane defaults) and returns each layer's
// uncompressed tar bytes in order.
//
// Authentication: anonymous for public images; uses ~/.docker/config.json
// credentials when present (via the default keychain).
func FetchLayers(ctx context.Context, imageRef string) ([]OCILayer, error) {
	// Resolve the image, following manifest lists to the right platform.
	img, err := crane.Pull(imageRef,
		crane.WithContext(ctx),
		crane.WithAuthFromKeychain(authn.DefaultKeychain),
		crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: runtime.GOARCH}),
	)
	if err != nil {
		return nil, fmt.Errorf("pull %q: %w", imageRef, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("list layers for %q: %w", imageRef, err)
	}

	result := make([]OCILayer, 0, len(layers))
	for i, layer := range layers {
		digest, err := layerDigest(layer)
		if err != nil {
			return nil, fmt.Errorf("layer %d digest: %w", i, err)
		}

		// Uncompressed returns the raw tar bytes (decompressed gzip layer).
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, fmt.Errorf("layer %d uncompressed: %w", i, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("layer %d read: %w", i, err)
		}
		result = append(result, OCILayer{
			Digest: digest,
			Data:   data,
		})
	}
	return result, nil
}

// layerDigest returns the "sha256:<hex>" string for a layer.
// Prefers the DiffID (uncompressed digest) so the identifier is stable
// and matches what the runner uses when merging layers into the rootfs.
func layerDigest(layer v1.Layer) (string, error) {
	h, err := layer.DiffID()
	if err != nil {
		// Fall back to compressed digest.
		h, err = layer.Digest()
		if err != nil {
			return "", err
		}
	}
	return h.String(), nil
}

// defaultLayerCacheDir returns the directory used to cache downloaded OCI
// layer tars across exports. Layers are keyed by their DiffID (uncompressed
// sha256 digest) and stored as plain tar files.
func defaultLayerCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "bundle-layers")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "nexus", "bundle-layers")
}

// FetchLayersCached fetches OCI layers for imageRef, using a local disk cache
// to avoid re-downloading layers that have already been pulled. Layers are
// cached at defaultLayerCacheDir() keyed by their DiffID.
//
// On first export the layers are downloaded; on subsequent exports they are
// read directly from disk with no network access required.
func FetchLayersCached(ctx context.Context, imageRef string) ([]OCILayer, error) {
	return FetchLayersCachedTo(ctx, imageRef, defaultLayerCacheDir())
}

func FetchLayersCachedForArch(ctx context.Context, imageRef, arch string) ([]OCILayer, error) {
	return fetchLayersCachedTo(ctx, imageRef, defaultLayerCacheDir(), arch)
}

// ExtractImageToDir pulls imageRef from the OCI registry and extracts all
// layers into destDir, applying OCI whiteout semantics. Layers are cached in
// cacheDir (keyed by DiffID) to avoid re-downloading on subsequent calls.
//
// destDir is populated in-place; the caller is responsible for creating it
// before calling and for cleaning up on error.
func ExtractImageToDir(ctx context.Context, imageRef, destDir, cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create layer cache dir: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	img, err := crane.Pull(imageRef,
		crane.WithContext(ctx),
		crane.WithAuthFromKeychain(authn.DefaultKeychain),
		crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: runtime.GOARCH}),
	)
	if err != nil {
		return fmt.Errorf("pull %q: %w", imageRef, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("list layers for %q: %w", imageRef, err)
	}

	for i, layer := range layers {
		digest, err := layerDigest(layer)
		if err != nil {
			return fmt.Errorf("layer %d digest: %w", i, err)
		}

		// Obtain uncompressed tar bytes (disk-cached by DiffID).
		cacheKey := strings.TrimPrefix(digest, "sha256:")
		cachePath := filepath.Join(cacheDir, cacheKey+".tar")

		var tarBytes []byte
		if cached, readErr := os.ReadFile(cachePath); readErr == nil {
			tarBytes = cached
		} else {
			rc, uncompErr := layer.Uncompressed()
			if uncompErr != nil {
				return fmt.Errorf("layer %d uncompressed: %w", i, uncompErr)
			}
			tarBytes, err = io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return fmt.Errorf("layer %d read: %w", i, err)
			}
			_ = os.WriteFile(cachePath, tarBytes, 0o644) //nolint:gosec
		}

		// Apply the layer tar directly onto destDir.
		if err := applyOCILayerTar(tarBytes, destDir); err != nil {
			return fmt.Errorf("layer %d apply: %w", i, err)
		}
	}
	return nil
}

// applyOCILayerTar extracts a single OCI layer tar (uncompressed) onto destDir,
// applying OCI whiteout semantics:
//   - ".wh.<name>" entries delete <name> in the same directory.
//   - ".wh..wh..opq" entries mark an opaque whiteout (delete directory contents).
func applyOCILayerTar(tarBytes []byte, destDir string) error {
	tr := tar.NewReader(&bytesReader{data: tarBytes})

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Strip leading "./" from paths.
		name := strings.TrimPrefix(hdr.Name, "./")
		if name == "" || name == "." {
			continue
		}

		base := filepath.Base(name)
		destPath := filepath.Join(destDir, filepath.FromSlash(name))

		// Opaque whiteout: delete all sibling entries.
		if base == ".wh..wh..opq" {
			parent := filepath.Dir(destPath)
			entries, _ := os.ReadDir(parent)
			for _, e := range entries {
				_ = os.RemoveAll(filepath.Join(parent, e.Name()))
			}
			continue
		}

		// Regular whiteout: delete named target.
		if strings.HasPrefix(base, ".wh.") {
			target := filepath.Join(filepath.Dir(destPath), strings.TrimPrefix(base, ".wh."))
			_ = os.RemoveAll(target)
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, hdr.FileInfo().Mode()); err != nil {
				return fmt.Errorf("mkdir %s: %w", destPath, err)
			}
		case tar.TypeSymlink:
			_ = os.Remove(destPath)
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", destPath, err)
			}
			if err := os.Symlink(hdr.Linkname, destPath); err != nil {
				return fmt.Errorf("symlink %s: %w", destPath, err)
			}
		case tar.TypeLink:
			// Hard link: target relative to destDir.
			linkTarget := filepath.Join(destDir, filepath.FromSlash(strings.TrimPrefix(hdr.Linkname, "./")))
			_ = os.Remove(destPath)
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", destPath, err)
			}
			if err := os.Link(linkTarget, destPath); err != nil {
				// Fallback: copy the target file.
				if copyErr := copyRegularFile(linkTarget, destPath, hdr.FileInfo().Mode()); copyErr != nil {
					return fmt.Errorf("hardlink fallback copy %s: %w", destPath, copyErr)
				}
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", destPath, err)
			}
			mode := hdr.FileInfo().Mode()
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600) //nolint:gosec
			if err != nil {
				return fmt.Errorf("create %s: %w", destPath, err)
			}
			_, cpErr := io.Copy(f, tr)
			_ = f.Close()
			if cpErr != nil {
				return fmt.Errorf("write %s: %w", destPath, cpErr)
			}
		}
	}
	return nil
}

// copyRegularFile copies src to dst preserving mode.
func copyRegularFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600) //nolint:gosec
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// bytesReader wraps a byte slice as an io.Reader.
type bytesReader struct {
	data   []byte
	offset int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

// FetchLayersCachedTo is like FetchLayersCached but lets the caller specify
// the cache directory.
func FetchLayersCachedTo(ctx context.Context, imageRef, cacheDir string) ([]OCILayer, error) {
	return fetchLayersCachedTo(ctx, imageRef, cacheDir, runtime.GOARCH)
}

func fetchLayersCachedTo(ctx context.Context, imageRef, cacheDir, arch string) ([]OCILayer, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create layer cache dir: %w", err)
	}

	img, err := crane.Pull(imageRef,
		crane.WithContext(ctx),
		crane.WithAuthFromKeychain(authn.DefaultKeychain),
		crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: arch}),
	)
	if err != nil {
		return nil, fmt.Errorf("pull %q for %s: %w", imageRef, arch, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("list layers for %q (%s): %w", imageRef, arch, err)
	}

	result := make([]OCILayer, 0, len(layers))
	for i, layer := range layers {
		digest, err := layerDigest(layer)
		if err != nil {
			return nil, fmt.Errorf("layer %d digest: %w", i, err)
		}

		// Cache key: sha256:<hex> → <hex>.tar
		cacheKey := strings.TrimPrefix(digest, "sha256:")
		cachePath := filepath.Join(cacheDir, cacheKey+".tar")

		var data []byte
		if cached, readErr := os.ReadFile(cachePath); readErr == nil {
			// Cache hit — no network access needed.
			data = cached
		} else {
			// Cache miss — download the layer.
			rc, uncompErr := layer.Uncompressed()
			if uncompErr != nil {
				return nil, fmt.Errorf("layer %d uncompressed: %w", i, uncompErr)
			}
			data, err = io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, fmt.Errorf("layer %d read: %w", i, err)
			}
			// Write to cache (best-effort; don't fail the export on write error).
			_ = os.WriteFile(cachePath, data, 0o644) //nolint:gosec
		}

		result = append(result, OCILayer{
			Digest: digest,
			Data:   data,
		})
	}
	return result, nil
}
