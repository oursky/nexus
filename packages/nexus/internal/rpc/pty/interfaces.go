package pty

import (
	"context"
	"net"
)

// VsockDialer is implemented by VM runtime drivers to open an agent
// connection and map workspace IDs to guest workdirs.
type VsockDialer interface {
	AgentConn(ctx context.Context, workspaceID string) (net.Conn, error)
	GuestWorkdir(workspaceID string) string
}

// WorkspaceReadyChecker lets the PTY handler gate terminal creation on
// runtime-specific readiness checks (toolchain/bootstrap completed).
type WorkspaceReadyChecker interface {
	WorkspaceReady(ctx context.Context, workspaceID string) (bool, error)
}
