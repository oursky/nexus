# Nexus System Specification — Chapter 07: Cross-Cutting Invariants

> **Status**: Normative

---

## Uniqueness — `INV-001`–`INV-005`

**`INV-001`** — Workspace IDs MUST be globally unique and MUST NOT be reused, even after a
workspace is removed.

**`INV-002`** — Workspace names (`workspaceName`) MUST be unique among workspaces in any state
other than `removed` at any instant in time.

**`INV-003`** — Project IDs MUST be globally unique and MUST NOT be reused.

**`INV-004`** — Project names MUST be unique at all times (projects are hard-deleted, not soft-
deleted).

**`INV-005`** — Forward IDs MUST be unique at all times.

---

## Read-after-write consistency — `INV-006`–`INV-011`

**`INV-006`** — `workspace.list` MUST NOT return more than one entry per workspace ID.

**`INV-007`** — After `workspace.start` returns successfully, `workspace.info` MUST return state
`running`.

**`INV-008`** — After `workspace.stop` returns successfully, `workspace.info` MUST return state
`stopped`.

**`INV-009`** — After `workspace.remove` returns successfully, `workspace.list` MUST NOT include
the workspace, and `workspace.info` MUST return `ERR-011`.

**`INV-010`** — After `workspace.fork` returns successfully, both the source workspace and the
forked workspace MUST be visible in `workspace.list`.

**`INV-011`** — After `workspace.create` returns successfully, `workspace.info` MUST return the
created workspace in state `created`.

---

## Idempotency and error consistency — `INV-012`–`INV-018`

**`INV-012`** — Calling `workspace.start` on a `running` workspace MUST return `ERR-012` (invalid
state), NOT succeed silently.

**`INV-013`** — Calling `workspace.stop` on a `stopped` workspace MUST return `ERR-012` (invalid
state), NOT succeed silently.

**`INV-014`** — Calling `workspace.remove` on an already-`removed` workspace MUST return `ERR-011`
(not found), since removed workspaces are excluded from lookup.

**`INV-015`** — Calling `project.remove` on an unknown project MUST return `ERR-042`.

**`INV-016`** — `spotlight.stop` on a workspace with no active forwards MUST return success
(`{"closed": true}`). It is idempotent.

**`INV-017`** — Calling `workspace.ports.remove` with an unknown `forwardId` MUST return `ERR-052`.

---

## Concurrency — `INV-018`–`INV-021`

**`INV-018`** — Concurrent `workspace.create` calls with the same `workspaceName` MUST result in
at most one success; all others MUST return `ERR-010`.

**`INV-019`** — The daemon MUST serialize state transitions for a single workspace. Concurrent
`workspace.start` calls for the same workspace MUST NOT leave the workspace in an inconsistent
state.

**`INV-020`** — Multiple concurrent `workspace.list` calls MUST return results consistent with the
database state at their respective execution times (snapshot isolation).

**`INV-021`** — Concurrent `workspace.fork` on the same source workspace is implementation-defined;
the spec does not guarantee ordering, but MUST NOT corrupt either workspace's state.

---

## Lifecycle cleanup — `INV-022`–`INV-027`

**`INV-022`** — `spotlight.stop` MUST close all active port-forward listeners for the workspace
before returning.

**`INV-023`** — On daemon graceful shutdown (SIGTERM), all active spotlight listeners MUST be
closed.

**`INV-024`** — On daemon graceful shutdown, the Unix socket file MUST be removed.

**`INV-025`** — PTY sessions MUST NOT persist across daemon restarts. A reconnecting client will
find `pty.list` empty.

**`INV-026`** — When a PTY session's underlying process exits (for any reason, including crash),
a `pty.exit` notification MUST be sent with the actual exit code (or -1 if the exit code cannot
be determined).

**`INV-027`** — A workspace in state `removed` MUST NOT have any active PTY sessions or spotlight
forwards associated with it.

---

## VM backend equivalence

VM backend invariants and formal verification obligations are normative in:

- `docs/spec/09-vm-backend-formal-verification.md`
