---
type: master
feature_area: krun-runtime-parity
date: 2026-04-21
status: active
child_prds: []
---

# KRun Runtime Parity

## Overview

This feature provides production-grade `krun` runtime support in Nexus with full lifecycle parity for `fork`, `snapshot`, and `restore`, plus PTY/spotlight behavior that matches Firecracker semantics for isolated workspaces.

The current `krun` path is an experimental bootstrap that proves daemon/backend wiring and basic lifecycle orchestration through `smolvm`. It does not provide stateful runtime snapshotting, runtime-level fork semantics, or deterministic restore guarantees.

This PRD defines the complete implementation required to make `krun` a first-class backend, including direct `libkrun` integration for stable control surfaces and a compatibility bridge for existing CLI/RPC behavior.

## Architecture

The system is structured as a layered runtime backend under `internal/infra/runtime/krun` with a thin domain adapter and explicit control/data channels.

1. **Runtime Control Layer (Go + cgo binding)**
   - Nexus runtime driver calls a `libkrun` wrapper package for VM lifecycle (`create/start/stop/pause/resume/snapshot/restore/clone/destroy`).
   - Workspace identity maps to deterministic VM handles and persisted metadata.

2. **State Layer (Snapshot + Fork Model)**
   - Snapshot produces a durable snapshot bundle (runtime state + disk delta + metadata manifest).
   - Restore reconstructs VM state from snapshot bundle with compatibility validation.
   - Fork supports two modes:
     - **cold fork** (filesystem lineage only)
     - **runtime fork** (snapshot-based clone when source is running)

3. **Guest I/O Layer**
   - PTY and spotlight use a backend-agnostic guest control channel abstraction.
   - Firecracker-specific vsock assumptions are removed from higher layers; krun supplies equivalent port/session dialing behavior.

4. **Compatibility Layer**
   - Existing RPC contracts remain unchanged.
   - Backend-specific details are internal to runtime implementation and capability reporting.

## Data Model

- **KrunInstance**
  - `workspace_id: string`
  - `backend: "krun"`
  - `state: created|running|paused|stopped|removed`
  - `vm_handle: string`
  - `project_root: string`
  - `created_at: time`
  - `updated_at: time`

- **KrunSnapshotManifest**
  - `snapshot_id: string`
  - `workspace_id: string`
  - `runtime_version: string`
  - `kernel_abi: string`
  - `disk_layers: []string`
  - `memory_state_path: string`
  - `created_at: time`

- **KrunForkLineage**
  - `parent_workspace_id: string`
  - `child_workspace_id: string`
  - `mode: cold|runtime`
  - `source_snapshot_id: string?`

## API / Interface

- **Runtime backend selection**
  - `NEXUS_RUNTIME_BACKEND=krun`
  - Keep hidden feature gate during rollout: `NEXUS_EXPERIMENTAL_KRUN=1`

- **Domain runtime driver contract (existing)**
  - Implement fully for krun:
    - `Create`
    - `Start`
    - `Stop`
    - `Pause`
    - `Resume`
    - `Snapshot`
    - `Restore`
    - `Fork`
    - `Destroy`

- **Operational interfaces**
  - Node capability includes `runtime.krun` when active.
  - `workspace.create --backend krun` remains supported.
  - PTY and spotlight RPC interfaces remain unchanged externally.

## Error Handling

- Classify errors into:
  - `configuration_error` (missing libkrun, incompatible host features)
  - `runtime_transition_error` (invalid state transition)
  - `snapshot_error` (capture/restore incompatibility, corruption)
  - `fork_error` (lineage conflict, clone failure)
  - `guest_channel_error` (PTY/spotlight guest dial failures)

- Retry strategy:
  - idempotent retries for start/stop/destroy on transient transport errors
  - no automatic retry for snapshot/restore integrity failures

- User-facing behavior:
  - actionable remediation messages (`install libkrun runtime`, `rebuild snapshot`, `downgrade to cold fork`)
  - preserve original stderr in structured logs for diagnosis

## Known Limitations

- Initial release keeps `NEXUS_EXPERIMENTAL_KRUN` gated until parity test matrix passes.
- Runtime fork may be unavailable on specific host/kernel/libkrun combinations; cold fork remains fallback.
- Cross-version snapshot restore is blocked unless manifest compatibility checks pass.

## Task Graph

Define implementation tasks with strict dependency boundaries so parallel work does not conflict.

### Task List

| ID | Task | Depends On | Owner / Agent | Files Touched | Est. |
|----|------|-----------|---------------|---------------|------|
| T1 | Add `libkrun` bridge package with lifecycle APIs and host capability probes | — | coder | `packages/nexus/internal/infra/runtime/krun/*` | 2d |
| T2 | Replace smolvm orchestration driver internals with direct `libkrun` control path | T1 | coder | `packages/nexus/internal/infra/runtime/krun/driver.go`, `packages/nexus/internal/infra/runtime/krun/adapter.go` | 2d |
| T3 | Implement durable snapshot manifest + restore validation and persistence wiring | T2 | coder | `packages/nexus/internal/infra/runtime/krun/*`, `packages/nexus/internal/app/workspace/*` | 1.5d |
| T4 | Implement fork modes (cold/runtime) and lineage metadata integration | T3 | coder | `packages/nexus/internal/infra/runtime/krun/*`, `packages/nexus/internal/app/workspace/fork.go` | 1.5d |
| T5 | Add backend-agnostic guest control channel and krun PTY/spotlight wiring | T2 | coder | `packages/nexus/internal/rpc/pty/handler.go`, `packages/nexus/internal/app/spotlight/service.go`, `packages/nexus/internal/daemon/daemon.go` | 2d |
| T6 | Add integration/e2e parity suite for lifecycle, snapshot, restore, fork, PTY, spotlight | T3,T4,T5 | coder | `packages/nexus/internal/*_test.go`, `packages/nexus/cmd/nexus/main_test.go` | 2d |
| T7 | Rollout hardening: docs, capability gating, observability, and migration notes | T6 | coder | `docs/reference/*`, `packages/nexus/cmd/nexus/*`, `packages/nexus/internal/infra/config/*` | 1d |

### Dependency Graph

```text
T1 ──▶ T2 ──▶ T3 ──▶ T4 ──▶ T6 ──▶ T7
               └────────────▶ T5 ──▶ T6
```

### Parallelization Rules

- Tasks with **no dependency edges** can run simultaneously via `background_task`.
- Tasks touching **disjoint file sets** can run simultaneously.
- Tasks touching **overlapping files** MUST run sequentially.
- `T4` and `T5` may run in parallel after `T2`, but merge order must respect shared runtime interfaces.
- `T6` starts only after `T3`, `T4`, and `T5` are complete.

## Steer Log

### 2026-04-21 — Promote from smolvm prototype to full libkrun parity plan

- **Trigger**: User required full support for fork, snapshot, and restore with no major feature gaps.
- **From**: Experimental smolvm orchestration proving backend wiring and basic lifecycle only.
- **To**: Full implementation plan with direct `libkrun` integration and production parity requirements.
- **Rationale**: The prototype does not satisfy required advanced runtime semantics; direct libkrun control is required for correctness and feature completeness.
- **Affected sections**: Overview, Architecture, Data Model, API / Interface, Task Graph.

### 2026-04-21 — Require integration coverage and defer embedding

- **Trigger**: User asked for stronger coverage: "ensure this driver is well-covered with integration test not just against mock driver. And see if need to embed?"
- **From**: Primary validation through unit tests with mocked controller behavior.
- **To**: Add integration tests that execute the real krun driver + command-controller path using a fake smolvm binary harness; keep runtime artifacts external (no binary embedding) for now.
- **Rationale**: Integration-level confidence is required before parity rollout. Embedding runtime artifacts now would increase binary size and release complexity before interface stability; external runtime path remains configurable via env and package manager install.
- **Affected sections**: Task Graph, Error Handling, Known Limitations.
