//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// copyFile copies src→dst preferring a reflink clone (O(1) on XFS/btrfs)
// and falls back to a sparse copy on filesystems that don't support reflinks.
func copyFile(src, dst string) error {
	// Try reflink first (O(1) CoW); if unsupported fall back to sparse copy.
	out, err := exec.Command("cp", "--reflink=always", "--sparse=always", src, dst).CombinedOutput()
	if err == nil {
		return nil
	}
	// Reflink failed (non-XFS/btrfs host); use sparse copy instead.
	out, err = exec.Command("cp", "--sparse=always", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyFileWithContext copies src→dst using reflink clone when available (O(1) CoW on XFS/btrfs),
// falling back to sparse copy on filesystems that don't support reflinks.
// Retries transient failures up to 3 times with exponential backoff.
func copyFileWithContext(ctx context.Context, src, dst string) error {
	start := time.Now()
	log.Printf("[libkrun] copyFile start: %s → %s", src, dst)
	defer func() {
		log.Printf("[libkrun] copyFile done: %s → %s (%s)", src, dst, time.Since(start).Round(time.Millisecond))
	}()

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(attempt-1) * 500 * time.Millisecond
			log.Printf("[libkrun] copyFile retry attempt=%d backoff=%s: %s → %s", attempt, backoff, src, dst)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		// Require reflink (O(1) CoW on XFS/btrfs). cp --reflink=always returns 0
		// even when reflink fails (falls back to regular copy on coreutils ≥9), so
		// we inspect stderr to detect the failure.
		cmd := exec.CommandContext(ctx, "cp", "--reflink=always", "--sparse=auto", src, dst)
		out, err := cmd.CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		if err != nil {
			lastErr = fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, outStr)
			continue
		}
		if strings.Contains(outStr, "failed to clone") {
			lastErr = fmt.Errorf("reflink clone failed for %s → %s: %s", src, dst, outStr)
			continue
		}
		log.Printf("[libkrun] copyFile reflink clone enabled: %s → %s", src, dst)
		return nil
	}
	return fmt.Errorf("copyFile failed after 3 attempts: %w", lastErr)
}

// FIEMAP constants (linux/fiemap.h)
const (
	fsIocFiemap           = 0xC020660B // ioctl number for FS_IOC_FIEMAP
	fiemapFlagSync        = 0x00000001 // sync file data before mapping
	fiemapExtentUnwritten = 0x00000800 // extent is allocated but unwritten
)

// fiemapHeader mirrors struct fiemap from linux/fiemap.h.
type fiemapHeader struct {
	Start       uint64 // logical offset of first extent
	Length      uint64 // logical length of mapping
	Flags       uint32 // FIEMAP_FLAG_*
	MappedExts  uint32 // number of extents returned (out)
	ExtentCount uint32 // number of extents requested
	Reserved    uint32
}

// fiemapExtent mirrors struct fiemap_extent from linux/fiemap.h.
type fiemapExtent struct {
	Logical    uint64 // logical offset in bytes
	Physical   uint64 // physical offset in bytes
	Length     uint64 // length in bytes
	Reserved64 [2]uint64
	Flags      uint32 // FIEMAP_EXTENT_*
	Reserved32 [3]uint32
}

// fiemapUnwrittenExtents calls FS_IOC_FIEMAP with FIEMAP_FLAG_SYNC to flush
// dirty page cache and enumerate all extents of the file. It returns only
// extents that have the FIEMAP_EXTENT_UNWRITTEN flag set (i.e., allocated but
// not yet written to disk — these are the extents that need materialisation).
func fiemapUnwrittenExtents(f *os.File) ([]fiemapExtent, error) {
	const batchSize = 256 // extents per ioctl call

	var unwritten []fiemapExtent
	var start uint64

	for {
		// Allocate a contiguous buffer: fiemapHeader + batchSize*fiemapExtent.
		bufSize := int(unsafe.Sizeof(fiemapHeader{})) + batchSize*int(unsafe.Sizeof(fiemapExtent{}))
		buf := make([]byte, bufSize)

		hdr := (*fiemapHeader)(unsafe.Pointer(&buf[0]))
		hdr.Start = start
		hdr.Length = ^uint64(0) // to end of file
		hdr.Flags = fiemapFlagSync
		hdr.ExtentCount = batchSize

		_, _, errno := unix.Syscall(unix.SYS_IOCTL,
			f.Fd(),
			uintptr(fsIocFiemap),
			uintptr(unsafe.Pointer(&buf[0])),
		)
		if errno != 0 {
			return nil, fmt.Errorf("FS_IOC_FIEMAP: %w", errno)
		}

		mapped := int(hdr.MappedExts)
		extBase := unsafe.Pointer(uintptr(unsafe.Pointer(&buf[0])) + unsafe.Sizeof(fiemapHeader{}))
		extSlice := unsafe.Slice((*fiemapExtent)(extBase), mapped)

		for i := range extSlice {
			e := extSlice[i]
			if e.Flags&fiemapExtentUnwritten != 0 {
				unwritten = append(unwritten, e)
			}
		}

		if mapped == 0 {
			break
		}
		last := extSlice[mapped-1]
		// FIEMAP_EXTENT_LAST indicates no more extents.
		if last.Flags&0x80000000 != 0 {
			break
		}
		start = last.Logical + last.Length
	}

	return unwritten, nil
}

// materializeExtents converts any XFS unwritten extents in a disk image to
// written extents using the FIEMAP ioctl (FS_IOC_FIEMAP).
//
// Background: libkrun backs virtio-blk devices with files opened O_RDWR
// (without O_DIRECT), using mmap for I/O. When the guest writes to the VM
// disk, those writes land in the host kernel's page cache but are NOT
// necessarily reflected in the on-disk XFS extent tree — XFS may keep the
// extents in "unwritten" state (preallocated via fallocate). A subsequent
// fsync flushes dirty cache pages but does NOT convert unwritten extents to
// written ones.
//
// When we snapshot via FICLONE (cp --reflink=always) the clone shares the
// same XFS extents, including unwritten ones. However the source file's page
// cache is NOT shared with the clone. When the forked VM boots and reads from
// the clone, its page cache is cold — reads fall through to disk and return
// zeros because the data only lived in the source's page cache.
//
// Fix: use FIEMAP with FIEMAP_FLAG_SYNC to (a) flush dirty page cache to disk
// and (b) enumerate extents. Only extents with FIEMAP_EXTENT_UNWRITTEN need a
// read-then-write pass to force XFS to convert them. This makes
// materializeExtents O(unwritten_data) instead of O(image_size).
func materializeExtents(path string) error {
	const bufSize = 4 * 1024 * 1024 // 4 MiB write buffer

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	extents, err := fiemapUnwrittenExtents(f)
	if err != nil {
		return fmt.Errorf("fiemap %s: %w", path, err)
	}

	if len(extents) == 0 {
		log.Printf("[libkrun] materializeExtents: no unwritten extents in %s", path)
		return nil
	}

	log.Printf("[libkrun] materializeExtents: %d unwritten extent(s) in %s", len(extents), path)

	buf := make([]byte, bufSize)
	for _, ext := range extents {
		offset := int64(ext.Logical)
		remaining := int64(ext.Length)
		for remaining > 0 {
			chunkSize := int64(bufSize)
			if chunkSize > remaining {
				chunkSize = remaining
			}
			n, readErr := f.ReadAt(buf[:chunkSize], offset)
			if n > 0 {
				if _, writeErr := f.WriteAt(buf[:n], offset); writeErr != nil {
					return fmt.Errorf("write-back at offset %d in %s: %w", offset, path, writeErr)
				}
			}
			if readErr != nil {
				return fmt.Errorf("read at offset %d in %s: %w", offset, path, readErr)
			}
			offset += int64(n)
			remaining -= int64(n)
		}
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync %s after materialize: %w", path, err)
	}
	return nil
}
