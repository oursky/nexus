// Package toolchain provides a shared guest toolchain readiness probe used by
// the libkrun runtime driver (and process/seatbelt sandboxes where applicable).
//
// The probe connects to the guest agent via a vsock/unix connection, runs a
// shell command to verify that the expected AI CLI tools are present, and
// caches the result per workspace to avoid re-probing on every call.
package toolchain

import (
	"context"
	"fmt"
	"net"
	"time"
)

// AgentDialer opens a connection to the guest agent for a given workspace.
type AgentDialer func(ctx context.Context, workspaceID string) (net.Conn, error)

// AgentExecFn sends an exec request over conn and returns the exit code.
type AgentExecFn func(ctx context.Context, conn net.Conn, id, command string, args []string, workDir string) (exitCode int, err error)

// ProbeCommand is the shell command used to verify all expected CLI tools.
// It is intentionally kept as a single POSIX sh expression so both drivers
// can run it through the guest agent's exec channel unchanged.
const ProbeCommand = "sh"

// ProbeArgs are the arguments passed to ProbeCommand.
// Uses "which" instead of "--version" because claude --version exits 1 when
// stdout is redirected, giving false-negative probe results.
var ProbeArgs = []string{
	"-lc",
	"which codex >/dev/null 2>&1 && which opencode >/dev/null 2>&1 && which claude >/dev/null 2>&1",
}

// DockerProbeArgs verify the Docker daemon is running inside the VM.
var DockerProbeArgs = []string{
	"-lc",
	"docker info >/dev/null 2>&1",
}

// ToolNames is the canonical list of AI CLI tools expected in every workspace.
var ToolNames = []string{"codex", "opencode", "claude"}

// Prober manages per-workspace toolchain readiness state.
type Prober struct {
	dial  AgentDialer
	exec  AgentExecFn
	ready map[string]bool
}

// NewProber creates a Prober backed by the given dialer and exec implementations.
func NewProber(dial AgentDialer, execFn AgentExecFn) *Prober {
	return &Prober{
		dial:  dial,
		exec:  execFn,
		ready: make(map[string]bool),
	}
}

// IsReady returns true if the workspace toolchain has already been verified
// in this session (cached result).
func (p *Prober) IsReady(workspaceID string) bool {
	return p.ready[workspaceID]
}

// MarkReady sets the toolchain-ready flag for a workspace.
func (p *Prober) MarkReady(workspaceID string) {
	p.ready[workspaceID] = true
}

// Invalidate clears the cached readiness for a workspace (e.g. after stop).
func (p *Prober) Invalidate(workspaceID string) {
	delete(p.ready, workspaceID)
}

// Check probes the guest agent to confirm all CLI tools are installed and
// executable. Returns (true, nil) on success, (false, nil) when not yet ready
// (non-fatal), and (false, err) on a hard error.
func (p *Prober) Check(ctx context.Context, workspaceID string) (bool, error) {
	if p.ready[workspaceID] {
		return true, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for attempt := 1; attempt <= 3; attempt++ {
		attemptCtx, cancelAttempt := context.WithTimeout(probeCtx, 20*time.Second)
		conn, err := p.dial(attemptCtx, workspaceID)
		if err != nil {
			cancelAttempt()
			if attempt < 3 {
				select {
				case <-probeCtx.Done():
					return false, nil
				case <-time.After(300 * time.Millisecond):
				}
			}
			continue
		}

		reqID := fmt.Sprintf("toolchain-ready-%d", time.Now().UnixNano())
		exitCode, err := p.exec(attemptCtx, conn, reqID, ProbeCommand, ProbeArgs, "/workspace")
		_ = conn.Close()
		cancelAttempt()

		if err != nil || exitCode != 0 {
			if attempt < 3 {
				select {
				case <-probeCtx.Done():
					return false, nil
				case <-time.After(300 * time.Millisecond):
				}
			}
			continue
		}

		p.ready[workspaceID] = true
		return true, nil
	}
	return false, nil
}
