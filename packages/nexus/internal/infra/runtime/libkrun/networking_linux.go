//go:build linux

package libkrun

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// pickFreePort returns a free ephemeral TCP port on 127.0.0.1.
func pickFreePort() (int, error) {
	ln, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return 0, fmt.Errorf("socket: %w", err)
	}
	defer syscall.Close(ln)

	addr := &syscall.SockaddrInet4{Port: 0, Addr: [4]byte{127, 0, 0, 1}}
	if err := syscall.Bind(ln, addr); err != nil {
		return 0, fmt.Errorf("bind: %w", err)
	}
	sa, err := syscall.Getsockname(ln)
	if err != nil {
		return 0, fmt.Errorf("getsockname: %w", err)
	}
	return sa.(*syscall.SockaddrInet4).Port, nil
}

func resolvePasstPath() string {
	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, ".local", "bin", "passt")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("passt"); err == nil {
		return p
	}
	return "passt"
}

func createPasstSocketPair() (vmSide *os.File, hostSide *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("socketpair: %w", err)
	}
	return os.NewFile(uintptr(fds[0]), "passt-vm"), os.NewFile(uintptr(fds[1]), "passt-host"), nil
}

func startPasstProcess(workDir string, hostSide *os.File) (*os.Process, error) {
	logPath := filepath.Join(workDir, "passt.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		logFile = os.Stderr
	}
	passtBin := resolvePasstPath()
	cmd := exec.Command(
		passtBin,
		"--fd", strconv.Itoa(3),
		"--foreground",
		"--stderr",
	)
	cmd.ExtraFiles = []*os.File{hostSide}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start passt: %w", err)
	}
	_ = logFile.Close()
	return cmd.Process, nil
}
