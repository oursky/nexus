// Package net provides host-side network helpers for the VM runner.
package net

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	passtVersion     = "2025_02_17.a1e48a0"
	passtDownloadURL = "https://passt.top/builds/latest/x86_64/passt"
)

// Passt manages a passt subprocess for virtio-net backend on Linux.
type Passt struct {
	cmd     *exec.Cmd
	fd      int
	ports   []int
	sockFds [2]int
}

// FindPasst returns the path to the passt binary, searching in order:
//  1. $NEXUS_PASST_PATH
//  2. PATH
//  3. ~/.cache/nexus/bin/passt
//
// If the binary is not found and autoDownload is true, it will be downloaded
// to the cache directory (Linux x86_64 only).
func FindPasst(autoDownload bool) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("passt is only supported on Linux")
	}

	// 1. Explicit override from environment.
	if envPath := os.Getenv("NEXUS_PASST_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// 2. PATH
	if p, err := exec.LookPath("passt"); err == nil {
		return p, nil
	}

	// 3. Cache dir
	cacheBin := cachePasstPath()
	if _, err := os.Stat(cacheBin); err == nil {
		return cacheBin, nil
	}

	// 3. Embedded binary (zero-setup fallback)
	if len(passtAssetData) > 0 {
		if err := extractEmbeddedPasstIfNeeded(); err == nil {
			return cacheBin, nil
		}
	}

	if !autoDownload {
		return "", fmt.Errorf("passt not found in PATH or %s", cacheBin)
	}

	// 4. Download (x86_64 static build)
	if err := downloadPasst(cacheBin); err != nil {
		return "", fmt.Errorf("download passt: %w", err)
	}
	return cacheBin, nil
}

// PasstConfig holds network configuration for the passt backend.
type PasstConfig struct {
	GuestIP string // e.g. "10.0.2.15"
	Gateway string // e.g. "10.0.2.2"
	DNS     string // e.g. "8.8.8.8"
	Ports   []int  // host TCP ports to forward
}

// StartPasst starts a passt process connected via a socketpair.
// cfg holds the network configuration for the guest.
// The caller must call Stop() when done.
func StartPasst(passtPath string, cfg PasstConfig) (*Passt, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("passt is only supported on Linux")
	}

	// Create a Unix socketpair for libkrun <-> passt communication.
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("passt socketpair: %w", err)
	}

	// Build passt command line.
	args := []string{
		"--fd", "3", // fd 3 is the first ExtraFiles fd
		"--foreground", // keep in foreground so we can kill it
		"--stderr",     // log to stderr
	}
	if cfg.GuestIP != "" {
		args = append(args, "--address", cfg.GuestIP+"/24")
	}
	if cfg.Gateway != "" {
		args = append(args, "--gateway", cfg.Gateway)
	}
	if cfg.DNS != "" {
		args = append(args, "--dns", cfg.DNS)
	}
	if len(cfg.Ports) > 0 {
		var portStrs []string
		for _, p := range cfg.Ports {
			portStrs = append(portStrs, strconv.Itoa(p))
		}
		args = append(args, "--tcp-ports", strings.Join(portStrs, ","))
	}

	cmd := exec.Command(passtPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Pass one end of the socketpair as fd 3 in the child.
	cmd.ExtraFiles = []*os.File{os.NewFile(uintptr(fds[0]), "passt-sock")}

	if err := cmd.Start(); err != nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		return nil, fmt.Errorf("start passt: %w", err)
	}

	// Give passt a moment to start up.
	time.Sleep(50 * time.Millisecond)

	return &Passt{
		cmd:     cmd,
		fd:      fds[1],
		ports:   cfg.Ports,
		sockFds: fds,
	}, nil
}

// FD returns the file descriptor of the socketpair end that libkrun should use.
func (p *Passt) FD() int {
	return p.fd
}

// Ports returns the list of forwarded ports.
func (p *Passt) Ports() []int {
	return p.ports
}

// Stop terminates the passt process and closes the socketpair.
func (p *Passt) Stop() error {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	for _, fd := range p.sockFds {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
	}
	return nil
}

func cachePasstPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".cache", "nexus", "bin", "passt")
}

func downloadPasst(dest string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("auto-download of passt is only supported on linux-amd64")
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	resp, err := http.Get(passtDownloadURL)
	if err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download passt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download passt: HTTP %d", resp.StatusCode)
	}

	if _, err := f.ReadFrom(resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write passt: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close passt: %w", err)
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod passt: %w", err)
	}

	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename passt: %w", err)
	}

	return nil
}
