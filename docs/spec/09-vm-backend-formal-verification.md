# Nexus System Specification — Chapter 09: VM Backend Equivalence and Formal Verification

> **Status**: Normative

---

## Purpose

This chapter defines mandatory equivalence rules for VM backends and mandatory proof obligations
before a backend change can be accepted.

---

## Equivalence Invariants — `VM-001`–`VM-008`

**`VM-001`** — For VM backends, interactive shell sessions MUST execute inside the guest VM, not on
the daemon host.

**`VM-002`** — For VM backends, default working directory exposed to clients MUST be `/workspace`.

**`VM-003`** — Any client request for `/workspace` or `/workspace/*` MUST resolve to the guest
workspace filesystem, not host absolute paths.

**`VM-004`** — The shell prompt/user identity for VM backends MUST reflect guest identity (e.g.
`root@...`) and MUST NOT expose daemon-host identity (e.g. `newman@engine-03`).

**`VM-005`** — Runtime dispatch MUST be selected by workspace backend metadata (`workspace.backend`)
for lifecycle (`create/start/stop/restore/fork/destroy`) and PTY/spotlight dial paths.

**`VM-006`** — VM backend shell open MUST NOT fail because of stale in-memory host-root maps after
daemon restart; required runtime state must be recoverable from persisted workspace metadata.

**`VM-007`** — Workspace-local tooling bootstrap path MUST be backend-agnostic for VM backends:
`/workspace/.nexus/tools/bin` MUST be the first-class tool path used by PTY sessions.

**`VM-008`** — Node/npm and required agent CLIs (`codex`, `opencode`) MUST be installable and
invokable via workspace-local path, independent of host mise shim PATH.

---

## Formal Verification Obligations — `VM-PROOF-001`–`VM-PROOF-006`

All obligations are required for merge.

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

---

## Acceptance Checklist

- All `VM-00x` invariants are satisfied.
- All `VM-PROOF-00x` obligations have attached evidence in test logs.
- No unresolved host-path or host-identity leakage in VM shell UX.
