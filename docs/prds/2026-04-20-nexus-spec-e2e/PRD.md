---

## type: master

feature_area: nexus-spec-e2e
date: 2026-04-20
status: draft
child_prds: [2026-04-20-e2e-behavioral-verification]

# Nexus System Specification & E2E Verification

## Overview

The Nexus system currently lacks a formal, machine-verifiable specification. E2e tests were written
ad-hoc alongside feature development — they are fragile (string-parsing outputs, mismatched CLI
contracts, duplicate RPC handler registration with divergent wire shapes), incomplete (no coverage
of state machine transitions, error taxonomy, protocol invariants, or daemon lifecycle), and
structurally disconnected from any authoritative description of system behavior.

This PRD defines two tightly coupled deliverables:

**Deliverable 1 — The Nexus System Specification** (`docs/spec/`): A formal, normative document
describing every concept, state machine, RPC wire contract, CLI contract, error taxonomy, daemon
lifecycle, and behavioral invariant. Written in present tense using RFC-2119 keywords (MUST, MUST
NOT, SHOULD, MAY). Every normative statement is tagged with a unique clause ID (e.g. `WS-003`,
`ERR-015`, `DAEMON-002`) so tests can cite precisely what they verify.

**Deliverable 2 — Specification-Driven E2E Suite**: The existing `test/e2e/` tests are annotated
with `// spec:` markers, and gaps (uncovered MUST/MUST NOT clauses) are filled with new tests.
A `make check-spec-coverage` CI gate fails if any MUST or MUST NOT clause in the spec has no
test coverage — making the spec a living, enforced contract rather than static documentation.

---

## Architecture

### Specification file layout (`docs/spec/`)

```
docs/spec/
├── 00-overview.md
├── 01-concepts.md
├── 02-state-machines.md
├── 03-rpc-protocol.md
├── 04-rpc-workspace.md
├── 04-rpc-project.md
├── 04-rpc-spotlight.md
├── 04-rpc-pty.md
├── 04-rpc-auth.md
├── 04-rpc-daemon.md
├── 05-cli-commands.md
├── 06-error-taxonomy.md
├── 07-invariants.md
└── 08-daemon-lifecycle.md
```

`04-rpc-methods.md` is intentionally split into per-domain files. The single-file alternative
would be 300+ lines and would obscure the handler ownership boundaries that matter for
understanding the spotlight overwrite issue (see § RPC contract issues below).

The generated coverage map lives at `test/e2e/coverage/coverage-map.md` and is gitignored.
It is not part of the normative spec.

### Clause ID scheme

Every normative statement gets a unique ID following this scheme:


| Prefix       | Domain                              | Example      |
| ------------ | ----------------------------------- | ------------ |
| `WS-NNN`     | Workspace domain                    | `WS-001`     |
| `PRJ-NNN`    | Project domain                      | `PRJ-001`    |
| `SPOT-NNN`   | Spotlight / port forwarding         | `SPOT-001`   |
| `PTY-NNN`    | PTY sessions                        | `PTY-001`    |
| `AUTH-NNN`   | Auth relay                          | `AUTH-001`   |
| `RPC-NNN`    | Protocol (transport, framing)       | `RPC-001`    |
| `CLI-NNN`    | CLI commands                        | `CLI-001`    |
| `ERR-NNN`    | Error taxonomy (flat, cross-domain) | `ERR-001`    |
| `DAEMON-NNN` | Daemon lifecycle, startup, shutdown | `DAEMON-001` |
| `INV-NNN`    | Cross-cutting invariants            | `INV-001`    |


Rules:

- Always three digits: `WS-001`, not `WS-01` or `WS-1`
- Error codes belong only in `ERR-NNN`. Domain chapters cite `ERR-NNN` — no compound keys like
`WS-ERR-001`
- IDs are assigned sequentially within each prefix and never reused once assigned

### E2e test structure

The existing test files are annotated in place. The `spec/` subdirectory is reserved only for
new tests covering clauses with zero existing coverage. This approach ensures the coverage map
starts at non-zero on day one (capturing existing test coverage) rather than starting empty.

```
test/e2e/
├── harness/                 # Existing harness (refactored, not replaced)
├── workspace/               # Existing — annotate with // spec: markers
├── project/                 # Existing — annotate
├── spotlight/               # Existing — annotate
├── pty/                     # Existing — annotate
├── auth/                    # Existing — annotate
├── daemon/                  # Existing — annotate
├── cli/                     # Existing — annotate + fix contract mismatches
├── spec/                    # NEW — tests for clauses with no existing coverage
│   ├── workspace_state_test.go    # WS-010–WS-019 state transitions
│   ├── protocol_test.go           # RPC-* framing and transport
│   ├── error_taxonomy_test.go     # ERR-* shape and kind strings
│   ├── daemon_lifecycle_test.go   # DAEMON-* startup/shutdown/signal
│   └── invariants_test.go         # INV-* cross-cutting
└── coverage/
    ├── generate.go          # Regex scanner: finds // spec: markers, cross-refs spec chapters
    └── coverage-map.md      # Generated, gitignored
```

Annotation format (every test function, including existing ones after annotation pass):

```go
// spec: WS-003, WS-011, WS-012
func TestWorkspaceLifecycle_StartStop(t *testing.T) { … }
```

The scanner uses:

```
regexp.MustCompile(`//\s*spec:\s*(.+)`)
```

CI gate (`make check-spec-coverage`): exits non-zero if any MUST or MUST NOT clause from
`docs/spec/**/*.md` has no entry in the coverage map.

---

## Spec chapter contents (section-level)

This section defines precisely what each spec file must contain, at the section level,
with the clause ranges allocated to each section.

---

### `00-overview.md` — System overview

Clause range: `DAEMON-001`–`DAEMON-010` (deployment topology)

**Sections:**

**§ System purpose** — One paragraph. Nexus is a remote workspace daemon that manages isolated
development environments bound to git repositories. It exposes a JSON-RPC 2.0 interface over
Unix socket and (optionally) WebSocket. A CLI client communicates with the daemon, either
directly over Unix socket or via an SSH tunnel.

**§ Component topology** — Diagram. Components:

- `nexus daemon`: long-running process on a Linux host
- `nexus` CLI: client on the developer's machine (macOS or Linux)
- SSH tunnel: established by CLI when `NEXUS_E2E_DAEMON_WEBSOCKET` is not set
- Runtime backends: `process` (sandbox) and `firecracker` (VM)

**§ Deployment variants** — `DAEMON-001`–`DAEMON-005`

- `DAEMON-001`: Single-host mode: daemon and CLI on the same machine, Unix socket only
- `DAEMON-002`: Remote mode: CLI on macOS, daemon on Linux, SSH tunnel to WebSocket endpoint
- `DAEMON-003`: CI mode (`NEXUS_E2E_DAEMON_WEBSOCKET`): direct WebSocket, pre-shared token
- `DAEMON-004`: Daemon socket path resolution: `$NEXUS_DAEMON_SOCKET` → `$XDG_STATE_HOME/nexus/nexus.sock` → `$HOME/.local/state/nexus/nexus.sock`
- `DAEMON-005`: Daemon DB path resolution: `$NEXUS_DAEMON_DB` → `$XDG_STATE_HOME/nexus/nexus.db` → `$HOME/.local/state/nexus/nexus.db`

---

### `01-concepts.md` — Domain concepts

Clause range: `WS-001`–`WS-009`, `PRJ-001`–`PRJ-009`, `SPOT-001`–`SPOT-009`, `PTY-001`–`PTY-009`, `AUTH-001`–`AUTH-009`

**Sections:**

**§ Workspace** — `WS-001`–`WS-009`

- `WS-001`: A workspace is the primary runtime unit. It binds a git repository at a specific ref
to a runtime backend instance.
- `WS-002`: Every workspace has a globally unique string ID (UUID format).
- `WS-003`: A workspace's `repo` field is a URL (or local path). `ref` is a git ref (branch, tag,
SHA). Both are immutable after creation.
- `WS-004`: `workspaceName` is a human-readable label. MUST be unique within the daemon instance
at any point where the workspace is not in state `removed`.
- `WS-005`: A workspace MUST have exactly one `state` at all times (see `02-state-machines.md`).
- `WS-006`: A workspace MAY be associated with a project via `projectId`. If set, the project MUST
exist.
- `WS-007`: `parentWorkspaceId` and `lineageRootId` are set only for forked workspaces. For the
lineage root, `lineageRootId == id`.
- `WS-008`: `backend` specifies the runtime backend (`process` or `firecracker`). If omitted at
create time, the daemon selects the default based on its configuration.
- `WS-009`: `policy` controls workspace automation: `autoStop` (bool), `autoStopDelay` (duration
string), `isolationLevel` (string), `maxLifetimeSec` (int). Validation rules are normative in
the `workspace.create` method clause.

**§ Project** — `PRJ-001`–`PRJ-009`

- `PRJ-001`: A project represents a tracked repository registration. It is an optional organizing
concept; workspaces may exist without a project.
- `PRJ-002`: Every project has a globally unique string ID (UUID format).
- `PRJ-003`: `name` is a human-readable label. MUST be unique within the daemon instance.
- `PRJ-004`: `repoUrl` is the canonical URL for the repository. No format validation is enforced
beyond non-empty string (the daemon does not resolve or clone the URL itself).
- `PRJ-005`: `rootPath` is an optional local filesystem path on the daemon host.
- `PRJ-006`: `config.defaultBackend` and `config.defaultRef` are advisory defaults for workspace
creation; they are not enforced.

**§ Spotlight / Port Forwarding** — `SPOT-001`–`SPOT-009`

- `SPOT-001`: A forward (spotlight entry) exposes a port from a running workspace to the daemon
host's network.
- `SPOT-002`: Every forward has a unique ID of the form `spot-<nanoseconds>`.
- `SPOT-003`: A forward MUST be associated with an existing workspace.
- `SPOT-004`: `localPort` is the port on the daemon host. `remotePort` is the port inside the
workspace runtime. `protocol` MUST be `tcp` (default) or `udp`.
- `SPOT-005`: In process backend mode, the daemon binds `127.0.0.1:<localPort>` and proxies
connections to `127.0.0.1:<remotePort>` inside the workspace.
- `SPOT-006`: In Firecracker mode with an endpoint resolver, the daemon resolves a guest
endpoint directly and sets `targetHost`; no local listener is bound for that path.
- `SPOT-007`: `state` is one of: `active`, `closed`.
- `SPOT-008`: A forward MUST NOT be created if another active forward with the same `localPort`
already exists for the same daemon instance.
- `SPOT-009`: Closing a forward MUST remove any associated local listener.

**§ PTY Sessions** — `PTY-001`–`PTY-009`

- `PTY-001`: A PTY session is an interactive terminal session attached to a running workspace.
- `PTY-002`: Every PTY session has a unique string ID.
- `PTY-003`: A PTY session MUST only be created for a workspace in state `running`.
- `PTY-004`: PTY sessions are in-memory only; they do not survive daemon restart.
- `PTY-005`: `cols` and `rows` default to 80 and 24 respectively if not specified.
- `PTY-006`: Data flows bidirectionally: client writes via `pty.write` (raw string); daemon pushes
data via `pty.data` server-push notifications with payload `{sessionId, data: string}` (raw
string, NOT base64). On WebSocket connections this is a bare notification; on mux connections it
is delivered via the subscription mechanism.
- `PTY-007`: When a PTY session exits, the daemon MUST push a `pty.exit` notification with
payload `{sessionId, exitCode: int}` on WebSocket connections.
- `PTY-008`: `pty.resize` MUST apply new dimensions to the underlying PTY immediately.
- `PTY-009`: `pty.close` MUST terminate the session and remove it from the registry.

**§ Auth Relay** — `AUTH-001`–`AUTH-009`

- `AUTH-001`: The auth relay issues short-lived tokens tied to a workspace's auth bindings.
- `AUTH-002`: `authrelay.mint` accepts `workspaceId` and optional `duration` (duration string,
default 1h). Returns `{token, expiresAt}`.
- `AUTH-003`: `authrelay.revoke` accepts `token`. Idempotent: revoking an unknown token returns
`ERR-030`.
- `AUTH-004`: Tokens are bearer tokens passed in `Authorization: Bearer <token>` headers.
- `AUTH-005`: An expired token MUST be treated identically to a revoked token from the client's
perspective (both return `ERR-031`).

---

### `02-state-machines.md` — Workspace state machine

Clause range: `WS-010`–`WS-029`

**Sections:**

**§ State definitions** — `WS-010`–`WS-016`

- `WS-010`: `created` — workspace record exists, runtime not yet started
- `WS-011`: `running` — runtime is active and accepting connections
- `WS-012`: `paused` — runtime is suspended (Firecracker snapshot or SIGSTOP equivalent)
- `WS-013`: `stopped` — runtime has been shut down; workspace record persists
- `WS-014`: `restored` — workspace was restored from a snapshot; semantically equivalent to
`created` for transition purposes
- `WS-015`: `removed` — workspace record is logically deleted; MUST NOT appear in `workspace.list`
results; ID MUST NOT be reused

**§ Legal transitions** — `WS-017`–`WS-025` (one clause per edge)

```
WS-017: created   → running   (via workspace.start)
WS-018: running   → paused    (via workspace.pause — Firecracker only)
WS-019: paused    → running   (via workspace.resume — Firecracker only)
WS-020: running   → stopped   (via workspace.stop)
WS-021: stopped   → running   (via workspace.start)
WS-022: created   → removed   (via workspace.remove)
WS-023: stopped   → removed   (via workspace.remove)
WS-024: restored  → running   (via workspace.start)
WS-025: restored  → removed   (via workspace.remove)
```

**§ Illegal transitions** — `WS-026`–`WS-029`

- `WS-026`: Calling `workspace.start` on a `running` workspace MUST return `ERR-011`
- `WS-027`: Calling `workspace.stop` on a non-`running` workspace MUST return `ERR-012`
- `WS-028`: Calling `workspace.remove` on a `running` workspace MUST return `ERR-013`
- `WS-029`: Calling `workspace.pause` or `workspace.resume` with the process backend MUST return
`ERR-014`

**§ Fork lineage** — `WS-030`–`WS-033`

- `WS-030`: `workspace.fork` creates a new workspace by snapshotting the source workspace. The
source workspace MUST be in state `running`.
- `WS-031`: The forked workspace is created in state `created`.
- `WS-032`: `parentWorkspaceId` of the fork is set to the source workspace's ID.
- `WS-033`: `lineageRootId` of the fork is set to the source workspace's `lineageRootId` (or
source ID if source has no lineage root).

---

### `03-rpc-protocol.md` — Transport and framing

Clause range: `RPC-001`–`RPC-029`

**Sections:**

**§ Unix socket transport** — `RPC-001`–`RPC-006`

- `RPC-001`: The daemon MUST bind a Unix domain socket at the configured socket path before
emitting any readiness signal.
- `RPC-002`: Each connection is independent. The daemon MUST handle concurrent connections.
- `RPC-003`: Messages are newline-delimited. A complete JSON-RPC 2.0 object terminated by `\n`
constitutes one message.
- `RPC-004`: The daemon MUST close the connection if a message exceeds the scanner buffer limit
(default 64 KB) without a newline.
- `RPC-005`: The Unix socket transport does NOT require authentication. Any process with filesystem
access to the socket path can connect.
- `RPC-006`: The Unix socket MUST be removed by the daemon on clean shutdown.

**§ WebSocket / HTTP transport** — `RPC-007`–`RPC-015`

- `RPC-007`: When network mode is enabled, the daemon MUST serve HTTP on the configured address.
`/healthz` returns 200 with body `ok`. `/version` returns 200 with a JSON object
`{version, buildTime}`.
- `RPC-008`: `GET /` with `Upgrade: websocket` header upgrades to WebSocket. The daemon MUST
reject non-WebSocket GET requests to `/` with 404.
- `RPC-009`: When a token is configured, the daemon MUST reject WebSocket upgrades lacking
`Authorization: Bearer <token>` with HTTP 401.
- `RPC-010`: When network mode is enabled without a token, the daemon MUST log a warning at
startup. (Non-normative for clients; normative for daemon implementations.)
- `RPC-011`: WebSocket messages are JSON-RPC 2.0 objects, one per WebSocket text frame.
- `RPC-012`: The daemon MAY push server-initiated JSON-RPC notifications (method without `id`)
on WebSocket connections via the `Notifier`. Unix socket connections do NOT receive
push notifications.
- `RPC-013`: Push notification methods: `pty.data` (payload: `{sessionId: string, data: string}`
— raw UTF-8/binary string, NOT base64), `pty.exit` (payload: `{sessionId: string, exitCode: int}`).
- `RPC-014`: TLS validation: if `bindAddr` is not loopback (`127.0.0.1` or `::1`), TLS MUST be
set to `auto` or `required`. The daemon MUST refuse to start with `tls: none` on a non-loopback
address.
- `RPC-015`: Token length: if a token is provided with fewer than 16 characters, the daemon MUST
log a warning at startup.

**§ JSON-RPC 2.0 contract** — `RPC-016`–`RPC-024`

- `RPC-016`: Every request MUST include `jsonrpc: "2.0"`, `method: string`, `id: string|int`.
- `RPC-017`: The daemon MUST respond with `jsonrpc: "2.0"` and the matching `id` from the request.
- `RPC-018`: A successful response includes `result`. An error response includes `error` with
sub-fields `code` (int), `message` (string), `data` (object, optional).
- `RPC-019`: When `data` is present on an error, it MUST include a `kind` string that is a
machine-readable error identifier (see `06-error-taxonomy.md`).
- `RPC-020`: Unknown method returns error code `-32601`.
- `RPC-021`: Malformed JSON returns error code `-32700`.
- `RPC-022`: Params schema mismatch returns error code `-32602`.
- `RPC-023`: Application errors (workspace not found, invalid state, etc.) use error code `-32000`.
- `RPC-024`: The daemon MUST NOT return `null` for a successful response that has no meaningful
result; it MUST return `{}` or a typed empty object.

**§ Mux connections** — `RPC-025`–`RPC-029`

- `RPC-025`: The CLI uses a mux layer on top of WebSocket for PTY data streaming.
- `RPC-026`: PTY data channel and RPC channel share a single WebSocket connection via the mux.
- `RPC-027`: `pty.data` and `pty.exit` are delivered via the mux subscription mechanism,
not as bare WebSocket messages, on mux connections.
- `RPC-028`: The mux protocol is internal to the CLI and daemon; it is not part of the public
API surface.
- `RPC-029`: External integrations MUST use WebSocket + JSON-RPC 2.0 directly, not the mux
protocol.

---

### `04-rpc-workspace.md` — Workspace RPC methods

Clause range: `WS-040`–`WS-099`

Each method section defines: wire shape (exact JSON field names), response shape, pre-conditions,
post-conditions, and error clauses (citing `ERR-NNN`).

**§ `workspace.create`** — `WS-040`–`WS-044`

- `WS-040`: Request shape:
  ```json
  {
    "spec": {
      "repo": "string (required)",
      "ref": "string (required)",
      "workspaceName": "string (required)",
      "projectId": "string (optional)",
      "policy": { "autoStop": bool, "autoStopDelay": "string", "isolationLevel": "string", "maxLifetimeSec": int },
      "backend": "process|firecracker (optional)",
      "authBinding": "any (optional)",
      "configBundle": "any (optional)"
    }
  }
  ```
  Note: params are nested under `spec`. The flat-param shape is NOT accepted.
- `WS-041`: Response shape: full `Workspace` object.
- `WS-042`: Pre-conditions: `workspaceName` MUST be unique among non-`removed` workspaces
(`ERR-001`). If `projectId` is set, project MUST exist (`ERR-020`).
- `WS-043`: Post-condition: workspace is created in state `created`.
- `WS-044`: The `spec.ref` field MUST be non-empty. If the daemon cannot validate the ref at
create time (because the repo is not locally accessible), no error is returned — the error
surfaces at `workspace.start`.

**§ `workspace.list`** — `WS-045`

- `WS-045`: No params. Returns `[]Workspace`. Workspaces in state `removed` MUST NOT appear.

**§ `workspace.info`** — `WS-046`–`WS-047`

- `WS-046`: Request: `{"id": "string"}`. Returns full `Workspace` object.
- `WS-047`: If no workspace with the given ID exists (including `removed`), returns `ERR-002`.

**§ `workspace.start`** — `WS-048`–`WS-050`

- `WS-048`: Request: `{"id": "string"}`. Returns full `Workspace` object.
- `WS-049`: Pre-condition: workspace MUST be in state `created`, `stopped`, or `restored`.
Otherwise returns `ERR-011`.
- `WS-050`: Post-condition: workspace is in state `running`.

**§ `workspace.stop`** — `WS-051`–`WS-053`

- `WS-051`: Request: `{"id": "string"}`. Returns full `Workspace` object.
- `WS-052`: Pre-condition: workspace MUST be in state `running`. Otherwise returns `ERR-012`.
- `WS-053`: Post-condition: workspace is in state `stopped`.

**§ `workspace.remove`** — `WS-054`–`WS-056`

- `WS-054`: Request: `{"id": "string"}`. Returns `{}`.
- `WS-055`: Pre-condition: workspace MUST NOT be in state `running`. Otherwise returns `ERR-013`.
- `WS-056`: Post-condition: workspace is in state `removed`. It no longer appears in `workspace.list`.

**§ `workspace.fork`** — `WS-057`–`WS-062`

- `WS-057`: Request: `{"id": "string", "childName": "string (optional)", "childRef": "string (required)"}`.
Returns full `Workspace` object representing the new fork.
- `WS-058`: `childRef` is REQUIRED on the wire. Omitting it returns `ERR-022`.
- `WS-059`: Pre-condition: source workspace MUST be in state `running`. Otherwise returns `ERR-011`.
- `WS-060`: Post-condition: fork is created in state `created`, with `parentWorkspaceId` set to
the source workspace's ID, and `lineageRootId` set per `WS-033`.
- `WS-061`: If `childName` is omitted, the daemon generates a name (implementation-defined).
- `WS-062`: The CLI flag `--ref` for `nexus workspace fork` is REQUIRED. Omitting it returns
exit code 2 with a usage error.

**§ `workspace.restore`** — `WS-063`–`WS-066`

- `WS-063`: Request: `{"id": "string"}`. Returns full `Workspace` object.
Note: there is no `snapshotId` parameter — the daemon selects the most recent snapshot.
- `WS-064`: Pre-condition: workspace MUST be in state `stopped`. Otherwise returns `ERR-012`.
- `WS-065`: Post-condition: workspace is in state `restored`.
- `WS-066`: If no snapshot exists for the workspace, returns `ERR-023`.

**§ `workspace.checkout`** — DEPRECATED / TO BE REMOVED

This method exists in the code (`checkoutReq{ID, TargetRef}`) but has been identified as a design
mistake. It will be removed in a future cleanup. The spec does NOT assign normative clauses to it.
Tests MUST NOT be written to verify its behavior; it MUST NOT be called by clients. It is noted
here only to prevent its wire shape from being accidentally treated as canonical.

**§ `workspace.ready`** — `WS-070`–`WS-071`

- `WS-070`: Request: `{"id": "string"}`. Returns `{"ready": bool}`.
- `WS-071`: Pre-condition: workspace MUST exist. Otherwise returns `ERR-002`.

**§ `workspace.relations.list`** — DEPRECATED / TO BE REMOVED

This method exists in the code (`relationsReq{RepoID}`, returns `appws.Relations`) but has been
identified as providing no useful value in the current system. It will be removed. The spec does
NOT assign normative clauses to it. Tests MUST NOT verify its behavior.

**§ `workspace.ports.list`** — `WS-075`–`WS-076`
  (Effective handler: spotlight. See `04-rpc-spotlight.md` for full contract.)

- `WS-075`: Request: `{"workspaceId": "string"}`. Returns `{"forwards": []*Forward}`.
- `WS-076`: The workspace handler's registration of this method is overwritten by the spotlight
handler at daemon startup. The workspace handler's `{id}` → `{ports: []int}` shape is not
reachable and MUST NOT be used by clients.

**§ `workspace.ports.add`** — `WS-077`
  (Effective handler: spotlight. See `04-rpc-spotlight.md`.)

- `WS-077`: Request: `{"workspaceId": "string", "spec": {"localPort": int, "remotePort": int, "protocol": "tcp|udp"}}`.
Returns `{"forward": Forward}`. The workspace handler's `{id, port: int}` shape is NOT reachable.

**§ `workspace.ports.remove`** — `WS-078`
  (Effective handler: spotlight. See `04-rpc-spotlight.md`.)

- `WS-078`: Request: `{"workspaceId": "string", "forwardId": "string"}`. Returns `{"closed": bool}`.
The workspace handler's `{id, port: int}` shape is NOT reachable.

**§ `workspace.tunnels.start` / `workspace.tunnels.stop`** — DEPRECATED / TO BE REMOVED

These methods are registered by the spotlight handler with a flat wire shape different from both
the workspace handler's vestigial registration AND the canonical `workspace.ports.add`/`remove`:

- `workspace.tunnels.start`: `{workspaceId, localPort, remotePort}` (flat, no nested `spec`) →
creates one forward. Functionally identical to `spotlight.start` / `workspace.ports.add` but
with a third incompatible wire shape.
- `workspace.tunnels.stop`: `{workspaceId, forwardId}` → closes one forward. Functionally
identical to `spotlight.stop` / `workspace.ports.remove`.

These are legacy names with no distinct semantics. They will be removed. Clients MUST use
`workspace.ports.add` / `workspace.ports.remove` instead.

**§ `workspace.discover-ports`** — `WS-081`–`WS-082`

- `WS-081`: Request: `{"id": "string"}`. Note: param is `id`, NOT `workspaceId`. This method is
registered by the workspace handler (not spotlight) and uses the workspace handler's param
convention.
- `WS-082`: Returns a list of discovered port objects. Each object may include `localPort`,
`remotePort`, `service`, `protocol`, `source` fields (all optional except `localPort`/`remotePort`).

---

### `04-rpc-project.md` — Project RPC methods

Clause range: `PRJ-010`–`PRJ-039`

**§ `project.create`** — `PRJ-010`–`PRJ-014`

- `PRJ-010`: Request: `{"name": "string", "repoUrl": "string", "rootPath": "string (optional)"}`.
- `PRJ-011`: Returns full `Project` object.
- `PRJ-012`: `name` MUST be unique among projects. Duplicate returns `ERR-040`.
- `PRJ-013`: `repoUrl` MUST be non-empty. Empty value returns `ERR-041`.
- `PRJ-014`: Post-condition: project exists and is returned by `project.list`.

**§ `project.list`** — `PRJ-015`

- `PRJ-015`: No params. Returns `[]Project`. Never returns removed projects.

**§ `project.get`** — `PRJ-016`–`PRJ-017`

- `PRJ-016`: Request: `{"id": "string"}`. Returns full `Project` object.
- `PRJ-017`: If not found, returns `ERR-042`.

**§ `project.remove`** — `PRJ-018`–`PRJ-019`

- `PRJ-018`: Request: `{"id": "string"}`. Returns `{}`.
- `PRJ-019`: Post-condition: project no longer appears in `project.list`. Associated workspaces
retain their `projectId` field (it is not nulled); the workspace's `projectId` becomes a dangling
reference (implementation behavior, not specced as an error).

---

### `04-rpc-spotlight.md` — Spotlight RPC methods

Clause range: `SPOT-010`–`SPOT-039`

**§ Handler ownership and duplication note** — `SPOT-010`

- `SPOT-010`: The spotlight handler registers `spotlight.*`, `workspace.ports.*`, and
`workspace.tunnels.*`. Its registration of `workspace.ports.*` OVERWRITES the workspace
handler's prior registration of the same names. `workspace.tunnels.*` are deprecated legacy
aliases (see § `workspace.tunnels.*` above). The `spotlight.*` methods and `workspace.ports.*`
methods are functionally duplicate surfaces for the same underlying operation (`StartSpotlight` /
`CloseForward`). The canonical client-facing interface is `workspace.ports.*`. The `spotlight.*`
RPC names are used internally by the CLI and are not recommended for external clients.

**§ `spotlight.start`** — `SPOT-011`–`SPOT-014`

- `SPOT-011`: Request: `{"workspaceId": "string", "spec": {"localPort": int, "remotePort": int, "protocol": "string (optional)"}}`.
- `SPOT-012`: Creates exactly ONE port forward for the given workspace and spec. Returns
`{"forward": Forward}`. This is functionally identical to `workspace.ports.add`; the only
difference is that the CLI uses this name internally.
- `SPOT-013`: Note: the bulk "discover all ports and forward them" behavior is implemented
entirely in the CLI (`nexus spotlight start`), NOT in this RPC method. The RPC creates one
forward at a time.
- `SPOT-014`: Pre-condition: workspace MUST exist. Returns `ERR-002` if not found.

**§ `spotlight.list`** — `SPOT-015`–`SPOT-016`

- `SPOT-015`: Request: `{"workspaceId": "string"}`. `workspaceId` is REQUIRED.
- `SPOT-016`: Returns `{"forwards": []*Forward}` — all active forwards for the given workspace.
If no forwards, returns `{"forwards": []}`. This is functionally identical to
`workspace.ports.list`.

**§ `spotlight.stop`** — `SPOT-017`–`SPOT-019`

- `SPOT-017`: Request: `{"id": "string"}`. Note: param is `id` (a FORWARD ID), NOT `workspaceId`.
- `SPOT-018`: Closes the single forward identified by `id`. Returns `{"closed": true}`.
- `SPOT-019`: This method closes ONE forward. It is NOT a bulk "stop all forwards for workspace"
operation. The CLI `nexus spotlight start` stops all previously created forwards by iterating
over persisted forward IDs and calling `spotlight.stop` once per forward.

**§ `workspace.ports.add` (spotlight handler)** — `SPOT-019`–`SPOT-022`

- `SPOT-019`: Request: `{"workspaceId": "string", "spec": {"localPort": int, "remotePort": int, "protocol": "tcp|udp (optional, default tcp)"}}`.
- `SPOT-020`: Returns `{"forward": Forward}`.
- `SPOT-021`: Pre-condition: `localPort` MUST NOT be bound by another active forward. Returns
`ERR-051`.
- `SPOT-022`: Pre-condition: workspace MUST exist. Returns `ERR-002` if not.

**§ `workspace.ports.remove` (spotlight handler)** — `SPOT-023`–`SPOT-025`

- `SPOT-023`: Request: `{"workspaceId": "string", "forwardId": "string"}`.
- `SPOT-024`: Returns `{"closed": bool}`.
- `SPOT-025`: If `forwardId` is not found, returns `ERR-052`.

---

### `04-rpc-pty.md` — PTY RPC methods

Clause range: `PTY-010`–`PTY-039`

**§ `pty.create`** — `PTY-010`–`PTY-015`

- `PTY-010`: Request:
  ```json
  {
    "workspaceId": "string (required)",
    "name":        "string (optional, session label)",
    "shell":       "string (optional, path — defaults to $SHELL or /bin/sh)",
    "args":        ["string", "..."] "(optional, passed to shell; defaults to ['-l'] for interactive)",
    "workDir":     "string (optional, working directory inside workspace; defaults to /workspace)",
    "cols":        "int (optional, default 80)",
    "rows":        "int (optional, default 24)"
  }
  ```
- `PTY-011`: Returns `SessionInfo` object: `{"id": "string", "workspaceId": "string", "workDir": "string"}`.
- `PTY-012`: Pre-condition: workspace MUST exist. A non-running workspace will cause the PTY
process to fail to start; the daemon does NOT pre-check workspace state.
- `PTY-013`: If `cols`/`rows` are zero or absent, defaults are 80/24.
- `PTY-014`: When `args` is empty, the shell is launched interactively with `-l`. When `args` is
provided (e.g. `["-c", "echo hello"]`), the shell executes the given command and exits.
- `PTY-015`: Post-condition: session appears in `pty.list`.

**§ `pty.list`** — `PTY-016`–`PTY-017`

- `PTY-016`: Request: `{"workspaceId": "string"}`. `workspaceId` is REQUIRED.
- `PTY-017`: Returns `{"sessions": []SessionInfo}`. Note: key is `sessions`, not a bare array.
Only live sessions for the given workspace are returned. Terminated sessions MUST NOT appear.

**§ `pty.write`** — `PTY-018`–`PTY-019`

- `PTY-018`: Request: `{"sessionId": "string", "data": "string"}`. Note: `data` is a raw string
(UTF-8 text or control characters), NOT base64-encoded.
- `PTY-019`: If session is not found, returns `ERR-061`.

**§ `pty.resize`** — `PTY-020`–`PTY-021`

- `PTY-020`: Request: `{"sessionId": "string", "cols": int, "rows": int}`.
- `PTY-021`: If session is not found, returns `ERR-061`.

**§ `pty.rename`** — `PTY-022`–`PTY-023`

- `PTY-022`: Request: `{"sessionId": "string", "name": "string"}`.
- `PTY-023`: If session is not found, returns `ERR-061`.

**§ `pty.close`** — `PTY-024`–`PTY-026`

- `PTY-024`: Request: `{"sessionId": "string"}`.
- `PTY-025`: Terminates the PTY process, closes any underlying connection, and removes the session
from the registry.
- `PTY-026`: If session is not found, returns `ERR-061`.

---

### `04-rpc-auth.md` — Auth relay RPC methods

Clause range: `AUTH-010`–`AUTH-029`

**§ `authrelay.mint`** — `AUTH-010`–`AUTH-015`

- `AUTH-010`: Request: `{"workspaceId": "string", "duration": "duration-string (optional)"}`.
- `AUTH-011`: Returns `{"token": "string", "expiresAt": "RFC3339 timestamp"}`.
- `AUTH-012`: Default duration is 1 hour if not specified.
- `AUTH-013`: `workspaceId` MUST reference an existing workspace. Returns `ERR-002` if not found.
- `AUTH-014`: Token is opaque to the client. Its internal format is implementation-defined.
- `AUTH-015`: The issued token is valid for authentication on the same daemon instance only.

**§ `authrelay.revoke`** — `AUTH-016`–`AUTH-018`

- `AUTH-016`: Request: `{"token": "string"}`.
- `AUTH-017`: Returns `{}` on success.
- `AUTH-018`: Revoking an unknown or already-revoked token returns `ERR-030`.

---

### `04-rpc-daemon.md` — Daemon RPC methods

Clause range: `DAEMON-020`–`DAEMON-039`

**§ `node.info`** — `DAEMON-020`–`DAEMON-024`

- `DAEMON-020`: No params.
- `DAEMON-021`: Returns `{"version": "string", "buildTime": "string", "capabilities": []string}`.
- `DAEMON-022`: `capabilities` always includes `"runtime.process"`.
- `DAEMON-023`: `capabilities` includes `"runtime.firecracker"` when the daemon was started with
Firecracker enabled.
- `DAEMON-024`: This method MUST succeed even if the daemon is not yet fully initialized (it is
used as a readiness probe).

---

### `05-cli-commands.md` — CLI commands

Clause range: `CLI-001`–`CLI-099`

This chapter specifies exact flag names, required vs. optional, exit codes, and output contract
for every CLI command.

**§ Global** — `CLI-001`–`CLI-005`

- `CLI-001`: All commands connect to the daemon via `EnsureDaemon()`, which either dials
`$NEXUS_E2E_DAEMON_WEBSOCKET` with `$NEXUS_DAEMON_TOKEN`, or opens an SSH tunnel to the
profile-configured endpoint.
- `CLI-002`: If the daemon is unreachable, all commands exit with code 1 and print to stderr.
- `CLI-003`: Unknown subcommands exit with code 2 and print usage to stderr.
- `CLI-004`: All commands print human-readable output to stdout on success.
- `CLI-005`: `--json` flag (where supported) prints the result as a JSON object to stdout.

**§ `nexus daemon start`** — `CLI-010`–`CLI-016`

- `CLI-010`: Flags: `--db string`, `--socket string`, `--network string` (bind addr),
`--tls string` (none|auto|required), `--token string`, `--workdir string`.
- `CLI-011`: Starts the daemon in the foreground when `NEXUS_DAEMON_SERVE=1` is set;
otherwise forks to background (implementation-defined).
- `CLI-012`: Daemon emits a readiness signal (newline on stdout or `/healthz` 200) before
accepting connections. The CLI harness waits for `node.info` success before proceeding.
- `CLI-013`: Exit code 1 if daemon fails to start (socket bind failure, DB open failure, etc.).
- `CLI-014`: Firecracker is always enabled when the daemon starts via `nexus daemon start`.
The `--network` flag enables WebSocket transport.
- `CLI-015`: `NEXUS_DAEMON_SERVE=1` enables network transport (WebSocket) for e2e use.
- `CLI-016`: Token is required when `--network` is provided without `--no-auth` (implementation
detail; currently the token is always required with network mode).

**§ `nexus daemon stop`** — `CLI-017`

- `CLI-017`: Sends SIGTERM to the running daemon. Exits 0 on success, 1 if daemon not running.

**§ `nexus daemon token`** — `CLI-018`

- `CLI-018`: Prints the daemon token to stdout. Reads from the OS-native token store (Keychain on
macOS, SecretService on Linux, file fallback on headless Linux). Used by `nexus daemon connect`
when fetching the token from a remote host via SSH.

**§ `nexus daemon implode`** — `CLI-019`

- `CLI-019`: Removes ALL Nexus daemon state: running processes, workspace VM images, rootfs,
kernel, tap-helper, Firecracker binary, and any installed daemon files. Intended for contributors
who want to start completely from scratch. Prompts for confirmation unless `--force` is passed.
After `implode`, a bare `nexus daemon start` MUST be able to re-provision the system from scratch.

**§ `nexus workspace create`** — `CLI-020`–`CLI-028`

- `CLI-020`: Required flags: `--repo string`, `--name string`.
- `CLI-021`: Optional flags: `--ref string` (default `main`), `--backend string`, `--project string`.
- `CLI-022`: Outputs the created workspace ID and name on success.
- `CLI-023`: Exits 1 if `--repo` or `--name` is missing (exits 2 with usage if flags are omitted).
- `CLI-024`: Does NOT accept a positional `.` argument as a shorthand for the current directory.
(The `workspace_create_test.go` test calling `nexus workspace create .` is incorrect and MUST
be fixed to use `--repo` and `--name`.)
- `CLI-025`: On success, prints the workspace ID to stdout (exact format TBD in implementation).
- `CLI-026`: When the active profile has an SSH target (`profile.Host` is set) and `--repo` is a
local filesystem path (not a remote URL), the CLI MUST mirror the local path to the remote host
via `mirror.Ensure()` before calling the `workspace.create` RPC. The mirrored remote path
(e.g. `~/.local/share/nexus/mirrors/<slug>`) is passed as `spec.repo` to the daemon. The user
never needs to pre-populate the remote host with the repository.
- `CLI-027`: The base mirror session for a workspace MUST exclude `.worktrees/` via a mutagen
`--ignore` flag, so fork worktrees created inside the project root are not synced to the remote
as part of the base workspace.
- `CLI-028`: After a successful `workspace.create`, when `--repo` is a local path, the CLI MUST
record the workspace in the client-side local state store (`~/.local/share/nexus/workspaces.json`)
with `localPath = <original-mac-path>`, `gitRoot = <original-mac-path>`, `isWorktree = false`.

**§ `nexus workspace list`** — `CLI-026`

- `CLI-026`: No flags. Prints workspace ID, name, and state for each workspace.

**§ `nexus workspace start/stop/remove`** — `CLI-027`–`CLI-029`

- `CLI-027`: `nexus workspace start <id-or-name>`: positional arg required.
- `CLI-028`: `nexus workspace stop <id-or-name>`: positional arg required.
- `CLI-029`: `nexus workspace remove <id-or-name>`: positional arg required.

**§ `nexus workspace info`** — `CLI-030`–`CLI-031`

- `CLI-030`: `nexus workspace info <id-or-name>`: positional arg required.
- `CLI-031`: `--json` flag prints the full workspace JSON object.

**§ `nexus workspace shell`** — `CLI-032`–`CLI-034`

- `CLI-032`: `nexus workspace shell <id-or-name>`: positional arg required.
- `CLI-033`: Opens an interactive PTY session via `pty.create`, streams data bidirectionally.
- `CLI-034`: Exit code mirrors the shell exit code.

**§ `nexus workspace exec`** — `CLI-035`–`CLI-039`

- `CLI-035`: `nexus workspace exec <id-or-name> <command> [args...]` (registered as `Use: "exec"`,
alias `run` in code but `run` alias is a stopgap — see note below).
- `CLI-036`: Runs a command inside an EXISTING, already-running workspace via `pty.create`.
Streams stdout. Exits when the command exits.
- `CLI-037`: Optional flag: `--workdir string` (default `/workspace`).
- `CLI-038`: Exit code mirrors the command's exit code.
- `CLI-039`: The `run` alias on this command is a STOPGAP. The intended design for `nexus workspace run`
is an EPHEMERAL workspace command: create a fresh workspace → start it → exec the given command →
automatically remove the workspace when the command exits (like Daytona's `daytona run`). This
ephemeral lifecycle is NOT yet implemented. The spec marks `workspace run` as INTENDED FUTURE
BEHAVIOR. Until implemented, `run` and `exec` are identical in behavior.

**§ `nexus workspace fork`** — `CLI-039`–`CLI-046`

- `CLI-039`: `nexus workspace fork <id-or-name> --ref <ref>`.
- `CLI-040`: `--ref` is REQUIRED. Omitting it exits with code 2.
- `CLI-041`: Optional: `--name string` for the fork's workspace name.
- `CLI-042`: After a successful `workspace.fork` RPC, if the parent workspace has a client-side
state record (in `~/.local/share/nexus/workspaces.json`), the CLI MUST create a git worktree
for the fork: `git worktree add <gitRoot>/.worktrees/<childName> <childRef>`.
- `CLI-043`: The worktree MUST be placed inside `<gitRoot>/.worktrees/` — co-located with the
parent project, not in a global `~/nexus-workspaces` directory.
- `CLI-044`: The CLI MUST append `.worktrees/` to `<gitRoot>/.git/info/exclude` (if not already
present) so git ignores the directory without modifying `.gitignore`.
- `CLI-045`: When the active profile has an SSH target, the CLI MUST mirror the new worktree
to the remote host (as a new, independent mirror session) so the daemon can use it as the
fork workspace's sync source.
- `CLI-046`: The fork workspace MUST be recorded in the client-side state store with
`localPath = <worktreePath>`, `gitRoot = <parentGitRoot>`, `isWorktree = true`.

**§ `nexus workspace checkout`** — DEPRECATED / TO BE REMOVED

`nexus workspace checkout` exists in the command tree but is scheduled for removal. It MUST NOT
be used by new code or tested as a stable API.

**§ `nexus workspace restore`** — `CLI-042`–`CLI-043`

- `CLI-042`: `nexus workspace restore <id-or-name>`. No `--snapshot` flag.
- `CLI-043`: Restores from the most recent snapshot. Exits 1 if no snapshot exists.

**§ `nexus spotlight start/list/stop`** — `CLI-050`–`CLI-056`

- `CLI-050`: `nexus spotlight start <workspace-id>`: positional arg REQUIRED (workspace ID).
- `CLI-051`: Bulk orchestrator: calls `workspace.discover-ports {id: workspaceId}` to get
discovered ports, then loops calling `spotlight.start {workspaceId, spec}` RPC once per port
to create daemon-side forwards, then opens an SSH tunnel from client to daemon for each port.
Persists forward IDs in client-side state for later cleanup.
- `CLI-052`: Prints `<service> → localhost:<localPort>` for each forwarded port.
- `CLI-053`: `nexus spotlight list <workspace-id>`: positional arg REQUIRED. Calls
`spotlight.list {workspaceId: ...}` RPC.
- `CLI-054`: `nexus spotlight stop <forward-id>`: positional arg is a FORWARD ID (not a workspace
ID). Calls `spotlight.stop {id: forwardId}` RPC. Prompts for confirmation unless `--force` is
passed.
- `CLI-055`: `nexus spotlight stop --force <forward-id>`: skips confirmation prompt.
- `CLI-056`: The CLI `nexus spotlight start` also tears down previously active spotlight forwards
(from persisted client state) before creating new ones.

**§ `nexus spotlight port add/list/remove/state`** — `CLI-055`–`CLI-060`

- `CLI-055`: `nexus spotlight port add <workspace-id> --local <int> --remote <int>`.
- `CLI-056`: `--local` and `--remote` are REQUIRED for `port add`. Optional: `--protocol tcp|udp`.
- `CLI-057`: `nexus spotlight port list <workspace-id>`.
- `CLI-058`: `nexus spotlight port remove <workspace-id> --forward-id <string>`.
- `CLI-059`: `--forward-id` is REQUIRED for `port remove`.
- `CLI-060`: `nexus spotlight port state <glob>`: lists forwards matching the glob pattern.

**§ `nexus project` commands** — `CLI-070`–`CLI-079`

- `CLI-070`: `nexus project create --name <string> --repo <string>`. Optional: `--root-path`.
- `CLI-071`: `nexus project list`.
- `CLI-072`: `nexus project get <id-or-name>`.
- `CLI-073`: `nexus project remove <id-or-name>`.

---

### `06-error-taxonomy.md` — Error taxonomy

Clause range: `ERR-001`–`ERR-099`

Every RPC error response with code `-32000` MUST include `data.kind` as a machine-readable
string matching one of the following:


| Clause    | `data.kind`                       | Meaning                                 | Typically returned by                                |
| --------- | --------------------------------- | --------------------------------------- | ---------------------------------------------------- |
| `ERR-001` | `workspace.duplicate_name`        | `workspaceName` already in use          | `workspace.create`                                   |
| `ERR-002` | `workspace.not_found`             | No workspace with given ID              | most workspace methods                               |
| `ERR-011` | `workspace.invalid_state`         | Operation not valid in current state    | `workspace.start`, `workspace.fork`                  |
| `ERR-012` | `workspace.invalid_state`         | Operation requires running state        | `workspace.stop`                                     |
| `ERR-013` | `workspace.invalid_state`         | Cannot remove running workspace         | `workspace.remove`                                   |
| `ERR-014` | `workspace.backend_unsupported`   | pause/resume on process backend         | `workspace.pause`, `workspace.resume`                |
| `ERR-020` | `project.not_found`               | projectId on workspace create not found | `workspace.create`                                   |
| `ERR-021` | `workspace.invalid_spec`          | Create spec validation failed           | `workspace.create`                                   |
| `ERR-022` | `workspace.fork_missing_ref`      | `childRef` omitted on fork              | `workspace.fork`                                     |
| `ERR-023` | `workspace.no_snapshot`           | No snapshot exists for restore          | `workspace.restore`                                  |
| `ERR-030` | `auth.unknown_token`              | Revoke: token not found                 | `authrelay.revoke`                                   |
| `ERR-031` | `auth.expired`                    | Token is expired                        | bearer auth check                                    |
| `ERR-040` | `project.duplicate_name`          | Project name already in use             | `project.create`                                     |
| `ERR-041` | `project.invalid_spec`            | `repoUrl` empty or invalid              | `project.create`                                     |
| `ERR-042` | `project.not_found`               | No project with given ID                | `project.get`, `project.remove`                      |
| `ERR-050` | `spotlight.workspace_not_running` | Workspace must be running               | `spotlight.start`                                    |
| `ERR-051` | `spotlight.port_conflict`         | `localPort` already bound               | `workspace.ports.add`                                |
| `ERR-052` | `spotlight.not_found`             | `forwardId` not found                   | `workspace.ports.remove`                             |
| `ERR-060` | `pty.workspace_not_running`       | Workspace must be running               | `pty.create`                                         |
| `ERR-061` | `pty.not_found`                   | PTY session not found                   | `pty.write`, `pty.resize`, `pty.rename`, `pty.close` |


---

### `07-invariants.md` — Cross-cutting invariants

Clause range: `INV-001`–`INV-029`

**§ Uniqueness** — `INV-001`–`INV-005`

- `INV-001`: Workspace IDs MUST be globally unique and MUST NOT be reused.
- `INV-002`: Workspace names MUST be unique among non-`removed` workspaces at all times.
- `INV-003`: Project IDs MUST be globally unique and MUST NOT be reused.
- `INV-004`: Project names MUST be unique at all times.
- `INV-005`: Forward IDs MUST be unique at all times.

**§ Ordering and consistency** — `INV-006`–`INV-010`

- `INV-006`: `workspace.list` MUST return at most one workspace per ID.
- `INV-007`: After `workspace.start` returns, `workspace.info` MUST return state `running`.
- `INV-008`: After `workspace.stop` returns, `workspace.info` MUST return state `stopped`.
- `INV-009`: After `workspace.remove` returns, `workspace.list` MUST NOT include the workspace.
- `INV-010`: After `workspace.fork` returns, both the source and fork workspace MUST be visible
in `workspace.list`.

**§ Idempotency** — `INV-011`–`INV-015`

- `INV-011`: `workspace.remove` on an already-`removed` workspace returns `ERR-002`.
- `INV-012`: `workspace.start` on an already-`running` workspace returns `ERR-011`.
- `INV-013`: `workspace.stop` on an already-`stopped` workspace returns `ERR-012`.
- `INV-014`: `project.remove` on an unknown project returns `ERR-042`.
- `INV-015`: Closing a non-existent forward (`spotlight.stop {id: <unknown>}` or `workspace.ports.remove`)
returns `ERR-052` (not a silent no-op).

**§ Concurrency** — `INV-016`–`INV-020`

- `INV-016`: Concurrent `workspace.create` with the same `workspaceName` MUST result in exactly
one success and at least one `ERR-001`.
- `INV-017`: The daemon MUST serialize state transitions for a single workspace (no TOCTOU on
state checks).
- `INV-018`: Multiple concurrent calls to `workspace.list` MUST return consistent results (no
partial writes visible).

**§ Lifecycle cleanup** — `INV-021`–`INV-025`

- `INV-021`: Removing a workspace MUST close all associated spotlight forwards.
- `INV-022`: Removing a workspace MUST close all associated PTY sessions.
- `INV-023`: On daemon shutdown, all active forwards' local listeners MUST be closed.
- `INV-024`: On daemon shutdown, the Unix socket file MUST be removed.
- `INV-025`: PTY sessions MUST NOT persist across daemon restarts.

---

### `08-daemon-lifecycle.md` — Daemon lifecycle

Clause range: `DAEMON-040`–`DAEMON-079`

**§ Startup sequence** — `DAEMON-040`–`DAEMON-052`

- `DAEMON-040`: On start, the daemon opens (or creates) the SQLite database at the configured
path. Failure to open the DB is fatal (exit 1).
- `DAEMON-041`: On start, the daemon creates the Unix socket file. If a socket file already exists
at the path, the daemon MUST remove it before binding (stale socket cleanup).
- `DAEMON-042`: On start, the daemon registers all RPC handlers in this order: workspace, spotlight
(overwriting `workspace.ports.`* and `workspace.tunnels.`*), PTY, FS, node info, project, auth.
- `DAEMON-043`: After binding the socket and registering handlers, the daemon emits a readiness
signal. The signal MUST occur before the socket begins accepting connections.
(For `NEXUS_DAEMON_SERVE=1` mode, `/healthz` becoming 200 is the readiness signal.)
- `DAEMON-044`: `node.info` MUST succeed on the first call after readiness signaling.
- `DAEMON-045`: If Firecracker is enabled (`--firecracker` or config), the daemon injects the
guest agent into the rootfs at startup via `debugfs`. Failure is logged but MAY be non-fatal
depending on configuration.
- `DAEMON-046`: Network listener (WebSocket) is started after the Unix socket is ready. Token
validation (see `RPC-009`) is applied before the WebSocket connection is accepted.
- `DAEMON-047`: `NEXUS_DAEMON_SERVE=1` env var causes the daemon to also bind the HTTP/WebSocket
network listener and expose `WebSocketURL` for use by CLI e2e harness.

**§ Shutdown sequence** — `DAEMON-050`–`DAEMON-059`

- `DAEMON-050`: On SIGTERM, the daemon initiates graceful shutdown.
- `DAEMON-051`: During graceful shutdown, the daemon MUST drain in-flight RPC calls (with a
timeout, implementation-defined).
- `DAEMON-052`: During graceful shutdown, the daemon MUST close all active spotlight listeners.
- `DAEMON-053`: During graceful shutdown, the daemon MUST close all active PTY sessions.
- `DAEMON-054`: During graceful shutdown, the daemon MUST close the SQLite database.
- `DAEMON-055`: During graceful shutdown, the daemon MUST remove the Unix socket file.
- `DAEMON-056`: The daemon MUST exit with code 0 after clean shutdown.
- `DAEMON-057`: SIGKILL is not a graceful shutdown; no cleanup guarantees apply.

**§ Network security constraints** — `DAEMON-060`–`DAEMON-066`

- `DAEMON-060`: Token authentication is REQUIRED when network mode is enabled. The daemon MUST
refuse to start in network mode without a token unless explicitly bypassed (if a bypass option
exists, it MUST log a prominent warning).
- `DAEMON-061`: If the bind address is non-loopback, TLS MUST be `auto` or `required`. The daemon
MUST refuse to start if TLS is `none` on a non-loopback address.
- `DAEMON-062`: A token shorter than 16 characters MUST trigger a startup warning to stderr.
- `DAEMON-063`: The daemon MUST NOT log the token value at any log level.

**§ Token store** — `DAEMON-070`–`DAEMON-074`

- `DAEMON-070`: The daemon token MUST be stored in the OS-native secret store, never in a
plaintext file on platforms where a secret store is available.
- `DAEMON-071`: On macOS, the token MUST be stored in the macOS Keychain using the system
`security` CLI (service: `nexus`, account: `daemon-token`). No CGo or external dependencies
are required.
- `DAEMON-072`: On Linux, the token MUST first attempt SecretService (D-Bus). If SecretService
is unavailable (headless server, no D-Bus session), the daemon MUST fall back to a
0600-permission file at `~/.local/share/nexus/daemon-token`. This fallback is acceptable on
headless servers because the security model is equivalent to SSH host keys.
- `DAEMON-073`: The CLI profile token (for connecting to a remote daemon) MUST be stored in the
same OS-native store with a distinct key, and MUST NOT be written to the profile JSON file.
- `DAEMON-074`: `nexus daemon connect <host>` fetches the token from the remote host by SSHing
and running `$HOME/.local/bin/nexus daemon token` (full path, since non-interactive SSH sessions
do not source shell profiles).

**§ Firecracker runtime implementation** — `DAEMON-080`–`DAEMON-085`

- `DAEMON-080`: The kernel boot args MUST include `init=/usr/local/bin/nexus-firecracker-agent`
so the kernel launches the agent directly without relying on the rootfs's init tree. This
removes the need to patch `/sbin/init` or `/usr/sbin/init` in the rootfs image.
- `DAEMON-081`: `ensureFirecrackerGuestAgent` MUST inject only the agent binary into the rootfs
(`/usr/local/bin/nexus-firecracker-agent`). It MUST NOT write a wrapper init script or patch
any init path.
- `DAEMON-082`: All VM-image file copies (workspace snapshots, fork CoW) MUST use
`cp --reflink=auto --sparse=always` rather than `io.Copy`. This preserves sparse holes in
ext4 images and automatically uses copy-on-write on XFS/btrfs.
- `DAEMON-083`: The Firecracker API config MUST use relative paths for per-VM files
(`workspace.ext4`, `vsock.sock`) since Firecracker is launched with `cmd.Dir = workDir`.
The shared base rootfs continues to use its absolute path.
- `DAEMON-084`: Network mode MUST be enabled by default when `nexus daemon start` is run. The
`--network` flag is not required; the daemon binds the WebSocket listener automatically.

**§ Client-side local workspace state** — `DAEMON-090`–`DAEMON-094`

- `DAEMON-090`: The CLI maintains a client-side local state file at
`~/.local/share/nexus/workspaces.json` (respects `$XDG_DATA_HOME`). This file maps workspace
IDs to their Mac-local paths and is never sent to the daemon.
- `DAEMON-091`: Each record contains: `workspaceID`, `workspaceName`, `localPath` (the Mac path
the user works in), `gitRoot` (the git repository root, never a worktree), `isWorktree` (bool).
- `DAEMON-092`: For base workspaces created from a local `--repo` path: `localPath == gitRoot`,
`isWorktree == false`. The user's original directory IS their working tree; no worktree is
created.
- `DAEMON-093`: For forked workspaces: `localPath = <gitRoot>/.worktrees/<childName>`,
`gitRoot = <parent's gitRoot>`, `isWorktree = true`.
- `DAEMON-094`: The state file is written with 0600 permissions. The CLI reads it without
contacting the daemon — it is a pure client-side cache.

---

## Data Model

The data model is fully specified within the spec chapters above (§ concepts, § RPC methods).
Key wire-shape notes:

- `workspace.create` params are nested under `{"spec": {...}}`, not flat
- `workspace.fork` requires `childRef` (not optional); child name field is `childWorkspaceName`
- `workspace.restore` has no `snapshotId` param; restores from most recent snapshot
- `workspace.discover-ports` uses `{"id": ...}` not `{"workspaceId": ...}`
- `workspace.ports.*` effective shapes are spotlight handler shapes (see SPOT clauses)
- `spotlight.list` takes `{workspaceId}`; `spotlight.stop` takes `{id: forwardId}` (not workspaceId)
- `pty.data` notifications and `pty.write` data are raw strings, NOT base64
- **DEPRECATED — no normative shapes**: `workspace.checkout`, `workspace.relations.list`,
`workspace.tunnels.start`, `workspace.tunnels.stop`

---

## Error Handling

Fully specified in `06-error-taxonomy.md` (above). Key principles:

1. All RPC application errors use code `-32000`
2. All application errors include `data.kind` (machine-readable)
3. Protocol errors use standard JSON-RPC codes: `-32700`, `-32600`, `-32601`, `-32602`
4. CLI errors: exit 1 for operational errors, exit 2 for usage errors

---

## Known Limitations

### Deprecated / Scheduled for Removal (do NOT spec, do NOT test)

- `**workspace.checkout` RPC + CLI**: Identified as a design mistake. Registered in code
(`checkoutReq{ID, TargetRef}`) but carries no useful semantics in the current model. Will be
removed. No normative clauses assigned. No tests to be written.
- `**workspace.relations.list` RPC**: Registered in code (`relationsReq{RepoID}`), not useful.
Will be removed. No normative clauses assigned. No tests to be written.
- `**workspace.tunnels.start` / `workspace.tunnels.stop` RPC**: Legacy names in the spotlight
handler, flat wire shapes that duplicate `spotlight.start`/`spotlight.stop` with incompatible
param schemas. Will be removed. Clients MUST use `workspace.ports.add`/`workspace.ports.remove`.

### Intended Future Behavior (not yet implemented)

- `**nexus workspace run` (ephemeral workspace)**: The intended design is an ephemeral lifecycle:
create workspace → start it → exec given command → auto-remove on exit (like Daytona's `daytona run`).
Currently `run` is only an alias for `workspace exec`. The spec marks this as future intent;
`workspace run` MUST NOT be tested as ephemeral behavior until explicitly implemented.

### Deferred to follow-up spec chapters

- `**nexus exec` / `nexus doctor` / `nexus init`**: Logic lives in `main.go` outside standard
command pattern. `workspace exec` (via `run.go`) IS specced here. Top-level `nexus exec`,
`nexus doctor`, and `nexus init` are deferred.
- **FS RPC methods** (`fs.readFile`, `fs.writeFile`, `fs.readdir`, `fs.stat`, `fs.rm`, `fs.mkdir`,
`fs.exists`): Registered in the daemon, not CLI-exposed, not yet specced here.
- **Firecracker-specific paths**: pause/resume, vsock PTY dialer, guest endpoint resolver,
guest-agent injection. Clauses marked `[firecracker-only]` require a Firecracker environment.
E2e coverage conditional on `NEXUS_RUNTIME_BACKEND=firecracker`.
- `**nexus mutagen path`**: Exists in `commands/mutagen/mutagen.go`, not wired in `main.go`.
Not specced.

### Known code issues (tracked, not specced as intended behavior)

- **Duplicate handler registration for `workspace.ports.*`**: Spotlight handler overwrites
workspace handler's registration of `workspace.ports.*` and `workspace.tunnels.*`. The spec
documents only the effective (spotlight) contract. Code cleanup is tracked as T16.

---

## Task Graph

### Task List


| ID  | Task                                                                                                  | Depends On | Owner / Agent | Files Touched                                           | Est.  |
| --- | ----------------------------------------------------------------------------------------------------- | ---------- | ------------- | ------------------------------------------------------- | ----- |
| T0  | Ground-truth audit: verify each RPC method's actual wire shape against the spec via harness           | —          | coder         | packages/nexus/                                         | 0.5d  |
| T1  | Write `docs/spec/00-overview.md` + `01-concepts.md`                                                   | T0         | coder         | docs/spec/                                              | 0.5d  |
| T2  | Write `docs/spec/02-state-machines.md`                                                                | T0, T1     | coder         | docs/spec/                                              | 0.25d |
| T3  | Write `docs/spec/03-rpc-protocol.md`                                                                  | T0         | coder         | docs/spec/                                              | 0.5d  |
| T4  | Write `docs/spec/04-rpc-*.md` (all six domain files)                                                  | T0, T3     | coder         | docs/spec/                                              | 1d    |
| T5  | Write `docs/spec/05-cli-commands.md`                                                                  | T0, T1     | coder         | docs/spec/                                              | 0.5d  |
| T6  | Write `docs/spec/06-error-taxonomy.md` + `07-invariants.md` + `08-daemon-lifecycle.md`                | T2, T4, T5 | coder         | docs/spec/                                              | 0.5d  |
| T7  | Implement `test/e2e/coverage/generate.go` (regex scanner + CI gate)                                   | T6         | coder         | test/e2e/coverage/                                      | 0.5d  |
| T8  | Annotate existing tests with `// spec:` markers (workspace, project, spotlight, pty, auth, daemon)    | T6         | coder         | test/e2e/{workspace,project,spotlight,pty,auth,daemon}/ | 0.5d  |
| T9  | Fix existing test contract bugs: `cli/workspace_create_test.go` (requires `--repo`/`--name`, not `.`) | T5         | coder         | test/e2e/cli/                                           | 0.25d |
| T10 | Write `spec/workspace_state_test.go` (state machine transitions, all `WS-017`–`WS-029`)               | T2, T8     | coder         | test/e2e/spec/                                          | 0.5d  |
| T11 | Write `spec/protocol_test.go` (framing, healthz, auth rejection, TLS constraint, push notifications)  | T3, T8     | coder         | test/e2e/spec/                                          | 0.5d  |
| T12 | Write `spec/error_taxonomy_test.go` (verify each `ERR-NNN` returns correct `data.kind`)               | T6, T8     | coder         | test/e2e/spec/                                          | 0.5d  |
| T13 | Write `spec/daemon_lifecycle_test.go` (startup signal, stale socket, shutdown cleanup)                | T6, T8     | coder         | test/e2e/spec/                                          | 0.5d  |
| T14 | Write `spec/invariants_test.go` (concurrency, cleanup, idempotency)                                   | T6, T8     | coder         | test/e2e/spec/                                          | 0.5d  |
| T15 | Run `make check-spec-coverage`, fix any remaining gaps, finalize coverage map                         | T7–T14     | coder         | test/e2e/                                               | 0.5d  |


### Dependency Graph

```
T0 ──▶ T1 ──▶ T2 ──▶ T6
T0 ──▶ T3 ──▶ T4 ──▶ T6
T0 ──▶ T1 ──▶ T5 ──▶ T6

T6 ──▶ T7
T6 ──▶ T8
T5 ──▶ T9   (can overlap with T8)

T2 + T8 ──▶ T10
T3 + T8 ──▶ T11
T6 + T8 ──▶ T12
T6 + T8 ──▶ T13
T6 + T8 ──▶ T14

T7 + T10 + T11 + T12 + T13 + T14 ──▶ T15
```

### Critical path

T0 → T3 → T4 → T6 → T8 → T12/T13/T14 → T15 (~5.5 days)

### Parallelization rules

- T0 must complete before any spec writing (ground-truth required)
- T1, T3, T5 can run in parallel after T0
- T2 depends on T1 (borrows concept definitions)
- T4 depends on T3 (borrows protocol definitions)
- T6 depends on T2+T4+T5 (borrows from all domain chapters)
- T8 and T9 can run in parallel after T6
- T10–T14 can run in parallel with each other after T8
- T7 (coverage tool) can be written as soon as T6 is done, independently of T8–T14
- **T16 (fix duplicate RPC registration) is a code fix deferred outside this PRD — the spec
documents the effective behavior**

---

## Steer Log

### 2026-04-20 — Design mistake audit: deprecated methods and intent corrections

- **Trigger**: User review identified methods being specced as design decisions when they are
mistakes or legacy artifacts, plus `workspace run` intent misread from current code
- **From**: `workspace.checkout` and `workspace.relations.list` specced as normative active API;
`workspace.tunnels.start`/`stop` treated as working aliases; `spotlight.start` described as a
bulk port-discovery orchestrator; `spotlight.list` described as taking no params; `spotlight.stop`
described as taking `workspaceId` and bulk-closing all workspace forwards; `nexus spotlight stop`
CLI described as taking `<workspace-id>`; `nexus spotlight list` described as taking no args;
`workspace run` described as an alias for exec in existing workspace (final state); `pty.data`
notification data described as base64; `pty.write` data described as base64;
`workspace.discover-ports` param named `workspaceId`; `pty.list` response shape wrong;
`pty.create` missing `name`/`shell`/`args`/`workDir` params
- **To**:
  - `workspace.checkout` → DEPRECATED / TO BE REMOVED, no clauses, no tests
  - `workspace.relations.list` → DEPRECATED / TO BE REMOVED, no clauses, no tests
  - `workspace.tunnels.start`/`stop` → DEPRECATED / TO BE REMOVED; clarified as flat-shape
  duplicates of `spotlight.start`/`stop`, not mere overwrite victims
  - `spotlight.start` → creates ONE forward (same as `workspace.ports.add`); bulk orchestration
  is the CLI layer, not this RPC
  - `spotlight.list` → takes `{workspaceId}` (required)
  - `spotlight.stop` → takes `{id: forwardId}`, closes ONE forward
  - `nexus spotlight stop` CLI → takes `<forward-id>`, not `<workspace-id>`; has `--force` flag
  - `nexus spotlight list` CLI → takes `<workspace-id>` positional arg
  - `nexus workspace run` → documented as INTENDED FUTURE BEHAVIOR (ephemeral workspace lifecycle,
  like Daytona); current code is stopgap alias for exec; not to be tested as ephemeral until implemented
  - `pty.data` / `pty.write` data → raw string, NOT base64
  - `workspace.discover-ports` param → `id`, NOT `workspaceId`
  - `pty.list` response → `{"sessions": []SessionInfo}` key
  - `pty.create` params → added `name`, `shell`, `args`, `workDir`
- **Rationale**: Mistakes in spec contaminate the e2e tests and perpetuate bad API surfaces;
explicitly marking deprecated items prevents them from accreting tests and documentation
- **Affected sections**: `01-concepts.md`, `04-rpc-workspace.md`, `04-rpc-spotlight.md`,
`04-rpc-pty.md`, `05-cli-commands.md`, `06-error-taxonomy.md`, `07-invariants.md`,
Known Limitations

### 2026-04-20 — Remote daemon contributor flow, local workspace topology, token storage, Firecracker improvements

- **Trigger**: Implementation session formalizing the remote Linux daemon contributor flow,
  including auto-mirroring, client-side workspace state, git worktree topology for forks,
  OS-native token storage, and forgevm-inspired Firecracker runtime improvements.
- **From**: PRD had no clauses for remote profile mirroring, client-side state, token store
  implementation, Firecracker init injection method, or fork worktree location.
- **To**:
  - `CLI-026`–`CLI-028`: `workspace create` auto-mirrors local path to remote host via mutagen
    when an SSH profile is active; `.worktrees/` excluded from base mirror; result recorded in
    client state.
  - `CLI-039`→`CLI-046`: `workspace fork` creates worktree at `<gitRoot>/.worktrees/<name>`,
    adds to `.git/info/exclude`, mirrors worktree independently, records in client state.
  - `CLI-018`: `nexus daemon token` — reads from OS-native store, used by remote connect.
  - `CLI-019`: `nexus daemon implode` — full teardown for fresh contributor start.
  - `DAEMON-070`–`DAEMON-074`: Token store — Keychain (macOS), SecretService/file (Linux),
    profile token never in JSON, remote connect uses full binary path for non-interactive SSH.
  - `DAEMON-080`–`DAEMON-084`: Firecracker — `init=` boot arg (no more debugfs init injection),
    `cp --reflink=auto --sparse=always` for all image copies, relative per-VM paths in API config,
    network mode on by default.
  - `DAEMON-090`–`DAEMON-094`: Client-side local workspace state file spec.
- **Rationale**: These behaviors are all implemented and in production on the PR branch. They need
  to be formally specced so the documentation task and e2e test writers have a normative reference.
- **Affected sections**: `05-cli-commands.md`, `08-daemon-lifecycle.md` (new §§ Token store,
  Firecracker runtime implementation, Client-side local workspace state)

### 2026-04-20 — Advisor review incorporated

- **Trigger**: Oracle advisor review after initial draft
- **From**: Single `04-rpc-methods.md` file; `08-test-coverage-map.md` in `docs/spec/`; flat
`ERR-NNN` inside domain prefixes (`WS-ERR-001`); `spec/` as primary test directory; T15/T16
as parallel tasks; `workspace.fork --ref` listed as optional; `workspace.restore` listed with
`snapshotId` param; `workspace.create` listed with flat params; `workspace.relations.list`
listed with `workspaceId`
- **To**: Per-domain RPC files; coverage map gitignored in `test/e2e/coverage/`; flat `ERR-NNN`
taxonomy; annotate-in-place strategy for existing tests + `spec/` only for new tests; T0
(ground-truth audit) added as first task; T16 deferred; all four wire-shape corrections applied;
`DAEMON-` prefix chapter added; `workspace.exec` documented as real command
- **Rationale**: Oracle identified four wire-shape mismatches (create envelope, fork required ref,
restore missing param, relations.list param name) that would cause tests written against the
spec to fail against the daemon; these are now documented correctly. Coverage map as gitignored
generated artifact avoids churn. Annotate-in-place gives non-zero day-one coverage.
- **Affected sections**: Architecture, Data Model, all `04-rpc-*.md` sections, Task Graph

