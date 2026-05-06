# Formal Verification Matrix

This document defines mandatory proof obligations for the Nexus system. All
obligations are required for merge unless explicitly waived.

Generated coverage map:

- `packages/nexus/test/e2e/coverage/coverage-map.md`

Regenerate and validate:

```bash
cd packages/nexus
go run ./test/e2e/coverage --check
```

The generated map classifies each formal-proof ID as:

- `covered` — one or more tests include matching `Spec:` annotations.
- `waived` — currently unautomated, but explicitly tracked with reason.
- `missing` — uncovered and not waived (check fails).

---

## VM Backend Proof Obligations — `VM-PROOF-001`–`VM-PROOF-014`

**`VM-PROOF-001 (Static)`** — Code review evidence must show PTY shell execution path uses backend
guest-exec mechanism for VM backends (no direct host `exec` for `/workspace` sessions).

**`VM-PROOF-002 (Integration)`** — Automated integration tests must verify backend dispatch by
workspace backend label for lifecycle and PTY/spotlight paths.

**`VM-PROOF-003 (Restart)`** — Restart test must prove shell open and `workspace exec` still succeed
for existing VM workspaces after daemon restart.

**`VM-PROOF-004 (User Journey)`** — End-user flow must pass for each VM backend:
create -> start -> open shell -> fork -> restore -> open shell.

**`VM-PROOF-005 (Path/Identity)`** — Captured terminal evidence must show VM shell opens in
`/workspace` and does not leak host identity/path.

**`VM-PROOF-006 (Tooling)`** — Captured evidence must show workspace-local tools exist and run:

- `/workspace/.nexus/tools/bin/codex --version`
- `/workspace/.nexus/tools/bin/opencode --version`

**`VM-PROOF-007 (Compatibility Gate, E2E)`** — Export/import user journey MUST prove compatibility
gating with deterministic outcomes:
- compatible host tuple imports/runs successfully
- incompatible host tuple fails before guest boot with a stable machine-readable reason

**`VM-PROOF-008 (macOS Runner, E2E)`** — On macOS supported architecture, standalone runner flow
(`start`/`exec`/`stop`) MUST pass and preserve `/workspace` semantics. Evidence MUST include explicit
host capability detection and Hypervisor.framework entitlement checks.

**`VM-PROOF-009 (Semantic Separation, E2E)`** — Tests MUST demonstrate that daemon snapshot/fork
lineage and portable `.nxbundle` distribution are distinct mechanisms (i.e., artifact import does not
depend on snapshot lineage IDs from source daemon state).

**`VM-PROOF-010 (Intent Preservation, E2E)`** — Tests MUST demonstrate standalone/exported behavior
preserves Nexusfile workspace intent contract:
- `workspace.up`/`workspace.down` drive local runner lifecycle behavior
- `workspace.init` remains one-time per imported runtime instance
- deploy-only `services[].start` is not auto-executed as local-start fallback

**`VM-PROOF-011 (OverlayFS Lowerdir Immutability, E2E)`** — Tests MUST demonstrate that host file
edits to unmodified files become visible inside the running guest VM without restart. Evidence
MUST include host-side file modification followed by guest-side `cat` showing the new content.

**`VM-PROOF-012 (OverlayFS Copy-Up Isolation, E2E)`** — Tests MUST demonstrate that guest writes
do not propagate back to the host. Evidence MUST include guest-side file write followed by
host-side read showing the original content unchanged.

**`VM-PROOF-013 (Fork Snapshot Semantics, E2E)`** — Tests MUST demonstrate that a forked workspace
inherits the parent's mutated state (files written in the parent) but subsequent writes in the
child do not affect the parent. Evidence MUST include: (a) parent writes a file, (b) child sees
the parent's write after fork, (c) child writes the same file, (d) parent still sees its own
version.

**`VM-PROOF-014 (Docker Daemon, E2E)`** — Tests MUST demonstrate that the Docker daemon starts
inside the guest VM and can run containers. Evidence MUST include `docker info` succeeding and a
`docker run` command executing a container.

---

## macOS App Proof Obligations — `MACAPP-PROOF-001`–`MACAPP-PROOF-003`

**`MACAPP-PROOF-001 (Unit)`** — Unit tests MUST verify daemon compatibility classification logic,
including protocol threshold and dev-build compatibility exception.

**`MACAPP-PROOF-002 (Unit)`** — Unit tests MUST verify disabled provisioning returns the dedicated
error (`provisioningDisabled`) and does not proceed with upload/start flows.

**`MACAPP-PROOF-003 (Packaging)`** — Build/release verification MUST assert helper binaries are
re-signed with app entitlements and are executable in app bundle resources.

---

## Evidence Sources

- VM backend: `packages/nexus/test/e2e/**/*_test.go`
- macOS app: `packages/nexus-swift/Tests/NexusAppTests/`
- Packaging/signing: `packages/nexus-swift/project.yml`
