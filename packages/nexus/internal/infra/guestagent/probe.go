package guestagent

import "time"

// Central guest-agent timing used by readiness probes, PTY preconditions, and
// libkrun agent socket polling. Values match the historical fixed timeouts
// (single place to adjust consistently).
const (
	probeDialTimeout           = 8 * time.Second
	agentSocketWaitTimeout     = 15 * time.Second
	readinessProbeOuterTimeout = 12 * time.Second
)

// ProbeDialDuration is the budget for a guest-agent dial + probe round-trip
// (readiness checks, shell.open ACK wait, etc.).
func ProbeDialDuration() time.Duration {
	return probeDialTimeout
}

// AgentSocketWaitDuration is how long libkrun host code retries dialing the
// agent vsock Unix socket while the VM may still be booting.
func AgentSocketWaitDuration() time.Duration {
	return agentSocketWaitTimeout
}

// ReadinessProbeOuterDuration wraps the async readiness probe goroutine
// (the span that bounds WorkspaceReady including inner work).
func ReadinessProbeOuterDuration() time.Duration {
	return readinessProbeOuterTimeout
}
