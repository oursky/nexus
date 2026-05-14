//go:build darwin

package macvm

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

// vsock mappings mirror linux libkrun default ports (guest agent listens on CID_ANY).
const (
	agentVSockPort     uint32 = 10789
	spotlightVSockPort uint32 = 10792
	guestSSHPortGuest  int    = 22
	guestAgentTCPPort  int    = 7777
)

func (m *Manager) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	val, ok := m.vms.Load(workspaceID)
	if !ok {
		return nil, fmt.Errorf("workspace not running: %s", workspaceID)
	}
	inst := val.(*vmInstance)

	if conn, err := dialUnixRetry(ctx, agentSockPath(inst.sockDir)); err == nil {
		return conn, nil
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", inst.agentPort))
	if err != nil {
		return nil, fmt.Errorf("macvm agent tcp %d or vsock unreachable: %w", inst.agentPort, err)
	}
	return conn, nil
}

// DialSpotlight opens a forwarded connection to remotePort inside the guest via spotlight vsock proxy.
func (m *Manager) DialSpotlight(ctx context.Context, workspaceID string, remotePort int) (net.Conn, error) {
	val, ok := m.vms.Load(workspaceID)
	if !ok {
		return nil, fmt.Errorf("workspace not running: %s", workspaceID)
	}
	inst := val.(*vmInstance)
	return dialSpotlightForward(ctx, spotlightSockPath(inst.sockDir), remotePort)
}

func (m *Manager) SerialLogPath(workspaceID string) (string, error) {
	path := filepath.Join(m.cfg.VMWorkDir, workspaceID, "hvc0.log")
	return path, nil
}

func agentSockPath(workDir string) string {
	return filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", agentVSockPort))
}

func spotlightSockPath(workDir string) string {
	return filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", spotlightVSockPort))
}

func dialUnixRetry(ctx context.Context, sock string) (net.Conn, error) {
	deadline := time.Now().Add(60 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dialer := &net.Dialer{Timeout: 2 * time.Second}
		conn, err := dialer.DialContext(ctx, "unix", sock)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("unix dial %s: %w", sock, lastErr)
}

// waitAgentListening polls the vsock-forwarded unix socket until the guest
// agent has started and libkrun has materialised the host-side socket file.
//
// The TCP/gvproxy port is intentionally NOT checked here: gvproxy listens on
// the host TCP port regardless of VM state, so a successful TCP dial does not
// mean the agent is running. Only the vsock unix socket is created by libkrun
// when the guest actually calls accept() on the vsock device, making it a
// reliable liveness signal.
func waitAgentListening(ctx context.Context, sockDir string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ud := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := ud.DialContext(ctx, "unix", agentSockPath(sockDir))
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for guest agent vsock unix socket %s", agentSockPath(sockDir))
}

func dialSpotlightForward(ctx context.Context, spotlightSock string, guestPort int) (net.Conn, error) {
	if guestPort <= 0 || guestPort > 65535 {
		return nil, fmt.Errorf("guestPort %d out of range", guestPort)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", spotlightSock)
	if err != nil {
		return nil, fmt.Errorf("spotlight dial %s: %w", spotlightSock, err)
	}
	if _, err := fmt.Fprintf(conn, "FORWARD %d\n", guestPort); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight FORWARD write: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight deadline: %w", err)
	}
	resp, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight FORWARD response: %w", err)
	}
	r := strings.TrimSpace(resp)
	if !strings.HasPrefix(r, "OK") {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight FORWARD rejected: %s", strings.TrimSuffix(r, "\r"))
	}
	return conn, nil
}
