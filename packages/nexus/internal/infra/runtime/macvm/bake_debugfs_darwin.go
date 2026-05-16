//go:build darwin

package macvm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func resolveDebugFS() string {
	if p, err := exec.LookPath("debugfs"); err == nil {
		return p
	}
	for _, p := range []string{
		"/opt/homebrew/opt/e2fsprogs/sbin/debugfs",
		"/usr/local/opt/e2fsprogs/sbin/debugfs",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func resolveE2fsck() string {
	if p, err := exec.LookPath("e2fsck"); err == nil {
		return p
	}
	for _, p := range []string{
		"/opt/homebrew/opt/e2fsprogs/sbin/e2fsck",
		"/usr/local/opt/e2fsprogs/sbin/e2fsck",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func injectFileIntoExt4(imagePath string, data []byte, destPath string, mode os.FileMode) error {
	dbg := resolveDebugFS()
	if dbg == "" {
		return fmt.Errorf("debugfs not found (brew install e2fsprogs)")
	}
	tmp, err := os.CreateTemp("", "nexus-inject-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	_ = tmp.Close()

	cmds := fmt.Sprintf("write %s %s\nset_inode_field %s mode 0%o\n",
		tmp.Name(), destPath, destPath, 0o100000|mode)
	cmd := exec.Command(dbg, "-w", imagePath)
	cmd.Stdin = bytes.NewBufferString(cmds)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = out
	}
	return nil
}

func rootfsHasBakeStamp(rootfsPath string) bool {
	const stampInsideRootfs = "/var/lib/nexus-tools-base-v19"
	dbg := resolveDebugFS()
	if dbg == "" || strings.TrimSpace(rootfsPath) == "" {
		return false
	}
	out, err := exec.Command(dbg, "-R", "stat "+stampInsideRootfs, rootfsPath).CombinedOutput()
	joined := strings.ToLower(string(out))
	if strings.Contains(joined, "file not found") || strings.Contains(joined, "not found by ext2_lookup") {
		return false
	}
	if err == nil {
		return true
	}
	return strings.Contains(joined, "inode:")
}

func writeStampIntoRootfs(rootfsPath string) error {
	dbg := resolveDebugFS()
	const stampInsideRootfs = "/var/lib/nexus-tools-base-v19"
	if dbg == "" {
		return fmt.Errorf("debugfs not found")
	}
	stampTmp, err := os.CreateTemp("", "nexus-bake-stamp-*")
	if err != nil {
		return err
	}
	stampHost := stampTmp.Name()
	defer func() { _ = os.Remove(stampHost) }()
	if _, err := stampTmp.WriteString("ok\n"); err != nil {
		_ = stampTmp.Close()
		return err
	}
	_ = stampTmp.Close()

	_ = exec.Command(dbg, "-w", "-R", "mkdir /var/lib", rootfsPath).Run()
	out, err := exec.Command(dbg, "-w", "-R",
		fmt.Sprintf("write %s %s", stampHost, stampInsideRootfs),
		rootfsPath,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs write stamp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func debugfsStatLowerDarwin(dbg, imagePath, guestPath string) string {
	out, _ := exec.Command(dbg, "-R", "stat "+guestPath, imagePath).CombinedOutput()
	return strings.ToLower(string(out))
}

func debugfsPathMissingDarwin(statLower string) bool {
	return strings.Contains(statLower, "not found") || strings.Contains(statLower, "ext2_lookup")
}

func ensureNPMBinSymlinksInRootfs(rootfsPath string) error {
	dbg := resolveDebugFS()
	if dbg == "" {
		return fmt.Errorf("debugfs not found")
	}
	symlinks := []struct {
		link, target string
	}{
		{"/usr/local/bin/opencode", "/usr/local/lib/node_modules/opencode-ai/bin/opencode"},
		{"/usr/local/bin/codex", "/usr/local/lib/node_modules/@openai/codex/bin/codex.js"},
		{"/usr/local/bin/claude", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe"},
	}
	for _, sl := range symlinks {
		tgt := debugfsStatLowerDarwin(dbg, rootfsPath, sl.target)
		if debugfsPathMissingDarwin(tgt) {
			continue
		}
		link := debugfsStatLowerDarwin(dbg, rootfsPath, sl.link)
		if strings.Contains(link, "type: symlink") || strings.Contains(link, "type: regular") {
			continue
		}
		if !debugfsPathMissingDarwin(link) {
			continue
		}
		_ = exec.Command(dbg, "-w", "-R", "mkdir /usr/local", rootfsPath).Run()
		_ = exec.Command(dbg, "-w", "-R", "mkdir /usr/local/bin", rootfsPath).Run()
		out2, err := exec.Command(dbg, "-w", "-R",
			fmt.Sprintf("symlink %s %s", sl.link, sl.target),
			rootfsPath,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("debugfs symlink %s -> %s: %w: %s", sl.link, sl.target, err, strings.TrimSpace(string(out2)))
		}
	}
	return nil
}
