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

## Lima CLI E2E preflight

Use preflight before running CLI E2E in Lima:

```bash
task test:e2e:cli:lima:preflight
```

Possible statuses:

- `READY`: prerequisites satisfied.
- `MISCONFIGURED`: required env/path/tooling settings are missing or invalid.
- `UNSUPPORTED_HOST`: guest host cannot satisfy Linux VM e2e requirements.
- `BOOTSTRAP_FAILED`: guest shell/bootstrap path is not healthy.

Required environment for full-stack CLI bundle tests:

- `NEXUS_E2E_REMOTE_PROFILE=1`
- `NEXUS_VM_KERNEL=<guest-visible kernel path>`
- `NEXUS_VM_ROOTFS=<guest-visible rootfs path>`

Then run:

```bash
task test:e2e:cli:lima
```
