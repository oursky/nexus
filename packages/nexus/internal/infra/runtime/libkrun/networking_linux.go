//go:build linux

package libkrun

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const passtBinName = "passt"

// passtState tracks a running passt instance for a workspace VM.
type passtState struct {
	proc   *os.Process
	vmFD   *os.File  // the VM-side end of the socket pair (passed as ExtraFile to child)
	hostFD *os.File  // the host-side end (passed to passt); closed after passt starts
}

// resolvePasstPath resolves the passt binary. Checks ~/.local/bin first.
func resolvePasstPath() string {
	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, ".local", "bin", passtBinName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(passtBinName); err == nil {
		return p
	}
	return passtBinName
}

// setupPasst creates a socket pair and starts passt for one VM.
// Returns passtState where:
//   - vmFD is the socket fd to pass to the libkrun child process (as ExtraFile)
//   - hostFD is the passt-side fd (inherited by passt; closed in parent after exec)
//
// sshHostPort: if > 0, passt will forward this host TCP port to guest port 22.
func setupPasst(workspaceID, workDir string, sshHostPort int) (*passtState, error) {
	passtBin := resolvePasstPath()

	// Create a Unix stream socket pair. passt gets fds[0], libkrun-vm gets fds[1].
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socketpair: %w", err)
	}

	hostFile := os.NewFile(uintptr(fds[0]), "passt-host")
	vmFile := os.NewFile(uintptr(fds[1]), "passt-vm")

	logPath := filepath.Join(workDir, "passt.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		logFile = os.Stderr
	}

	// passt args: socket fd mode, foreground, stderr logging.
	// --fd: the socket pair fd (passt reads/writes L2 frames here)
	// -t HOST_PORT:22: forward host port to guest SSH port 22
	args := []string{
		"--fd", strconv.Itoa(3), // fds[0] will be fd 3 (ExtraFiles[0])
		"--foreground",
		"--stderr",
		"--no-map-gw",
	}
	if sshHostPort > 0 {
		// Forward host TCP sshHostPort to guest port 22.
		// passt format: HOST_PORT:GUEST_PORT
		args = append(args, "-t", fmt.Sprintf("127.0.0.1/%d:22", sshHostPort))
	}

	cmd := exec.Command(passtBin, args...)
	cmd.ExtraFiles = []*os.File{hostFile} // becomes fd 3 in passt
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		_ = hostFile.Close()
		_ = vmFile.Close()
		return nil, fmt.Errorf(
			"start passt for workspace %s: %w\n\nCheck log: %s\nEnsure passt is installed: %s --version",
			workspaceID, err, logPath, passtBin,
		)
	}

	// Close the host-side fd in the parent — passt has it now.
	_ = hostFile.Close()

	// Give passt a moment to initialise.
	time.Sleep(80 * time.Millisecond)

	log.Printf("[libkrun] passt started for workspace %s (pid %d)", workspaceID, cmd.Process.Pid)

	return &passtState{
		proc: cmd.Process,
		vmFD: vmFile,
	}, nil
}

// teardownPasst stops passt and closes its file descriptors.
func teardownPasst(ps *passtState) {
	if ps == nil {
		return
	}
	if ps.proc != nil {
		if err := ps.proc.Signal(syscall.SIGTERM); err != nil {
			_ = ps.proc.Kill()
		}
		done := make(chan struct{})
		go func() {
			_, _ = ps.proc.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = ps.proc.Kill()
		}
	}
	if ps.vmFD != nil {
		_ = ps.vmFD.Close()
	}
}

// pickFreePort returns a free ephemeral TCP port on 127.0.0.1.
func pickFreePort() (int, error) {
	// syscall.Listen on 127.0.0.1:0 lets the kernel pick an ephemeral port.
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

// checkPasstAvailable returns an error if passt is not installed.
func checkPasstAvailable() error {
	bin := resolvePasstPath()
	if _, err := os.Stat(bin); err == nil {
		return nil
	}
	if _, err := exec.LookPath(passtBinName); err == nil {
		return nil
	}
	return fmt.Errorf(
		"prerequisite_error.network_backend_missing: %s not found\n\n"+
			"Remediation: install passt (e.g. sudo apt install passt)",
		passtBinName,
	)
}
