//go:build darwin

package macvm

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// socketTempDir creates a directory for Unix domain sockets. gvproxy.sock.ctl and
// virtio listen paths must fit in sockaddr_un (~104 bytes including NUL on
// macOS). Default os.TempDir() under /var/folders/... plus long bake prefixes
// overflows that limit; /tmp paths stay short.
func socketTempDir(pattern string) (string, error) {
	base := "/tmp"
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, pattern)
	if err != nil {
		return "", err
	}
	ctl := filepath.Join(dir, "gvproxy.sock.ctl")
	// Darwin sockaddr_un.Path is 104 bytes including the terminating NUL for file paths.
	if len(ctl) >= 104 {
		_ = os.RemoveAll(dir)
		return "", errSocketPathTooLong(ctl)
	}
	return dir, nil
}

func macvmBakeWorkDir() (string, error) {
	base := "/tmp"
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		base = os.TempDir()
	}
	return os.MkdirTemp(base, "nxmb-")
}

func errSocketPathTooLong(p string) error {
	return &os.PathError{Op: "socketTempDir", Path: p, Err: unix.EINVAL}
}

type diskStat struct {
	avail uint64
	total uint64
}

// diskUsage returns available and total bytes for the filesystem containing path.
func diskUsage(path string) (diskStat, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return diskStat{}, err
	}
	return diskStat{
		avail: s.Bavail * uint64(s.Bsize), //nolint:gosec
		total: s.Blocks * uint64(s.Bsize), //nolint:gosec
	}, nil
}

// copyFile copies src to dst.  On APFS it uses clonefile(2) for an O(1) CoW
// clone; on any other filesystem (or if clonefile fails) it falls back to a
// byte-by-byte io.Copy.  This avoids the multi-minute delay from copying a
// 10 GiB rootfs image with a plain read/write loop.
func copyFile(src, dst string) error {
	// Remove dst first: clonefile(2) requires the destination not to exist.
	_ = os.Remove(dst)

	err := unix.Clonefile(src, dst, 0)
	if err == nil {
		return nil
	}
	// clonefile(2) is not available on non-APFS volumes (e.g. HFS+, tmpfs) or
	// across device boundaries.  Fall back to sparse-aware io.Copy.
	if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EXDEV) &&
		!errors.Is(err, unix.ENOSYS) {
		return err
	}
	return ioFallbackCopy(src, dst)
}

// ioFallbackCopy copies src to dst byte-by-byte via io.Copy.
func ioFallbackCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
