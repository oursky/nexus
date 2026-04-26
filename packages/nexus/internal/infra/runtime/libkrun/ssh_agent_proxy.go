//go:build linux

package libkrun

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// VendingVSockPort is the vsock port the guest connects to for SSH agent forwarding.
// Matches the historical VM driver constant so the same guest agent code works.
const VendingVSockPort uint32 = 10790

// startSSHAgentProxy starts a Unix socket listener that proxies guest vsock
// connections to the host SSH agent.
//
// libkrun maps guest vsock (CID 3, port VendingVSockPort) connections to the
// Unix socket at sockPath (listen=false: guest is the dialer).
// We accept those connections and proxy them to SSH_AUTH_SOCK.
func startSSHAgentProxy(sockPath string) {
	authSock := os.Getenv("SSH_AUTH_SOCK")
	if authSock == "" {
		log.Printf("[libkrun] SSH_AUTH_SOCK not set; SSH agent proxy disabled")
		return
	}

	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Printf("[libkrun] SSH agent proxy: listen on %s: %v", sockPath, err)
		return
	}

	log.Printf("[libkrun] SSH agent proxy listening on %s → %s", sockPath, authSock)

	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go proxySSHAgent(conn, authSock)
		}
	}()
}

func proxySSHAgent(guest net.Conn, authSock string) {
	defer guest.Close()

	agent, err := net.Dial("unix", authSock)
	if err != nil {
		log.Printf("[libkrun] SSH agent proxy: dial %s: %v", authSock, err)
		return
	}
	defer agent.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(agent, guest)
		_ = agent.(*net.UnixConn).CloseWrite()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(guest, agent)
	}()
	wg.Wait()
}

// sshAgentSockPath returns the Unix socket path for the SSH agent proxy of a workspace.
func sshAgentSockPath(workDir string) string {
	return filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", VendingVSockPort))
}
