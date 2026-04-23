//go:build linux

package firecracker

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	// passtBinName is the preferred binary name for the passt networking backend.
	passtBinName = "passt"

	// tunDevice is the Linux TUN/TAP character device.
	tunDevice = "/dev/net/tun"

	// tunFlags for creating a TAP device.
	iffTAP   = uint16(0x0002)
	iffNOPI  = uint16(0x1000)
	tunsetiff = uintptr(0x400454ca)
)

// passtProcess tracks a running passt instance for a workspace.
type passtProcess struct {
	proc    *os.Process
	tapName string
	tapFD   *os.File
}

// resolvePasstPath returns the path to the passt binary.
// Checks the user-local install location, then PATH.
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

// createTAPDevice creates a non-persistent TAP interface using /dev/net/tun.
// Returns the open tun file descriptor that keeps the interface alive.
// Requires write access to /dev/net/tun (standard on most Linux systems).
func createTAPDevice(name string) (*os.File, error) {
	if len(name) > 15 {
		return nil, fmt.Errorf("TAP name %q exceeds 15-character Linux limit", name)
	}

	tun, err := os.OpenFile(tunDevice, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w\n\nRemedy: ensure %s is readable (mode 0666 or user in tun/kvm group)", tunDevice, err, tunDevice)
	}

	// struct ifreq: name[IFNAMSIZ=16] + flags (uint16) + padding
	var ifr [40]byte
	copy(ifr[:15], name)
	*(*uint16)(unsafe.Pointer(&ifr[16])) = iffTAP | iffNOPI

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tun.Fd(), tunsetiff, uintptr(unsafe.Pointer(&ifr[0]))); errno != 0 {
		tun.Close()
		return nil, fmt.Errorf("TUNSETIFF for %q: %w", name, errno)
	}

	return tun, nil
}

// startPasst starts the passt process connected to the given TAP fd.
// passt provides userspace NAT/routing for the VM without host firewall changes.
//
// passt receives the tap fd via an extra inherited file descriptor; the fd
// number is passed via the --fd flag.
func startPasst(tapName string, tapFD *os.File, workDir string) (*os.Process, error) {
	passtBin := resolvePasstPath()

	// passt receives the tap fd as extra file descriptor #3 (0=stdin,1=stdout,2=stderr,3=first extra).
	fdNum := 3

	// Log file for passt output per workspace.
	logPath := filepath.Join(workDir, "passt.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		logFile = os.Stderr
	}

	cmd := exec.Command(
		passtBin,
		"--fd", strconv.Itoa(fdNum),
		"--foreground",       // don't daemonize; we manage the lifecycle
		"--stderr",           // log to stderr (redirect to log file)
		"--no-map-gw",        // don't set up gateway mapping (handled internally)
		"--address", passtGuestIP(tapName), // assign a fixed IP to this tap
	)
	cmd.ExtraFiles = []*os.File{tapFD} // becomes fd 3
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Detach so passt doesn't die when daemon process group changes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf(
			"runtime_error.network_attach: start passt for tap %s: %w\n\n"+
				"Check passt log: %s\n"+
				"Ensure passt is installed: %s --version",
			tapName, err, logPath, passtBin,
		)
	}
	return cmd.Process, nil
}

// passtGuestIP derives a stable guest IP from the TAP name for passt address assignment.
func passtGuestIP(tapName string) string {
	// Derive from the same CID-based scheme as guestIPForCID.
	// For passt we just use the bridge gateway-relative addressing.
	_ = tapName
	return guestSubnetCIDR()
}

// stopPasst gracefully terminates a passt process.
func stopPasst(proc *os.Process) {
	if proc == nil {
		return
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		proc.Kill()
	}
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		proc.Kill()
	}
}

// releaseTAPDevice closes the tun fd, which destroys the non-persistent TAP interface.
func releaseTAPDevice(tapFD *os.File) {
	if tapFD != nil {
		tapFD.Close()
	}
}

// checkPasstAvailable verifies passt is installed and returns nil if OK.
func checkPasstAvailable() error {
	bin := resolvePasstPath()
	if _, err := os.Stat(bin); err != nil {
		if _, lerr := exec.LookPath(passtBinName); lerr != nil {
			return fmt.Errorf(
				"prerequisite_error.network_backend_missing: %s not found (checked %s and PATH)\n\n"+
					"Remediation: nexus daemon start will auto-install passt, or:\n"+
					"  sudo apt install passt",
				passtBinName, bin,
			)
		}
	}
	return nil
}

// realSetupTAPPasst creates a rootless TAP + passt for workspace networking.
// Returns a *passtProcess handle stored in the manager for teardown.
func realSetupTAPPasst(tapName, workDir string) (*passtProcess, error) {
	tapFD, err := createTAPDevice(tapName)
	if err != nil {
		return nil, fmt.Errorf("create TAP %s: %w", tapName, err)
	}

	proc, err := startPasst(tapName, tapFD, workDir)
	if err != nil {
		releaseTAPDevice(tapFD)
		return nil, err
	}

	// Give passt a moment to initialize before Firecracker opens the tap.
	time.Sleep(100 * time.Millisecond)

	log.Printf("[firecracker] passt started for tap %s (pid %d)", tapName, proc.Pid)
	return &passtProcess{proc: proc, tapName: tapName, tapFD: tapFD}, nil
}

// teardownTAPPasst tears down a passt process and its associated TAP device.
func teardownTAPPasst(pp *passtProcess) {
	if pp == nil {
		return
	}
	stopPasst(pp.proc)
	releaseTAPDevice(pp.tapFD)
	log.Printf("[firecracker] passt torn down for tap %s", pp.tapName)
}

// tapHelperSetupInstructions returns a human-readable note about the networking backend.
func tapHelperSetupInstructions() string {
	return "  nexus daemon start   # auto-installs passt and provisions rootless networking"
}

// bridgeSetupInstructions is kept for legacy references; rootless mode does not use a bridge.
func bridgeSetupInstructions() string {
	return "  nexus daemon start   # rootless mode: no bridge setup required"
}

// realSetupTAP creates a rootless TAP using passt (replaces the old tap-helper + bridge path).
// The passtProcess handle is returned as `any` and stored in the global registry for teardown.
func realSetupTAP(tapName, hostIP, subnetCIDR string) (any, error) {
	workDir := tapWorkDir(tapName)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("create tap workdir %s: %w", workDir, err)
	}
	pp, err := realSetupTAPPasst(tapName, workDir)
	if err != nil {
		return nil, err
	}
	// Register for teardown; realTeardownTAP looks up by tap name.
	registerTAP(tapName, pp)
	return pp, nil
}

// realTeardownTAP tears down the TAP + passt process associated with this tap name.
func realTeardownTAP(tapName, subnetCIDR string) {
	globalTapRegistry.mu.Lock()
	pp := globalTapRegistry.procs[tapName]
	delete(globalTapRegistry.procs, tapName)
	globalTapRegistry.mu.Unlock()
	teardownTAPPasst(pp)
}

// tapWorkDir returns the work directory for a tap's passt log.
// Uses the same pattern as the VM workdir root.
func tapWorkDir(tapName string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "nexus", "taps", tapName)
}

// checkBridge is kept for interface compatibility; rootless mode never needs a bridge.
func checkBridge() error {
	return nil
}

// checkTapHelper is kept for interface compatibility; rootless mode uses passt instead.
func checkTapHelper() error {
	return checkPasstAvailable()
}

// ── Global tap → passt process registry ───────────────────────────────────────
// Required because realTeardownTAP only receives the tap name, not the handle.

type tapRegistry struct {
	mu    sync.RWMutex
	procs map[string]*passtProcess
}

var globalTapRegistry = &tapRegistry{
	procs: make(map[string]*passtProcess),
}

// registerTAP stores a passtProcess in the global registry after setup.
func registerTAP(tapName string, pp *passtProcess) {
	globalTapRegistry.mu.Lock()
	globalTapRegistry.procs[tapName] = pp
	globalTapRegistry.mu.Unlock()
}

var _ = strings.TrimSpace // ensure strings is used (used by resolvePasstPath)
