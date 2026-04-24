package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// CheckResult holds the outcome of a single environment check.
type CheckResult struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// CheckSetupFn, if non-nil, returns driver-specific checks contributed by the
// main package (e.g. verifying libkrun.so is present).
// Signature: func(driver string) []CheckResult
var CheckSetupFn func(driver string) []CheckResult

func checkCommand() *cobra.Command {
	var driver string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify the Nexus daemon environment is ready for use",
		Long: `check runs a series of environment health checks and reports the result.

Checks include:
  • KVM device access (/dev/kvm)
  • VM kernel image present
  • VM rootfs image present
  • Guest agent embedded in rootfs
  • passt network backend available
  • libkrun shared libraries
  • Docker available on host
  • Git config present
  • SSH config/keys present
  • Auth token config for opencode / claude / codex

Use --json to get machine-readable output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			results := runEnvChecks(driver)
			w := cmd.OutOrStdout()

			if jsonOut {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			allOK := true
			for _, r := range results {
				status := "✓"
				if !r.OK {
					status = "✗"
					allOK = false
				}
				if r.Message != "" {
					fmt.Fprintf(w, "  %s  %-40s  %s\n", status, r.Name, r.Message)
				} else {
					fmt.Fprintf(w, "  %s  %s\n", status, r.Name)
				}
			}
			fmt.Fprintln(w, "")
			if allOK {
				fmt.Fprintln(w, "All checks passed — environment is ready.")
				return nil
			}
			return fmt.Errorf("one or more environment checks failed")
		},
	}

	cmd.Flags().StringVar(&driver, "driver", "", "Runtime driver to check for: libkrun (default: auto-detect)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON output")
	return cmd
}

// runEnvChecks executes all environment checks and returns results.
func runEnvChecks(driver string) []CheckResult {
	var results []CheckResult

	add := func(name string, ok bool, msg string) {
		results = append(results, CheckResult{Name: name, OK: ok, Message: msg})
	}
	check := func(name string, fn func() (bool, string)) {
		ok, msg := fn()
		add(name, ok, msg)
	}

	// ── Host system ──────────────────────────────────────────────────────────

	if runtime.GOOS == "linux" {
		check("kvm.access", func() (bool, string) {
			f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
			if err != nil {
				return false, fmt.Sprintf("/dev/kvm not accessible: sudo usermod -aG kvm $USER")
			}
			f.Close()
			return true, "/dev/kvm accessible (O_RDWR)"
		})
	}

	// ── VM assets ────────────────────────────────────────────────────────────

	kernelPath := xdgVMAsset("vmlinux.bin")
	check("vm.kernel", func() (bool, string) {
		fi, err := os.Stat(kernelPath)
		if err != nil {
			return false, fmt.Sprintf("missing: %s (run: nexus daemon start)", kernelPath)
		}
		return true, fmt.Sprintf("%s (%s)", kernelPath, humanSize(fi.Size()))
	})

	rootfsPath := xdgVMAsset("rootfs.ext4")
	check("vm.rootfs", func() (bool, string) {
		fi, err := os.Stat(rootfsPath)
		if err != nil {
			return false, fmt.Sprintf("missing: %s (run: nexus daemon start)", rootfsPath)
		}
		return true, fmt.Sprintf("%s (%s)", rootfsPath, humanSize(fi.Size()))
	})

	// ── Guest agent ──────────────────────────────────────────────────────────

	check("vm.guest-agent", func() (bool, string) {
		if _, err := os.Stat(rootfsPath); err != nil {
			return false, "rootfs not present, cannot verify agent"
		}
		// Primary: check the injection hash file written by ensureFirecrackerGuestAgent.
		// This avoids running debugfs on a live (potentially locked) rootfs.
		hashFile := filepath.Join(defaultDataDir(), "rootfs-agent.sha256")
		if _, err := os.Stat(hashFile); err == nil {
			return true, "nexus-guest-agent injected (hash file present)"
		}
		// Fallback: try debugfs (only works when rootfs is not mounted).
		if _, err := exec.LookPath("debugfs"); err == nil {
			out, _ := exec.Command("debugfs", "-R", "stat /usr/local/bin/nexus-guest-agent", rootfsPath).CombinedOutput()
			if strings.Contains(string(out), "Inode:") {
				return true, "nexus-guest-agent present in rootfs"
			}
		}
		return false, "nexus-guest-agent not injected — run: nexus daemon start"
	})

	// ── Network backend ──────────────────────────────────────────────────────

	check("net.passt", func() (bool, string) {
		home, _ := os.UserHomeDir()
		candidates := []string{
			filepath.Join(home, ".local", "bin", "passt"),
		}
		if p, err := exec.LookPath("passt"); err == nil {
			candidates = append([]string{p}, candidates...)
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return true, p
			}
		}
		return false, "passt not found (run: nexus daemon start)"
	})

	// ── Driver-specific ──────────────────────────────────────────────────────

	effectiveDriver := driver
	if effectiveDriver == "" {
		effectiveDriver = "libkrun"
	}

	check("runtime.libkrun", func() (bool, string) {
		home, _ := os.UserHomeDir()
		libDir := filepath.Join(home, ".local", "share", "nexus", "lib")
		soPath := filepath.Join(libDir, "libkrun.so.1")
		if _, err := os.Stat(soPath); err != nil {
			return false, fmt.Sprintf("libkrun.so.1 not found at %s (run: nexus daemon start)", libDir)
		}
		return true, soPath
	})
	_ = effectiveDriver

	// Delegate to main package for any additional driver-specific checks.
	if CheckSetupFn != nil {
		results = append(results, CheckSetupFn(effectiveDriver)...)
	}

	// ── Host tooling ─────────────────────────────────────────────────────────

	check("host.docker", func() (bool, string) {
		out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output()
		if err != nil {
			return false, "docker daemon not running or docker CLI not found"
		}
		return true, fmt.Sprintf("docker server %s", strings.TrimSpace(string(out)))
	})

	check("host.git", func() (bool, string) {
		home, _ := os.UserHomeDir()
		gitConfig := filepath.Join(home, ".gitconfig")
		if _, err := os.Stat(gitConfig); err == nil {
			return true, gitConfig
		}
		// Check XDG config
		xdgCfg := os.Getenv("XDG_CONFIG_HOME")
		if xdgCfg == "" {
			xdgCfg = filepath.Join(home, ".config")
		}
		xdgGit := filepath.Join(xdgCfg, "git", "config")
		if _, err := os.Stat(xdgGit); err == nil {
			return true, xdgGit
		}
		return false, "no ~/.gitconfig found — git commits inside VM may lack author info"
	})

	check("host.ssh-keys", func() (bool, string) {
		home, _ := os.UserHomeDir()
		sshDir := filepath.Join(home, ".ssh")
		entries, err := os.ReadDir(sshDir)
		if err != nil {
			return false, "~/.ssh/ not found — SSH keys will not be injected into workspaces"
		}
		count := 0
		for _, e := range entries {
			n := e.Name()
			if strings.HasSuffix(n, ".pub") || n == "config" {
				count++
			}
		}
		if count == 0 {
			return false, "no .pub keys in ~/.ssh/ — workspace git SSH operations may fail"
		}
		return true, fmt.Sprintf("%d key file(s) in %s", count, sshDir)
	})

	// ── Auth tokens for AI tools ─────────────────────────────────────────────

	check("auth.opencode", func() (bool, string) {
		home, _ := os.UserHomeDir()
		paths := []string{
			filepath.Join(home, ".config", "opencode", "opencode.json"),
			filepath.Join(home, ".opencode", "opencode.json"),
		}
		for _, p := range paths {
			if fi, err := os.Stat(p); err == nil && fi.Size() > 10 {
				return true, p
			}
		}
		return false, "opencode config not found — codex/opencode may require manual auth inside workspace"
	})

	check("auth.claude", func() (bool, string) {
		home, _ := os.UserHomeDir()
		credFile := filepath.Join(home, ".config", "claude", "credentials.json")
		if fi, err := os.Stat(credFile); err == nil && fi.Size() > 10 {
			return true, credFile
		}
		return false, "claude credentials not found — claude CLI may require manual auth inside workspace"
	})

	// ── Daemon connectivity ───────────────────────────────────────────────────

	check("daemon.socket", func() (bool, string) {
		sockPath := defaultDataDir()
		sock := filepath.Join(sockPath, "nexusd.sock")
		if _, err := os.Stat(sock); err != nil {
			return false, fmt.Sprintf("%s not found (daemon not running?)", sock)
		}
		return true, sock
	})

	return results
}

// humanSize returns a human-readable size string.
func humanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB"}
	f := float64(n)
	for i, u := range units {
		if f < 1024 || i == len(units)-1 {
			return fmt.Sprintf("%.0f %s", f, u)
		}
		_ = i // suppress unused warning
		f /= 1024
	}
	return fmt.Sprintf("%d B", n)
}
