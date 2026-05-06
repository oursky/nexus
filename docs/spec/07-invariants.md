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

## VM Backend — `VM-001`–`VM-017`

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

**`VM-009`** — A portable workspace artifact format (`.nxbundle`) MUST declare host compatibility
(`os/arch` and virtualization capability) and guest platform metadata. Import/run MUST fail fast
with a deterministic compatibility error when host requirements are not met.

**`VM-010`** — A standalone runner generated from a workspace export MUST support macOS execution
when all of the following hold: matching host architecture, Hypervisor.framework availability, and
runner binary code-signing/entitlements required by the virtualization path.

**`VM-011`** — Workspace portability semantics are split by design:
1. fork/restore snapshots are lineage-local and daemon-internal
2. `.nxbundle` artifacts are distributable and importable across hosts that satisfy compatibility
   constraints

**`VM-012`** — Portable export/import and standalone execution MUST preserve Nexusfile workspace
intent semantics (`workspace.bake`, `workspace.init`, `workspace.up`, `workspace.down`) with no
implicit remapping to deploy-only service intent fields.

**`VM-013` (Lowerdir Immutability)** — The baked base lowerdir (`workspace-base.ext4`) and the
virtiofs host project root lowerdir MUST be mounted read-only inside the guest. Guest reads of
unmodified files MUST be served directly from these lowerdirs. Host edits to files that have not
been modified in the guest MUST become visible inside the guest without restart or remount.

**`VM-014` (Copy-Up Isolation)** — For workspaces using hybrid-overlay mode, guest writes to any path under `/workspace` MUST trigger
overlayfs copy-up into the writable upperdir (`workspace-upper`). After copy-up, the guest MUST
read back its own modified version (shadowing the lowerdir original). The host MUST NOT observe
guest writes; there is no guest→host writeback. This invariant applies only to fork/bundle/restore
workspaces; regular workspaces use virtiofs-direct mode (see VM-017).

**`VM-015` (Fork Snapshot Semantics)** — `CheckpointFork` MUST copy only the parent's upperdir
(`workspace.ext4`) and `docker-data.ext4` into the child's snapshot. The baked base lowerdir
(`workspace-base.ext4`) MUST NOT be copied; child and parent share the same read-only base image.
Fork time and space overhead MUST be O(1) relative to mutation size (upperdir size), not project
size (lowerdir size).

**`VM-016` (Empty Upperdir Boot)** — For workspaces using hybrid-overlay mode, a newly-created workspace with no prior mutations MUST boot
with an empty upperdir. The guest MUST still see the full project tree via the baked base lowerdir.
The first guest write to any file MUST succeed via copy-up. This invariant applies only to
fork/bundle/restore workspaces; regular workspaces use virtiofs-direct mode (see VM-017).

**`VM-017` (Virtiofs-Direct Guest↔Host Reflection)** — For regular workspaces using virtiofs-direct
mode, `/workspace` MUST be a writable virtiofs mount backed by the host project directory. Guest
writes MUST be immediately reflected on the host filesystem (no upperdir isolation). Host writes to
unmodified files MUST be visible inside the guest without restart or remount. No overlayfs is used;
there is no copy-up or snapshot isolation.

**`VM-018` (Fork Workspace Tooling Availability)** — A forked workspace running in hybrid-overlay
mode MUST have docker, node, make, and git available and executable inside the guest at workspace
start.

**`VM-019` (Fork Workspace Mount Correctness)** — In hybrid-overlay mode, `/workspace` MUST be
correctly mounted and accessible inside the forked workspace guest. File reads and writes under
`/workspace` MUST succeed without errors.

**`VM-020` (Docker Compose Multi-Service)** — A Docker Compose stack with multiple services (e.g.
nginx + sidecar) started inside a VM workspace MUST: bring all services up, serve HTTP responses on
the expected port, and tear down cleanly without leaving orphaned containers.

**`VM-021` (Host Config Drive)** — The host config drive MUST be mounted at `/run/nexus-host/`
inside the VM. `/root/.ssh/` MUST be present and accessible. Environment variable injection from
the config drive MUST be active in guest shell sessions.

**`VM-022b` (SSH Agent Proxy Lifecycle Robustness)** — The SSH agent proxy socket MUST be
re-created after a workspace stop/start cycle. After restart, `/tmp/ssh-agent.sock` MUST exist as
a socket, `SSH_AUTH_SOCK` MUST point to it, and the proxy MUST respond without connection errors.

**`VM-023` (NX Bundle Workspace Tooling)** — A workspace created via NX bundle export→import→start
cycle MUST have docker, node, make, and git available inside the guest. `/workspace` MUST be
accessible with correct content.

**`VM-024` (Spotlight Survives Stop/Restart)** — A spotlight forward MUST survive a workspace
stop/restart cycle. After restart, the spotlight network path MUST be re-established and HTTP
traffic MUST flow through the forward without manual intervention.

**`VM-024b` (Fork Workspace Spotlight Independence)** — Parent and child (forked) workspaces MUST
each have independent spotlight servers. Both HTTP servers listening on the same port (one per
workspace) MUST be reachable independently and MUST NOT interfere with each other.

**`VM-025` (SSH Agent Proxy Socket Exists)** — The SSH agent proxy socket MUST exist at
`/tmp/ssh-agent.sock` inside the guest. The `SSH_AUTH_SOCK` environment variable MUST be set to
this path in guest shell sessions.

**`VM-025b` (SSH Agent Proxy Liveness)** — The SSH agent proxy socket MUST be live. Running
`ssh-add -l` inside the guest MUST respond without connection errors (exit code may reflect
empty agent, but MUST NOT fail with a connection-refused or socket-not-found error).

**`VM-026` (Fork Workspace SSH Agent Independence)** — Each forked workspace MUST receive its own
independent SSH agent proxy socket. A forked workspace's SSH agent MUST NOT share state with the
parent workspace's SSH agent.

---

## macOS App — `MACAPP-001`–`MACAPP-004`

**`MACAPP-001`** — Daemon compatibility status shown by the app MUST be derived from `/version`
protocol compatibility (`DaemonInfo.requiredProtocol`) with explicit dev-build exception
(`0.0.0-dev` is treated as compatible).

**`MACAPP-002`** — Outdated daemon protocol versions MUST surface as a distinct app state, not folded
into generic offline/disconnected states.

**`MACAPP-003`** — Remote auto-provisioning behavior from the app MUST be explicitly gated. When
provisioning is disabled, the app MUST fail with actionable guidance rather than attempting partial
host mutation.

**`MACAPP-004`** — Packaged app resources that include helper CLIs MUST preserve executable
permissions and app signing integrity requirements required for macOS launch.
