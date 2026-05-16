//go:build linux

package libkrun

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// injectFileIntoExt4 writes data into an ext4 image at the given absolute
// path inside the filesystem using debugfs, without mounting the image.
// The parent directories must already exist inside the image.
func injectFileIntoExt4(imagePath string, data []byte, destPath string, mode os.FileMode) error {
	// Write data to a temp file so debugfs can read it.
	tmp, err := os.CreateTemp("", "nexus-inject-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	_ = tmp.Close()

	// Use debugfs to write the file into the image and set permissions.
	// "write <src> <dst>" writes src into the filesystem at dst.
	// "set_inode_field <dst> mode 0<octal>" sets permissions.
	cmds := fmt.Sprintf("write %s %s\nset_inode_field %s mode 0%o\n",
		tmp.Name(), destPath, destPath, 0o100000|mode)
	cmd := exec.Command("debugfs", "-w", imagePath)
	cmd.Stdin = bytes.NewBufferString(cmds)
	if out, err := cmd.CombinedOutput(); err != nil {
		// debugfs exits non-zero on warnings; treat as success if file exists.
		_ = out
	}
	return nil
}

// writeStampIntoRootfs writes the in-image toolchain stamp directly into the ext4
// rootfs image using debugfs. This is called after the bake VM completes so
// that the stamp is reliably persisted even when libkrun's virtio-blk
// write-back didn't flush before the process was force-killed.
func writeStampIntoRootfs(rootfsPath string) error {
	// The agent checks for this stamp to skip package installation on subsequent boots.
	const stampInsideRootfs = "/var/lib/nexus-tools-base-v19"

	// Write a temporary host-side file with the stamp content, then inject it
	// into the ext4 image using debugfs. debugfs operates on the raw image
	// bytes, bypassing the VM entirely.
	stampFile, err := os.CreateTemp("", "nexus-bake-stamp-*")
	if err != nil {
		return fmt.Errorf("create stamp temp file: %w", err)
	}
	stampPath := stampFile.Name()
	defer os.Remove(stampPath)
	if _, err := stampFile.WriteString("ok\n"); err != nil {
		_ = stampFile.Close()
		return fmt.Errorf("write stamp content: %w", err)
	}
	_ = stampFile.Close()

	// Ensure the /var/lib directory exists inside the rootfs (it should for Ubuntu).
	// debugfs "mkdir" is idempotent-ish but errors if the directory already exists,
	// so we ignore the error.
	_ = exec.Command("debugfs", "-w", "-R", "mkdir /var/lib", rootfsPath).Run()

	// debugfs "write <host-path> <guest-path>" copies the host file into the image.
	out, err := exec.Command("debugfs", "-w", "-R",
		fmt.Sprintf("write %s %s", stampPath, stampInsideRootfs),
		rootfsPath,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs write stamp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("[libkrun] bake VM: wrote bake stamp %s into rootfs via debugfs", stampInsideRootfs)
	return nil
}

func debugfsStatLower(imagePath, guestPath string) string {
	out, _ := exec.Command("debugfs", "-R", "stat "+guestPath, imagePath).CombinedOutput()
	return strings.ToLower(string(out))
}

func debugfsPathMissing(statLower string) bool {
	return strings.Contains(statLower, "not found") || strings.Contains(statLower, "ext2_lookup")
}

// ensureNPMBinSymlinksInRootfs repairs npm-global bin symlinks for legacy images.
// Modern images use tiny mise+npx wrapper scripts at these paths — never overwrite those,
// and skip dangling targets when node_modules was never populated globally.
func ensureNPMBinSymlinksInRootfs(rootfsPath string) error {
	symlinks := []struct {
		link, target string
	}{
		{"/usr/local/bin/opencode", "/usr/local/lib/node_modules/opencode-ai/bin/opencode"},
		{"/usr/local/bin/codex", "/usr/local/lib/node_modules/@openai/codex/bin/codex.js"},
		{"/usr/local/bin/claude", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe"},
	}

	for _, sl := range symlinks {
		tgt := debugfsStatLower(rootfsPath, sl.target)
		if debugfsPathMissing(tgt) {
			continue
		}

		link := debugfsStatLower(rootfsPath, sl.link)
		if strings.Contains(link, "type: symlink") || strings.Contains(link, "type: regular") {
			continue
		}
		if !debugfsPathMissing(link) {
			// Unexpected inode type — leave untouched.
			continue
		}

		_ = exec.Command("debugfs", "-w", "-R", "mkdir /usr/local", rootfsPath).Run()
		_ = exec.Command("debugfs", "-w", "-R", "mkdir /usr/local/bin", rootfsPath).Run()
		out, err := exec.Command("debugfs", "-w", "-R",
			fmt.Sprintf("symlink %s %s", sl.link, sl.target),
			rootfsPath,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("debugfs symlink %s -> %s: %w: %s", sl.link, sl.target, err, strings.TrimSpace(string(out)))
		}
		log.Printf("[libkrun] bake VM: created npm symlink %s -> %s via debugfs", sl.link, sl.target)
	}
	return nil
}
