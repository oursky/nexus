package firecracker

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
)

// SSHAgentVSockPort is the vsock port used for SSH agent forwarding.
// When the guest does vsock.Dial(2, SSHAgentVSockPort), Firecracker routes
// the connection to the host unix socket at {workDir}/vsock.sock_10790.
const SSHAgentVSockPort = VendingVSockPort

// resolveSSHAuthSock returns the path to the host SSH agent socket.
// It checks $SSH_AUTH_SOCK first, then falls back to the systemd user-service
// socket at /run/user/<uid>/ssh-agent.sock.
func resolveSSHAuthSock() string {
	if s := os.Getenv("SSH_AUTH_SOCK"); s != "" {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	// Systemd user-session agent (created by our ssh-agent.service unit).
	if uid := os.Getuid(); uid >= 0 {
		candidate := fmt.Sprintf("/run/user/%d/ssh-agent.sock", uid)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// startSSHAgentProxy starts a unix-socket listener at
// {workDir}/vsock.sock_10790 and proxies each accepted guest connection to
// the host SSH agent.  It returns immediately and runs in the background.
// A no-op if no SSH agent socket is found.
func startSSHAgentProxy(workDir string) {
	authSock := resolveSSHAuthSock()
	if authSock == "" {
		log.Printf("[firecracker] ssh-agent proxy: no SSH agent socket found; skipping")
		return
	}

	// Firecracker routes guest→host vsock connections to:
	//   {relative-uds-path}_{port}  inside the VM workdir.
	// Our vsock device was configured with uds_path="vsock.sock", so the
	// host-side accept path is vsock.sock_10790 (absolute: workDir + that name).
	listenPath := filepath.Join(workDir, "vsock.sock_10790")
	_ = os.Remove(listenPath) // clean up any stale socket

	ln, err := net.Listen("unix", listenPath)
	if err != nil {
		log.Printf("[firecracker] ssh-agent proxy: listen %s: %v", listenPath, err)
		return
	}

	log.Printf("[firecracker] ssh-agent proxy: ready (forwarding %s → guest port %d)",
		authSock, SSHAgentVSockPort)

	go func() {
		defer ln.Close()
		for {
			guest, err := ln.Accept()
			if err != nil {
				return
			}
			go proxySSHAgentConn(guest, authSock)
		}
	}()
}

// proxySSHAgentConn tunnels one guest connection to the host SSH agent socket.
func proxySSHAgentConn(guest net.Conn, authSock string) {
	defer guest.Close()
	host, err := net.Dial("unix", authSock)
	if err != nil {
		log.Printf("[firecracker] ssh-agent proxy: dial %s: %v", authSock, err)
		return
	}
	defer host.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(host, guest); done <- struct{}{} }()
	go func() { _, _ = io.Copy(guest, host); done <- struct{}{} }()
	<-done
}
