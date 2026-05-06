package libkrun

import (
	"fmt"
	"path/filepath"
	"runtime"
)

// LibFilename returns the platform-specific filename for libkrun.
//
//   - macOS arm64: libkrun.dylib
//   - Linux arm64/amd64: libkrun.so
func LibFilename() string {
	switch runtime.GOOS {
	case "darwin":
		return "libkrun.dylib"
	default:
		return "libkrun.so"
	}
}

// LibFWFilename returns the platform-specific filename for libkrunfw (kernel firmware).
//
//   - macOS: libkrunfw.dylib
//   - Linux: libkrunfw.so
func LibFWFilename() string {
	switch runtime.GOOS {
	case "darwin":
		return "libkrunfw.dylib"
	default:
		return "libkrunfw.so"
	}
}

// LibPaths returns the expected paths for libkrun and libkrunfw given a lib directory.
// This is the directory typically at <bundle-cache>/lib/.
func LibPaths(libDir string) (libkrunPath, libkrunfwPath string) {
	return filepath.Join(libDir, LibFilename()), filepath.Join(libDir, LibFWFilename())
}

// HostArch returns the architecture string used in bundle asset paths.
//
//   - arm64 → "arm64"
//   - amd64 → "amd64"
func HostArch() string {
	return runtime.GOARCH
}

// HostPlatform returns the host platform string used in bundle manifests,
// e.g. "darwin/arm64" or "linux/amd64".
func HostPlatform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

// IsSupported reports whether the current host platform can run libkrun.
// libkrun requires Hypervisor.framework on macOS (available on Apple Silicon and
// Intel with HAXM) or KVM on Linux.
func IsSupported() bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		return true
	default:
		return false
	}
}
