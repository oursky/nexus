# E2E Test Suite

This directory contains black-box end-to-end tests (`//go:build e2e`) for Nexus daemon and CLI behavior.

## Layout

- `auth/` auth relay behavior
- `cli/` CLI user journeys
- `daemon/` node/daemon RPC behavior
- `fs/` file APIs
- `project/` project lifecycle
- `pty/` shell/exec/PTy lifecycle
- `spotlight/` TCP forwarding behavior
- `workspace/` workspace lifecycle, errors, protocol
- `harness/` shared daemon/CLI test harness
- `coverage/` formal verification spec coverage tooling

## Spec Annotation Convention

Tests that verify normative behavior include `Spec:` annotations above each `Test*` function.

Example:

```go
// Spec: VM-PROOF-002
func TestSpotlight_TCPProxyTraffic(t *testing.T) {
    ...
}
```
