//go:build darwin

package hostsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnsureHomebrew ensures Homebrew is installed and on PATH.
// If NEXUS_SKIP_HOMEBREW_INSTALL=1, returns an error instead.
func EnsureHomebrew() error {
	if _, err := exec.LookPath("brew"); err == nil {
		return nil
	}

	// Check common brew locations
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p, "shellenv")
			_, err := cmd.Output()
			if err == nil {
				// Source the shellenv by prepending the PATH
				// We just need brew on PATH; the caller will handle it.
				os.Setenv("PATH", "/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("PATH"))
				return nil
			}
		}
	}

	if os.Getenv("NEXUS_SKIP_HOMEBREW_INSTALL") == "1" {
		return fmt.Errorf("hostsetup: Homebrew required but NEXUS_SKIP_HOMEBREW_INSTALL=1")
	}

	fmt.Fprintf(os.Stderr, "hostsetup: macOS — installing Homebrew (non-interactive) ...\n")

	installCmd := exec.Command("/bin/bash", "-c",
		`NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`)
	installCmd.Stdout = os.Stderr
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("hostsetup: Homebrew installation failed: %w", err)
	}

	// Try to set PATH with brew shellenv
	if _, err := os.Stat("/opt/homebrew/bin/brew"); err == nil {
		os.Setenv("PATH", "/opt/homebrew/bin:"+os.Getenv("PATH"))
	} else if _, err := os.Stat("/usr/local/bin/brew"); err == nil {
		os.Setenv("PATH", "/usr/local/bin:"+os.Getenv("PATH"))
	}

	return nil
}

// prependBrewGuestRootfsPaths prepends Homebrew keg paths
// (e2fsprogs/sbin and coreutils/libexec/gnubin) to PATH.
func prependBrewGuestRootfsPaths() {
	brewPath, _ := exec.LookPath("brew")
	if brewPath == "" {
		return
	}

	cmd := exec.Command(brewPath, "--prefix", "e2fsprogs")
	if out, err := cmd.Output(); err == nil {
		e2fsPath := filepath.Join(string(out[:len(out)-1]), "sbin")
		if _, err := os.Stat(e2fsPath); err == nil {
			os.Setenv("PATH", e2fsPath+":"+os.Getenv("PATH"))
		}
	}

	cmd = exec.Command(brewPath, "--prefix", "coreutils")
	if out, err := cmd.Output(); err == nil {
		coreutilsPath := filepath.Join(string(out[:len(out)-1]), "libexec", "gnubin")
		if _, err := os.Stat(coreutilsPath); err == nil {
			os.Setenv("PATH", coreutilsPath+":"+os.Getenv("PATH"))
		}
	}
}

// EnsureMacOSRootfsTools ensures e2fsck, resize2fs, and truncate
// (or gtruncate) are available via Homebrew.
func EnsureMacOSRootfsTools() error {
	prependBrewGuestRootfsPaths()

	if _, err := exec.LookPath("e2fsck"); err == nil {
		if _, err := exec.LookPath("resize2fs"); err == nil {
			if _, err := exec.LookPath("truncate"); err == nil {
				return nil
			}
			// Check gtruncate
			if _, err := exec.LookPath("gtruncate"); err == nil {
				return nil
			}
		}
	}

	if err := EnsureHomebrew(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "hostsetup: macOS — brew install e2fsprogs coreutils ...\n")

	for _, pkg := range []string{"e2fsprogs", "coreutils"} {
		cmd := exec.Command("brew", "install", pkg)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "hostsetup: warning: brew install %s: %v\n", pkg, err)
		}
	}

	prependBrewGuestRootfsPaths()
	return nil
}

// EnsureMacOSZstd ensures zstd is available via Homebrew.
func EnsureMacOSZstd() error {
	if _, err := exec.LookPath("zstd"); err == nil {
		return nil
	}

	if err := EnsureHomebrew(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "hostsetup: macOS — brew install zstd ...\n")
	cmd := exec.Command("brew", "install", "zstd")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// EnsureMacVMRuntimeLibs checks for libkrun dylibs in
// ~/.local/share/nexus/lib/. Prints a non-fatal warning if missing.
func EnsureMacVMRuntimeLibs() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	libDir := filepath.Join(home, ".local", "share", "nexus", "lib")

	if _, err := os.Stat(filepath.Join(libDir, "libkrun.dylib")); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "hostsetup: macOS libkrun dylibs not provisioned yet at %s\n", libDir)
	fmt.Fprintf(os.Stderr, "hostsetup: (will be provisioned from embedded assets on first daemon start)\n")
	return nil
}
