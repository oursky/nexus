//go:build darwin

package macvm

import (
	"io"
	"os"
)

// copyFile copies src to dst using a simple io.Copy (no reflink on macOS APFS).
// For large rootfs images this is O(n) — a future improvement can use
// clonefile(2) on APFS for O(1) CoW copies.
func copyFile(src, dst string) error {
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
