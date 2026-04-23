//go:build linux

package firecracker

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	// slirp4netnsGatewayIP is the gateway IP that slirp4netns always places at
	// <cidr>.2 within its default 10.0.2.0/24 subnet.
	slirp4netnsGatewayIP = "10.0.2.2"

	// tapFCName is the TAP device inside the per-VM user netns that Firecracker opens.
	// Each VM gets its own isolated netns so "tap0" is unambiguous.
	tapFCName = "tap0"

	// tapSlirpName is the TAP device inside the per-VM user netns that slirp4netns owns.
	// It is bridged with tapFCName so the VM can reach slirp4netns without conflict.
	tapSlirpName = "tap1"

	// vmBridgeName is the Linux bridge inside the VM's user netns connecting the two TAPs.
	vmBridgeName = "br0"
)

// netnsHolder tracks a namespace-holder process that keeps the user+net namespace alive.
type netnsHolder struct {
	proc *os.Process
	pid  int
}

// slirpProcess tracks a running slirp4netns instance for a workspace VM.
type slirpProcess struct {
	proc        *os.Process
	tapName     string
	apiSocket   string
	sshHostPort int // host-side port forwarded to VM:22 via slirp4netns API
}

// ── Namespace holder ──────────────────────────────────────────────────────────

// startNetnsHolder starts a long-lived `sleep infinity` process inside a new
// user+network namespace. The holder keeps the namespace alive; all other
// processes (Firecracker, slirp4netns) join it via nsenter.
func startNetnsHolder(hostUID, hostGID int) (*netnsHolder, error) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		sleepBin = "/bin/sleep"
	}
	cmd := exec.Command(sleepBin, "infinity")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: hostUID, Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: hostGID, Size: 1},
		},
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start netns holder: %w", err)
	}
	return &netnsHolder{proc: cmd.Process, pid: cmd.Process.Pid}, nil
}

// setupNetnsNetworking runs nsenter into the holder's namespace and configures:
//   - tap0  (tapFCName)    — used by Firecracker
//   - tap1  (tapSlirpName) — used by slirp4netns
//   - br0   (vmBridgeName) — bridge connecting the two TAPs
//
// No IP is placed on br0; slirp4netns configures IP routing when it starts.
func setupNetnsNetworking(holderPid int) error {
	nsenterBin, _ := exec.LookPath("nsenter")
	if nsenterBin == "" {
		nsenterBin = "/usr/bin/nsenter"
	}

	script := fmt.Sprintf(`set -e
ip tuntap add dev %s mode tap
ip tuntap add dev %s mode tap
ip link add %s type bridge
ip link set %s master %s
ip link set %s master %s
ip link set %s up
ip link set %s up
ip link set %s up`,
		tapFCName, tapSlirpName,
		vmBridgeName,
		tapFCName, vmBridgeName,
		tapSlirpName, vmBridgeName,
		tapFCName, tapSlirpName, vmBridgeName,
	)

	cmd := exec.Command(
		nsenterBin,
		fmt.Sprintf("--user=/proc/%d/ns/user", holderPid),
		"--preserve-credentials",
		fmt.Sprintf("--net=/proc/%d/ns/net", holderPid),
		"--",
		"/bin/sh", "-c", script,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("setup netns networking (holder %d): %w\n%s", holderPid, err, string(out))
	}
	return nil
}

// nsenterFirecrackerCmd rewrites cmd so that Firecracker executes inside the
// holder's network namespace via nsenter.  The original binary and arguments
// are preserved; only the executable is wrapped with nsenter.
func nsenterFirecrackerCmd(cmd *exec.Cmd, holderPid int) {
	nsenterBin, _ := exec.LookPath("nsenter")
	if nsenterBin == "" {
		nsenterBin = "/usr/bin/nsenter"
	}
	originalPath := cmd.Path
	originalArgs := cmd.Args[1:] // skip argv[0]

	nsenterArgs := []string{
		"nsenter",
		fmt.Sprintf("--user=/proc/%d/ns/user", holderPid),
		"--preserve-credentials",
		fmt.Sprintf("--net=/proc/%d/ns/net", holderPid),
		"--",
		originalPath,
	}
	nsenterArgs = append(nsenterArgs, originalArgs...)

	cmd.Path = nsenterBin
	cmd.Args = nsenterArgs
}

// ── slirp4netns ───────────────────────────────────────────────────────────────

// startSlirp4NetnsForHolder runs slirp4netns to provide NAT for the namespace
// via tapSlirpName.  It uses --netns-type=path so the holder PID (not the
// Firecracker PID) identifies the namespace.  slirp4netns enters the namespace
// and opens tap1, which is already bridged to tap0 (Firecracker's interface).
//
// An API socket is created so we can later add host→guest port forwards via
// addSlirp4netnsHostFwd (e.g. for SSH access from the engine host to the VM).
//
// Network layout inside the namespace:
//
//	VM virtio-net → Firecracker → tap0 ─── br0 ─── tap1 → slirp4netns → host NAT
//
// guestIP is the VM's static IP (from guestIPForCID); it is used to add an SSH
// port forward once slirp4netns is ready.
func startSlirp4NetnsForHolder(holderPid int, workDir string, guestIP string) (*slirpProcess, error) {
	slirpBin, err := exec.LookPath("slirp4netns")
	if err != nil {
		slirpBin = "/usr/bin/slirp4netns"
	}

	// Ready-pipe: slirp4netns writes 1 byte when the network is configured.
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("slirp4netns ready-pipe: %w", err)
	}

	apiSocket := filepath.Join(workDir, "slirp4netns.sock")
	logPath := filepath.Join(workDir, "slirp4netns.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		readyR.Close()
		readyW.Close()
		return nil, fmt.Errorf("slirp4netns log: %w", err)
	}

	cmd := exec.Command(
		slirpBin,
		"--configure",                                         // configure tap IP + default route in the namespace
		"--mtu=1500",
		fmt.Sprintf("--ready-fd=%d", 3),                      // fd 3 = write end of readyPipe
		fmt.Sprintf("--api-socket=%s", apiSocket),            // enable port-forward API
		"--netns-type=path",                                   // specify namespace by path, not by PID
		fmt.Sprintf("--userns-path=/proc/%d/ns/user", holderPid),
		fmt.Sprintf("/proc/%d/ns/net", holderPid),
		tapSlirpName,
	)
	cmd.ExtraFiles = []*os.File{readyW} // becomes fd 3 in the child
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		readyR.Close()
		readyW.Close()
		logFile.Close()
		return nil, fmt.Errorf("start slirp4netns for holder %d: %w\n  Check: %s --version", holderPid, err, slirpBin)
	}
	// Only the child holds the write end; close our copy.
	readyW.Close()
	logFile.Close()

	// Wait for the ready signal (with timeout).
	readyCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, readErr := readyR.Read(buf)
		readyR.Close()
		readyCh <- readErr
	}()

	select {
	case err := <-readyCh:
		if err != nil && err.Error() != "EOF" {
			cmd.Process.Kill()
			return nil, fmt.Errorf("slirp4netns ready-fd error: %w  (log: %s)", err, logPath)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		return nil, fmt.Errorf("slirp4netns timed out waiting for network ready (log: %s)", logPath)
	}

	sp := &slirpProcess{proc: cmd.Process, tapName: tapSlirpName, apiSocket: apiSocket}
	log.Printf("[firecracker] slirp4netns ready for holder %d (tap=%s)", holderPid, tapSlirpName)

	// Add an SSH port forward so the engine host can reach the VM over SSH.
	// Uses a deterministic port derived from the guest IP to avoid collisions.
	if guestIP != "" {
		sshPort, fwdErr := addSlirp4netnsHostFwd(apiSocket, guestIP, 22)
		if fwdErr != nil {
			log.Printf("[firecracker] WARNING: could not add SSH host-forward for %s: %v", guestIP, fwdErr)
		} else {
			sp.sshHostPort = sshPort
			log.Printf("[firecracker] SSH host-forward ready: 127.0.0.1:%d → %s:22", sshPort, guestIP)
		}
	}

	return sp, nil
}

// addSlirp4netnsHostFwd adds a TCP port forward via the slirp4netns API socket.
// It binds to 127.0.0.1:0 on the host to discover a free ephemeral port, then
// instructs slirp4netns to forward connections on that port to guestIP:guestPort.
// Returns the assigned host port on success.
func addSlirp4netnsHostFwd(apiSocket, guestIP string, guestPort int) (int, error) {
	// Pick a free ephemeral port on 127.0.0.1.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	hostPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	req := map[string]any{
		"execute": "add_hostfwd",
		"arguments": map[string]any{
			"proto":      "tcp",
			"host_addr":  "127.0.0.1",
			"host_port":  hostPort,
			"guest_addr": guestIP,
			"guest_port": guestPort,
		},
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal add_hostfwd: %w", err)
	}

	// slirp4netns API requires the client to shut down the write half after
	// sending the request, then read the response.
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: apiSocket, Net: "unix"})
	if err != nil {
		return 0, fmt.Errorf("dial slirp4netns api socket %s: %w", apiSocket, err)
	}
	defer conn.Close()

	if _, err := conn.Write(reqBytes); err != nil {
		return 0, fmt.Errorf("write add_hostfwd: %w", err)
	}
	if err := conn.CloseWrite(); err != nil {
		return 0, fmt.Errorf("close write end: %w", err)
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		return 0, fmt.Errorf("read add_hostfwd response: %w", err)
	}

	// Response should be {"return": {}} on success.
	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("parse add_hostfwd response %q: %w", string(resp), err)
	}
	if errVal, hasErr := result["error"]; hasErr {
		return 0, fmt.Errorf("slirp4netns add_hostfwd error: %v", errVal)
	}

	return hostPort, nil
}

// getSlirpSSHTarget returns the host-visible SSH endpoint for a workspace VM.
// If a port forward is active it returns "127.0.0.1:PORT"; otherwise "".
func getSlirpSSHTarget(wsID string) string {
	globalSlirpRegistry.mu.RLock()
	sp := globalSlirpRegistry.procs[wsID]
	globalSlirpRegistry.mu.RUnlock()
	if sp == nil || sp.sshHostPort == 0 {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", sp.sshHostPort)
}

// stopSlirp gracefully stops a slirp4netns process.
func stopSlirp(sp *slirpProcess) {
	if sp == nil || sp.proc == nil {
		return
	}
	sp.proc.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { sp.proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		sp.proc.Kill()
	}
}

// killHolder terminates the namespace holder process.
func killHolder(h *netnsHolder) {
	if h == nil || h.proc == nil {
		return
	}
	h.proc.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { h.proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		h.proc.Kill()
	}
}

// defaultVMNetworkGatewayIP returns the slirp4netns gateway IP on Linux.
// This overrides the legacy bridge gateway so the guest static-IP fallback
// routes through the correct slirp4netns-provided gateway.
func defaultVMNetworkGatewayIP() string { return slirp4netnsGatewayIP }

// ── Manager hook implementations (Linux) ─────────────────────────────────────

// defaultHostDevName returns "tap0" — the fixed TAP name that Firecracker
// opens inside each VM's isolated network namespace.
func defaultHostDevName(_ string) string { return tapFCName }

// defaultTapPrepare:
//  1. Starts a namespace holder (user+net namespace) keyed by tapName.
//  2. Configures the bridge + TAP devices inside the namespace via nsenter.
//  3. Rewrites cmd to run Firecracker via nsenter so it starts inside the
//     pre-configured netns without needing host-level CAP_NET_ADMIN.
func defaultTapPrepare(tapName string, cmd *exec.Cmd) {
	holder, err := startNetnsHolder(os.Getuid(), os.Getgid())
	if err != nil {
		log.Printf("[firecracker] ERROR starting netns holder for %s: %v", tapName, err)
		return
	}

	// Brief pause to ensure the user namespace mapping is applied before nsenter.
	time.Sleep(50 * time.Millisecond)

	if err := setupNetnsNetworking(holder.pid); err != nil {
		log.Printf("[firecracker] ERROR setting up netns networking for %s: %v", tapName, err)
		holder.proc.Kill()
		holder.proc.Wait()
		return
	}

	registerHolder(tapName, holder)
	nsenterFirecrackerCmd(cmd, holder.pid)
}

// defaultTapAttach starts slirp4netns for the workspace's namespace holder.
// It uses the holder PID stored by defaultTapPrepare (keyed by tapName).
// cid is the VM guest CID used to derive the VM's static IP for SSH port forwarding.
func defaultTapAttach(workspaceID string, _ int, workDir string, cid uint32) error {
	tapName := tapNameForWorkspace(workspaceID)
	holder := getHolder(tapName)
	if holder == nil {
		return fmt.Errorf("no netns holder for workspace %s (tap %s): tapPrepare may have failed", workspaceID, tapName)
	}

	guestIP := guestIPForCID(cid)
	sp, err := startSlirp4NetnsForHolder(holder.pid, workDir, guestIP)
	if err != nil {
		return err
	}
	registerSlirp(workspaceID, sp)
	return nil
}

// defaultTapNetTeardown stops slirp4netns and kills the namespace holder for a workspace.
func defaultTapNetTeardown(workspaceID string) {
	tapName := tapNameForWorkspace(workspaceID)
	sp := unregisterSlirp(workspaceID)
	stopSlirp(sp)
	holder := unregisterHolder(tapName)
	killHolder(holder)
}

// ── Holder registry (keyed by tapName) ───────────────────────────────────────

type holderRegistry struct {
	mu      sync.RWMutex
	holders map[string]*netnsHolder
}

var globalHolderRegistry = &holderRegistry{
	holders: make(map[string]*netnsHolder),
}

func registerHolder(tapName string, h *netnsHolder) {
	globalHolderRegistry.mu.Lock()
	globalHolderRegistry.holders[tapName] = h
	globalHolderRegistry.mu.Unlock()
}

func getHolder(tapName string) *netnsHolder {
	globalHolderRegistry.mu.RLock()
	h := globalHolderRegistry.holders[tapName]
	globalHolderRegistry.mu.RUnlock()
	return h
}

func unregisterHolder(tapName string) *netnsHolder {
	globalHolderRegistry.mu.Lock()
	h := globalHolderRegistry.holders[tapName]
	delete(globalHolderRegistry.holders, tapName)
	globalHolderRegistry.mu.Unlock()
	return h
}

// ── Slirp registry (keyed by workspaceID) ─────────────────────────────────────

type slirpRegistry struct {
	mu    sync.RWMutex
	procs map[string]*slirpProcess
}

var globalSlirpRegistry = &slirpRegistry{
	procs: make(map[string]*slirpProcess),
}

func registerSlirp(wsID string, sp *slirpProcess) {
	globalSlirpRegistry.mu.Lock()
	globalSlirpRegistry.procs[wsID] = sp
	globalSlirpRegistry.mu.Unlock()
}

func unregisterSlirp(wsID string) *slirpProcess {
	globalSlirpRegistry.mu.Lock()
	sp := globalSlirpRegistry.procs[wsID]
	delete(globalSlirpRegistry.procs, wsID)
	globalSlirpRegistry.mu.Unlock()
	return sp
}
