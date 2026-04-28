---
type: master
feature_area: firecracker-complete-rootless
date: 2026-04-22
status: active
child_prds: []
---

# Firecracker Complete Rootless Flow

## Overview

This feature replaces the current Firecracker host bootstrap flow that relies on privileged host mutation (`sudo`, bridge creation, iptables, `/etc/systemd/network` writes, capability-bearing tap helper) with a fully rootless flow designed for seamless remote provisioning from the macOS app.

The target user experience is: provide SSH target once, app invokes `nexus daemon start`, Nexus performs idempotent user-scoped bootstrap internally, validates hard prerequisites, and reaches ready state without any interactive sudo prompts on fresh hosts.

This PRD explicitly requires fail-fast behavior when `/dev/kvm` is not readable and writable by the invoking user. No compatibility/emulation mode is provided for missing KVM access.

## Architecture

The design introduces a rootless runtime stack while preserving external daemon/CLI behavior.

1. **Rootless startup phases in `nexus daemon start` (user scope only)**
   - `nexus daemon start` includes internal phases: `preflight`, `asset-install`, `runtime-verify`, `daemon-launch`.
   - These phases install/refresh runtime artifacts under XDG user paths:
     - `~/.local/bin` (executables)
     - `~/.local/share/nexus` (VM artifacts)
     - `~/.local/state/nexus` (logs/state)
     - `~/.local/run/nexus` (sockets/pids)
   - No writes to `/var/lib/nexus`, `/etc/*`, system-wide networking config, or privileged capabilities.

2. **Hard prerequisite gate (fail-fast)**
   - Daemon startup validates:
     - Linux host
     - `/dev/kvm` exists and is `O_RDWR` accessible
     - required rootless networking binaries are available in user path
   - On failure, startup exits with actionable remediation and does not attempt degraded runtime.

3. **Rootless networking plane (Firecracker-compatible)**
   - Replace host-global bridge/TAP setup (`nexusbr0`, iptables, policy routing) with per-workspace rootless network namespaces.
   - Firecracker still uses TAP (required by Firecracker), but TAP is created inside each workspace network namespace owned by the user session.
   - **Network backend: `slirp4netns` with namespace holder + bridge (settled architecture)**
     - `pasta`/`passt` are technically incompatible with Firecracker and cannot be used as the uplink backend. Root cause: pasta creates a single-queue tap (`IFF_TAP | IFF_NO_PI`, no `IFF_MULTI_QUEUE`) and holds it open; Firecracker requires exclusive single-queue tap ownership → `EBUSY`. passt uses a QEMU Unix socket backend which Firecracker does not implement. The multi-queue workaround is also blocked: Firecracker explicitly rejects `IFF_MULTI_QUEUE` taps with `EINVAL` (upstream issue #750). This incompatibility is fundamental and not resolvable without patching Firecracker itself.
     - Implemented architecture (per workspace): a persistent `sleep infinity` namespace holder process creates a `CLONE_NEWUSER | CLONE_NEWNET` namespace. Inside this namespace, `ip tuntap add` creates `tap0` (Firecracker's TAP) and `tap1` (slirp4netns uplink), bridged via `br0`. Firecracker and `slirp4netns` are each started with `nsenter --user --preserve-credentials --net` into this holder namespace, each owning their respective single-queue TAP exclusively. slirp4netns is started with `--api-socket` to enable dynamic `add_hostfwd` port forwarding for SSH access (`127.0.0.1:RANDOM_PORT → VM_IP:22`).
   - Networking responsibilities:
     - VM egress NAT without host firewall mutation
     - explicit port publish/forward for host reachability (SSH via slirp4netns API)
     - deterministic per-workspace network lifecycle tied to VM PID

4. **Firecracker runtime integration**
   - Firecracker manager launches with rootless network attachment strategy and user-owned workspace artifact paths.
   - Remove dependency on `nexus-tap-helper` and `cap_net_admin` checks in host prerequisite validation.
   - Preserve existing workspace lifecycle semantics (`create/start/stop/shell/spotlight`) at interface level.

5. **macOS app provisioning flow update**
   - `RemoteProvisioner` performs one-shot remote startup:
     1) upload/install correct Linux binary
     2) run `nexus daemon start`
     3) poll healthz
   - UI progress states include rootless prerequisite and bootstrap phases so users see deterministic progress, not opaque daemon failure.

### Data Flow

```text
Mac app (RemoteProvisioner)
    │ SSH (non-interactive)
    ▼
`nexus daemon start` (remote user process)
    │ runs preflight + idempotent rootless bootstrap + spawns Firecracker + netns TAP + userspace uplink
    ▼
VM workloads + spotlight/PTY interfaces (unchanged external contracts)
```

## Data Model

### Rootless host state

```json
{
  "schema_version": 1,
  "installed_at": "2026-04-22T00:00:00Z",
  "nexus_version": "x.y.z",
  "firecracker_bin": "~/.local/bin/firecracker",
  "network_backend": "netns-slirp4netns",
  "network_backend_bin": "~/.local/bin/slirp4netns",
  "tap_name": "nexus-tap0",
  "kernel_path": "~/.local/share/nexus/vm/vmlinux.bin",
  "rootfs_path": "~/.local/share/nexus/vm/rootfs.ext4",
  "kvm_access": "ok"
}
```

Persist at: `~/.local/share/nexus/rootless/state.json`

### Bootstrap report (machine-readable)

```json
{
  "ok": true,
  "checks": {
    "linux_host": true,
    "kvm_access": true,
    "network_backend": true,
    "assets_present": true
  },
  "actions": ["install_firecracker", "install_network_backend", "refresh_rootfs_agent"],
  "warnings": []
}
```

## API / Interface

1. **CLI commands**
   - Update: `nexus daemon start`
     - performs idempotent user-scoped rootless bootstrap when backend is firecracker
     - emits structured phase progress (`preflight`, `asset-install`, `runtime-verify`, `daemon-launch`) with `--json`
     - exits non-zero on missing `/dev/kvm`
     - does not attempt privileged re-exec (`sudo`, `sg`, tap-helper setup)
   - Add/extend: `nexus doctor` checks for rootless Firecracker prerequisites and reports structured failures.

2. **Remote provisioning contract (macOS app)**
   - `RemoteProvisioner` sequence:
     - `daemon start`
     - `healthz` poll
   - The app maps `daemon start --json` phase events into UI progress updates.
   - SSH commands remain non-interactive and BatchMode-compatible.

3. **Environment/config surface**
   - `NEXUS_RUNTIME_BACKEND=firecracker`
   - `NEXUS_ROOTLESS_NETWORK_BACKEND=slirp4netns` (only supported option; pasta/passt are incompatible with Firecracker — see Architecture §3)
   - `NEXUS_FIRECRACKER_KERNEL`, `NEXUS_FIRECRACKER_ROOTFS` default to user-scoped paths when unset.

4. **Backward compatibility**
   - Existing RPC and CLI workspace lifecycle commands remain intact.
   - Existing privileged setup script path is deprecated and removed from default flow after rollout.

## Error Handling

- **`prerequisite_error.kvm_access`**
  - Trigger: `/dev/kvm` open with `O_RDWR` fails.
  - Message: clear remediation (`add user to kvm group`, `re-login`) and exact failed path.
  - Behavior: fail fast; no compatibility fallback.

- **`prerequisite_error.network_backend_missing`**
  - Trigger: configured rootless uplink backend binary unavailable/unexecutable.
  - Behavior: bootstrap fails with install guidance; daemon start blocked.

- **`bootstrap_error.asset_install`**
  - Trigger: kernel/rootfs/artifact fetch or file permission error in user paths.
  - Behavior: retries for transient download failures; hard fail for permission/path errors.

- **`runtime_error.network_attach`**
  - Trigger: userspace networking process fails to attach/initialize for workspace.
  - Behavior: workspace start fails; process logs include backend stderr tail.

- **`provision_error.remote_daemon_start`**
  - Trigger: macOS app remote `nexus daemon start` exits non-zero during any startup phase.
  - Behavior: UI surfaces phase name, exact stderr excerpt, and remediation hint.

## Known Limitations

- Linux + KVM is mandatory for Firecracker backend. Missing `/dev/kvm` access is a hard failure by design.
- Firecracker requires TAP. Strict rootless mode therefore depends on user namespace support plus `/dev/net/tun` access to create TAP inside user-owned network namespaces.
- Userspace uplink throughput and packet-rate performance are expected to be lower than host bridge/TAP kernel networking, especially under sustained high-throughput traffic.
- Rootless mode provides explicit port publishing/forwarding; direct L2 bridge semantics are not available.
- Host environments with restrictive kernel/user-namespace policies may require manual admin enablement before rootless networking can function.

## Task Graph

Define implementation tasks with clear dependency and file-boundary ownership to support parallel execution.

### Task List

| ID | Task | Depends On | Owner / Agent | Files Touched | Est. |
|----|------|-----------|---------------|---------------|------|
| T1 | Add rootless architecture constants, XDG path layout, and state model | — | coder | `packages/nexus/cmd/nexus/*.go`, `packages/nexus/internal/infra/config/*` | 1d |
| T2 | Integrate rootless bootstrap phases directly into `nexus daemon start` with structured phase events and idempotent actions | T1 | coder | `packages/nexus/cmd/nexus/main.go`, `packages/nexus/cmd/nexus/setup_firecracker*.go` | 1.5d |
| T3 | Implement fail-fast `/dev/kvm` prerequisite gate and remove privileged re-exec branches for Firecracker start path | T1 | coder | `packages/nexus/cmd/nexus/main.go`, `packages/nexus/cmd/nexus/main_test.go` | 1d |
| T4 | Add Firecracker-compatible rootless networking: per-workspace netns + TAP + userspace uplink (`slirp4netns`/`pasta`) | T1 | coder | `packages/nexus/internal/infra/runtime/firecracker/*` | 2d |
| T5 | Remove tap-helper/bridge privileged assumptions from doctor and startup validation paths | T3,T4 | coder | `packages/nexus/cmd/nexus/main.go`, `packages/nexus/cmd/nexus/main_test.go` | 1d |
| T6 | Update macOS `RemoteProvisioner` to use one-shot `nexus daemon start` flow and map phase events into progress/error states | T2,T3 | coder | `packages/nexus-swift/Sources/NexusCore/RemoteProvisioner.swift`, related UI state surfaces | 1d |
| T7 | Add integration/e2e suite for fresh-host rootless provisioning and daemon readiness over SSH using one-shot start | T2,T4,T6 | coder | `packages/nexus/cmd/nexus/*_test.go`, `packages/nexus-swift/*Tests*` | 2d |
| T8 | Add rootless networking benchmark harness (throughput + latency) and acceptance thresholds | T4 | coder | `scripts/`, `packages/nexus/cmd/nexus/*`, `docs/dev/internal/testing/*` | 1d |
| T9 | Rollout and cleanup: deprecate privileged setup script path from default flow, keep migration notes | T5,T7 | coder | `packages/nexus/cmd/nexus/scripts/firecracker-setup.sh`, docs under `docs/reference/` and `docs/guides/` | 0.5d |
| T10 | Build deterministic clean-host + headless RPC regression harness with exact command fixtures and CI gates | T7,T8 | coder | `scripts/remote/*`, `packages/nexus-swift/*Tests*`, `.github/workflows/*` | 1d |

### Dependency Graph

```text
T1 ──▶ T2 ──▶ T6 ──▶ T7 ──▶ T9
  └──▶ T3 ──▶ T5 ──┘
  └──▶ T4 ──▶ T5
           └──▶ T7
           └──▶ T8
T7, T8 ──▶ T10
```

### Parallelization Rules

- T2 and T3 can run in parallel after T1 if edits are split by command files and reconciled once.
- T4 is independent after T1 and should run in parallel with T2/T3 to minimize critical path.
- T6 must wait for T2/T3 contract freeze (`nexus daemon start` phase/event contract).
- T7 starts only after T2, T4, and T6 are complete.
- T8 can run as soon as T4 is stable; it does not block functional correctness but is required for rollout acceptance.
- T9 is sequential finalization and must not start until T5 and T7 pass.
- T10 starts after T7 and T8 to avoid unstable fixture contracts.

## Test Plan

This plan validates both fresh-user onboarding and regression safety across CLI, daemon, runtime, and macOS app integration.

### Environments

- Linux host matrix: Ubuntu 22.04/24.04, Debian 12, both `x86_64` and `arm64` where available.
- macOS app runner matrix: Apple Silicon primary, Intel optional compatibility run.
- Network conditions: normal network and high-latency SSH path (simulated with `tc` in CI environment where available).

### Fixture Contract (Exact Commands)

All clean-host tests run with explicit fixtures and no hidden local state.

1. **Remote clean-room fixture (implode-equivalent)**

```bash
ssh <user@host> 'set -euo pipefail; \
  pkill -f "nexus daemon start" 2>/dev/null || true; \
  rm -rf "$HOME/.local/share/nexus" "$HOME/.local/state/nexus" "$HOME/.local/run/nexus"; \
  rm -f "$HOME/.local/bin/firecracker" "$HOME/.local/bin/slirp4netns" "$HOME/.local/bin/pasta"; \
  mkdir -p "$HOME/.local/bin"'
```

2. **macOS app headless RPC activation**

```bash
touch "$HOME/.nexus-headless-rpc"
curl -sf http://127.0.0.1:7778/status
```

3. **Remote daemon readiness probe**

```bash
ssh <user@host> 'curl -sf --max-time 2 http://127.0.0.1:<port>/healthz'
```

### Golden End-to-End Journey (Clean Host)

For each Linux target, run from absolute clean state:

1. Run remote clean-room fixture and assert prior state is empty (`~/.local/share/nexus`, `~/.local/state/nexus`, `~/.local/run/nexus`).
2. Start app in headless mode and assert `GET /status` succeeds.
3. Trigger connect/provision for a new daemon profile from app automation.
4. Assert exactly one remote startup command is used: `nexus daemon start`.
5. Assert no remote stderr/stdout contains privileged escalation paths (`sudo`, `sg kvm`, `setcap`, `/etc/systemd/network`, `iptables`).
6. Wait for remote healthz success and verify startup phase order: `preflight -> asset-install -> runtime-verify -> daemon-launch`.
7. Create workspace, start workspace, run guest smoke (`echo nexus-rootless-ok`), start spotlight, and verify forwarded HTTP port from macOS.
8. Open terminal tab via headless RPC, write command, read output, clear buffer, and verify expected responses.
9. Restart daemon with same profile; verify idempotent fast path (asset-install phase no-op) and workspace reconnect.

### Headless RPC Coverage (macOS App)

- Add app-driven e2e that uses existing headless RPC endpoints with explicit request/response assertions:

```bash
curl -sf http://127.0.0.1:7778/status
curl -sf http://127.0.0.1:7778/terminal/tabs
curl -sf -X POST http://127.0.0.1:7778/terminal/open -H "Content-Type: application/json" -d '{"workspaceID":"<id>","name":"smoke"}'
curl -sf -X POST http://127.0.0.1:7778/terminal/write -H "Content-Type: application/json" -d '{"tabID":"<tab>","text":"echo headless-ok\\n"}'
curl -sf "http://127.0.0.1:7778/terminal/read?tabID=<tab>"
curl -sf -X POST http://127.0.0.1:7778/terminal/clear -H "Content-Type: application/json" -d '{"tabID":"<tab>"}'
curl -sf -X POST http://127.0.0.1:7778/workspace/ssh-check -H "Content-Type: application/json" -d '{"workspaceID":"<id>"}'
```

- Validate user-visible phase messages map to daemon phase events and include actionable errors.
- Assert no regression to existing headless RPC endpoint contracts introduced by provisioning updates.

### Regression Suites

- **Legacy command parity**: `workspace create/start/stop/shell`, spotlight start/stop, daemon connect flows.
- **Failure-path UX**:
  - `/dev/kvm` inaccessible -> deterministic fail-fast with remediation
  - rootless uplink backend (`slirp4netns` or configured alternative) missing -> deterministic fail-fast with remediation
  - transient asset download failure -> retry + surfaced final error if exhausted
- **No-privilege guarantee**:
  - CI guard fails if startup logs include `sudo`, `sg`, `setcap`, `iptables`, `ip rule add`, `/etc/systemd/network`
- **State idempotency**:
  - repeated `nexus daemon start` on already-prepared host remains fast and no-op for install phases
  - no duplicate artifacts/process leaks across 10 repeated start/stop cycles.

### Performance and Reliability Gates

- Startup SLO from clean host to healthz ready: define baseline and max tolerated regression.
- Userspace networking benchmark gate (throughput + p95 latency) recorded per environment and compared against previous commits.
- Long-run soak: 2-hour workspace uptime with periodic PTY and spotlight activity; zero daemon crashes and no orphan network backend processes.

### Acceptance Criteria

- Fresh host provisioning succeeds end-to-end from macOS app with no sudo interaction.
- One-shot `nexus daemon start` fully replaces prior privileged bootstrap path for supported rootless environments.
- All regression suites pass with zero critical failures.
- Fail-fast behavior for missing `/dev/kvm` is deterministic and consistently surfaced in CLI and app UX.

## Steer Log

### 2026-04-22 — Pivot from hybrid privileged setup to complete rootless flow

- **Trigger**: User explicitly requested complete rootless design for best UX.
- **From**: Hybrid model with rootless daemon and one-time privileged helper for networking/isolation setup.
- **To**: Complete rootless provisioning and runtime flow with no interactive sudo in fresh-host path.
- **Rationale**: Primary product goal is seamless first-run onboarding through macOS app without privileged host setup steps.
- **Affected sections**: Overview, Architecture, API / Interface, Known Limitations, Task Graph.

### 2026-04-22 — Enforce KVM fail-fast with no compatibility mode

- **Trigger**: User required no compatibility mode when `/dev/kvm` is unavailable.
- **From**: Prior direction considered optional degraded mode when KVM access is missing.
- **To**: Mandatory `/dev/kvm` read-write access gate; startup fails immediately if unmet.
- **Rationale**: Keeps runtime behavior deterministic and avoids low-confidence fallback paths.
- **Affected sections**: Overview, Architecture, Error Handling, Known Limitations, Task Graph.

### 2026-04-22 — Fold bootstrap into `nexus daemon start` and require full clean-host regression plan

- **Trigger**: User requested no standalone `bootstrap-rootless` command and asked for thorough test design from clean remote daemon state through macOS app headless RPC.
- **From**: Separate bootstrap command + daemon start, with generic integration tests.
- **To**: One-shot daemon start with internal rootless startup phases, plus explicit end-to-end clean-host/no-regression test plan.
- **Rationale**: Keeps UX minimal for first-time users and ensures measurable confidence for complete flow replacement.
- **Affected sections**: Overview, Architecture, API / Interface, Error Handling, Task Graph.

### 2026-04-22 — Expand regression design with exact command fixtures

- **Trigger**: User requested a thorough test plan from absolute clean remote daemon state through macOS headless RPC with no-regression guarantees.
- **From**: High-level test phases without concrete command contracts.
- **To**: Explicit fixture commands, headless RPC request examples, no-privilege CI guard rules, and a dedicated harness task (`T10`).
- **Rationale**: Precise command-level fixtures reduce ambiguity and make clean-host regression automation reproducible across environments.
- **Affected sections**: Task Graph, Test Plan.

### 2026-04-22 — Correct networking plan for Firecracker TAP-only attachment

- **Trigger**: User questioned `passt/pasta` compatibility with Firecracker's TAP networking model.
- **From**: PRD wording implied `passt/pasta` as direct Firecracker network backend.
- **To**: Firecracker-compatible rootless networking with per-workspace netns + TAP (inside namespace) + userspace uplink (`slirp4netns` default, `pasta` optional).
- **Rationale**: Firecracker attaches NICs via TAP; userspace uplink complements this path but does not replace TAP attachment directly.
- **Affected sections**: Architecture, Data Model, API / Interface, Error Handling, Known Limitations, Task Graph, Test Plan.
