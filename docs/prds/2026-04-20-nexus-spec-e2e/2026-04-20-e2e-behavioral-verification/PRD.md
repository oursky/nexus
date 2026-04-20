---
type: child
feature_area: nexus-spec-e2e
date: 2026-04-20
topic: e2e-behavioral-verification
status: draft
parent_prd: 2026-04-20-nexus-spec-e2e
---

# Child PRD: E2E Behavioral Verification

## Parent Context

Parent PRD: `docs/prds/2026-04-20-nexus-spec-e2e/PRD.md`
Affected sections: Known Limitations (deprecated code), Task Graph (T8–T14), Architecture (e2e
test structure)

---

## Overview

The parent PRD establishes the system specification and lays out a plan for spec-driven e2e tests.
This child PRD scopes and details the first implementation wave: removing the accumulated mistakes
from the codebase, consolidating fragile shell scripts into the Go test suite, and implementing
full behavioral e2e verification from the end-user's perspective.

Current state problems:

1. **Dead code masquerading as API**: `workspace.checkout`, `workspace.relations.list`, and
   `workspace.tunnels.start/stop` are registered in the daemon and show up in test output but are
   explicitly deprecated in the spec. They contaminate coverage analysis and can be accidentally
   re-tested.

2. **Shell scripts doing e2e work the Go suite should own**: `e2e-workspace-features.sh` and
   `e2e-shell-workflow.sh` test spotlight proxy traffic, fork, docker-compose integration, and
   interactive shell via `expect` — none of these behaviors are in the Go e2e suite. Scripts
   are brittle (sed/grep parsing, port-picking loops, Node.js WebSocket calls), not run in CI by
   default, and cannot be traced to spec clauses.

3. **Fork test verifies only metadata, not behavior**: `workspace/fork_test.go` checks that
   `parentWorkspaceId` is set. It does NOT verify: (a) that the fork's filesystem content matches
   the parent at fork time, (b) that the git branch inside the fork workspace is `childRef`, or
   (c) that the client-side worktree syncs to the forked workspace. This is the most important
   invariant of the fork feature.

4. **Interactive shell test is not truly interactive**: `cli/interactive_test.go` uses
   `creack/pty` and sends commands after fixed sleep delays. It only checks that a specific string
   echoes back. It does not verify shell environment (working directory, git branch, environment
   variables), handle the TTY resize path, or test any non-trivial interaction.

5. **Spotlight test verifies record creation, not proxy traffic**: `spotlight/spotlight_test.go`
   calls `workspace.ports.add` and checks the response. It does NOT verify that a connection to
   `localPort` is actually forwarded to `remotePort` inside the workspace.

---

## What Changed / Why This Cannot Wait

If implementation proceeds against the parent PRD's T8–T14 (annotate existing tests, write new
spec tests) without first:

- removing the deprecated methods from the codebase, and
- defining what behavioral correctness means for fork, shell, and spotlight,

...then the annotation pass will create `// spec:` markers on tests that verify wrong behavior or
verify nothing at all. The coverage map will show green on clauses that are not actually exercised.
The CI gate will pass while the system's most important user-facing behaviors are unverified.

The deprecated code removal must happen before spec writing because the spec explicitly says these
methods have no normative clauses — if they remain in the codebase, any test touching them looks
like a coverage hit.

---

## Scope

This child PRD owns:

### Phase 1 — Code Cleanup (prerequisite, ~1.5d)

Remove the deprecated API surfaces. After this phase, the running daemon exactly matches the
normative spec surface.

**P1-T1**: Remove `workspace.checkout` handler registration and implementation.
- Delete `handleCheckout` from `internal/rpc/workspace/handlers.go`
- Delete `checkoutReq` / `checkoutRes` DTOs
- Remove `Register("workspace.checkout", ...)` from `internal/rpc/workspace/handler.go`
- Delete `nexus workspace checkout` CLI command from `cmd/nexus/commands/workspace/`
- Delete any e2e test that calls `workspace.checkout`

**P1-T2**: Remove `workspace.relations.list` handler registration and implementation.
- Delete `handleRelations` from `internal/rpc/workspace/handlers.go`
- Delete `relationsReq` / `relationsRes` DTOs
- Remove `Register("workspace.relations.list", ...)` from handler.go
- Verify no existing test calls it (or delete those tests)

**P1-T3**: Remove `workspace.tunnels.start` / `workspace.tunnels.stop` from the spotlight handler.
- Delete `HandleTunnelsStart`, `HandleTunnelsStop` from `internal/rpc/spotlight/handler.go`
- Delete `tunnelsStartParams`, `tunnelsStopParams` DTOs
- Remove both `Register(...)` calls from `spotlight/handler.go`
- Verify no CLI command calls these methods

**P1-T4**: Fix duplicate handler registration for `workspace.ports.*`.
- Remove `reg.Register("workspace.ports.list", ...)`, `workspace.ports.add`, `workspace.ports.remove`,
  `workspace.tunnels.start`, `workspace.tunnels.stop` from `internal/rpc/workspace/handler.go`
  and the corresponding dead handler functions `handlePortsList`, `handlePortsAdd`, `handlePortsRemove`,
  `handleTunnelsStart`, `handleTunnelsStop`, and all related DTOs.
- These are never reached (spotlight handler overwrites them). Removing them eliminates the
  confusion and makes the handler ownership unambiguous.
- Also remove the unreachable `workspace.discover-ports` registration from spotlight handler if it
  exists there (it belongs only in workspace handler — confirm).

---

### Phase 2 — Behavioral E2E Tests (the main body, ~4d)

New and replacement tests that verify end-user behavior. Each test cites spec clauses.

---

#### P2-T1: Fork behavioral verification (`test/e2e/spec/fork_behavior_test.go`)

This is the most important test in this PRD. The fork feature is the system's primary isolation
primitive. If it doesn't actually fork filesystem state and set the correct branch, it is broken
regardless of what the metadata says.

**What must be verified:**

**[A] Filesystem content parity at fork time**
- Create a workspace on `main`, start it.
- Write a sentinel file inside the running workspace (via `workspace exec` / `pty.create` with
  `args: ["-c", "echo 'parent-content' > /workspace/sentinel.txt"]`).
- Fork the workspace to `childRef` = `feature-branch`.
- Start the fork.
- Execute inside the fork: `cat /workspace/sentinel.txt`.
- Assert the sentinel content is present in the fork — the snapshot carried the file.
- Write a NEW file inside the fork: `echo 'fork-only' > /workspace/fork_only.txt`.
- Execute inside the parent: `ls /workspace/fork_only.txt`.
- Assert the file does NOT exist in the parent — isolation is preserved.

**[B] Git branch correctness in fork**
- After starting the fork, execute: `git -C /workspace rev-parse --abbrev-ref HEAD`.
- Assert output is `feature-branch` (the `childRef` requested at fork time).
- Assert the parent workspace still reports `main` when the same check is run there.

**[C] Client worktree creation for forked workspace**
- After `workspace fork`, assert the client-side worktree directory for the fork:
  - Exists at `<gitRoot>/.worktrees/<childName>` (co-located with the project root, NOT in
    a global `~/nexus-workspaces` directory).
  - Is a valid git worktree (`git rev-parse --is-inside-work-tree` returns `true`).
  - Is checked out at `childRef` (`git rev-parse --abbrev-ref HEAD` returns `childRef`).
- Assert that `<gitRoot>/.git/info/exclude` contains `.worktrees/`.
- Assert that `<gitRoot>/.worktrees/` does NOT appear in `git status` output.
- Assert the client state store (`~/.local/share/nexus/workspaces.json`) contains a record for
  the fork with `isWorktree: true`, `localPath` pointing to the worktree, and `gitRoot` pointing
  to the parent's original repo directory.

**[D] Worktree → fork VM sync (remote profile only)**
- When the active profile has an SSH target, write a file in the client worktree:
  `echo 'from-client' > <worktreePath>/client_write.txt`.
- Wait for Mutagen sync to propagate (poll with timeout, max 30s).
- Execute inside the fork: `cat /workspace/client_write.txt`.
- Assert the content is present — the fork's independent mirror session is live.

**Spec clauses**: `WS-030`, `WS-031`, `WS-032`, `WS-033`, `WS-057`–`WS-062`, `INV-010`

---

#### P2-T2: Interactive shell behavioral verification (`test/e2e/spec/interactive_shell_test.go`)

Replace the fixed-sleep PTY test with deterministic interactive verification.

**What must be verified:**

**[A] Working directory**
- Open `workspace shell` via PTY.
- Send: `pwd\n`, wait for line matching `/workspace`.
- Assert output before timeout (do NOT use fixed sleep — use output-scanning loop with timeout).

**[B] Git branch inside shell**
- Send: `git rev-parse --abbrev-ref HEAD\n`.
- Assert output matches the ref used to create the workspace (e.g. `main`).

**[C] Shell writes are reflected in filesystem**
- Send: `echo 'interactive-write' > /workspace/interactive_write.txt\n`.
- Send: `cat /workspace/interactive_write.txt\n`.
- Assert `interactive-write` appears in output.

**[D] TTY resize**
- Send SIGWINCH (resize) via `pty.Setsize` after the session is open.
- Send: `echo $COLUMNS\n`.
- Assert the reported `$COLUMNS` matches the new size.
- Spec clause: `PTY-020` (`pty.resize` MUST apply new dimensions immediately).

**[E] Exit code propagation**
- Send: `exit 42\n`.
- Assert the CLI exits with code 42.
- Spec clause: `CLI-034`.

**[F] `workspace exec` non-interactive command**
- Run `workspace exec <ws> -- cat /workspace/sentinel.txt` (non-interactive, not PTY).
- Assert output contains the expected content.
- Assert exit code 0.
- Run `workspace exec <ws> -- exit 7`.
- Assert exit code 7.

**Scan pattern for PTY output** (replaces fixed sleep):

```go
func scanPTYUntil(t *testing.T, r io.Reader, pattern string, timeout time.Duration) string {
    deadline := time.Now().Add(timeout)
    var buf bytes.Buffer
    tmp := make([]byte, 256)
    for time.Now().Before(deadline) {
        n, err := r.Read(tmp)
        if n > 0 {
            buf.Write(tmp[:n])
            if strings.Contains(buf.String(), pattern) {
                return buf.String()
            }
        }
        if err != nil {
            break
        }
    }
    t.Fatalf("scanPTYUntil: pattern %q not seen within %v; output: %q", pattern, timeout, buf.String())
    return ""
}
```

**Spec clauses**: `PTY-001`–`PTY-009`, `PTY-010`–`PTY-026`, `CLI-032`–`CLI-038`, `RPC-013`

---

#### P2-T3: Spotlight proxy traffic verification (`test/e2e/spec/spotlight_proxy_test.go`)

Replace the record-only spotlight test with actual traffic verification.

**What must be verified:**

**[A] Forward actually proxies traffic**
- Create and start a workspace.
- Inside the workspace, start an HTTP server listening on a known port (e.g. 9090):
  `workspace exec <ws> -- sh -c "echo -e 'HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK' | nc -l -p 9090 &"`.
- Create a spotlight forward: `workspace.ports.add {workspaceId, spec: {localPort: <free>, remotePort: 9090}}`.
- Make an HTTP request to `localhost:<localPort>`.
- Assert HTTP 200 response with body `OK`.
- Remove the forward: `workspace.ports.remove`.
- Assert that `localhost:<localPort>` is no longer reachable (connection refused or timeout).

**[B] Port conflict is rejected**
- Create a forward on `localPort: X`.
- Attempt to create a second forward with the same `localPort: X`.
- Assert `ERR-051` (`spotlight.port_conflict`).
- Spec clause: `SPOT-021`, `ERR-051`.

**[C] Forward list is accurate**
- Create two forwards.
- Call `workspace.ports.list`.
- Assert both forwards appear with correct ports and `state: active`.
- Remove one. Call `workspace.ports.list` again.
- Assert only the remaining forward appears.

**[D] `spotlight.list` (with workspaceId)**
- Call `spotlight.list {workspaceId: <ws>}`.
- Assert same result as `workspace.ports.list`.
- Confirms `SPOT-015`: the param is `workspaceId` (not absent).

**Spec clauses**: `SPOT-001`–`SPOT-009`, `SPOT-011`–`SPOT-025`, `WS-075`–`WS-082`, `ERR-051`, `ERR-052`

---

#### P2-T4: Workspace lifecycle state machine verification (`test/e2e/spec/lifecycle_state_test.go`)

Upgrade `workspace/lifecycle_test.go` with explicit illegal-transition assertions.

**What must be verified (beyond current lifecycle test):**

**[A] Illegal start on running workspace** → must return `ERR-011` with `data.kind: workspace.invalid_state`.
**[B] Illegal stop on stopped workspace** → `ERR-012`.
**[C] Illegal remove on running workspace** → `ERR-013`.
**[D] After remove, workspace not in list** — `INV-009`.
**[E] After start, workspace.info returns `running`** — `INV-007`.
**[F] After stop, workspace.info returns `stopped`** — `INV-008`.
**[G] `workspace.ready` returns `{ready: bool}` shape** — `WS-070`.

All error assertions MUST check `data.kind` string, not just the error message.

**Spec clauses**: `WS-017`–`WS-029`, `INV-007`–`INV-009`, `ERR-011`–`ERR-013`, `RPC-019`, `RPC-023`

---

#### P2-T5: Error taxonomy verification (`test/e2e/spec/error_taxonomy_test.go`)

Systematic verification that every `ERR-NNN` clause in `06-error-taxonomy.md` produces the
correct `data.kind` string. This is a new test with no existing analog.

**Structure**: One `t.Run` per `ERR-NNN`. Each sub-test:
1. Triggers the specific error condition (e.g. calling `workspace.info` with unknown ID for `ERR-002`).
2. Asserts `error.code == -32000`.
3. Asserts `error.data.kind == "<expected kind string>"`.

Error kinds to cover (all from `06-error-taxonomy.md`):

| ERR | How to trigger | Expected `data.kind` |
|-----|----------------|----------------------|
| `ERR-001` | `workspace.create` with duplicate name | `workspace.duplicate_name` |
| `ERR-002` | `workspace.info` with unknown id | `workspace.not_found` |
| `ERR-011` | `workspace.start` on `running` workspace | `workspace.invalid_state` |
| `ERR-012` | `workspace.stop` on `stopped` workspace | `workspace.invalid_state` |
| `ERR-013` | `workspace.remove` on `running` workspace | `workspace.invalid_state` |
| `ERR-022` | `workspace.fork` without `childRef` | `workspace.fork_missing_ref` |
| `ERR-040` | `project.create` with duplicate name | `project.duplicate_name` |
| `ERR-041` | `project.create` with empty `repoUrl` | `project.invalid_spec` |
| `ERR-042` | `project.get` with unknown id | `project.not_found` |
| `ERR-051` | `workspace.ports.add` with conflicting localPort | `spotlight.port_conflict` |
| `ERR-052` | `workspace.ports.remove` with unknown forwardId | `spotlight.not_found` |
| `ERR-061` | `pty.close` with unknown sessionId | `pty.not_found` |

**Note**: `ERR-014` (pause/resume on process backend), `ERR-023` (no snapshot), `ERR-030`/`ERR-031`
(auth tokens) are conditionally included — process-backend pause is permanently unavailable in the
test environment; snapshot-related errors require setup.

**Spec clauses**: `ERR-001`–`ERR-099`, `RPC-018`, `RPC-019`, `RPC-023`

---

#### P2-T6: Protocol framing verification (`test/e2e/spec/protocol_test.go`)

New test covering transport-level behavior.

**What must be verified:**

**[A] `/healthz` returns 200 with body `ok`** — `RPC-007`.
**[B] `/version` returns 200 with JSON `{version, buildTime}`** — `RPC-007`.
**[C] WebSocket upgrade without token returns 401** — `RPC-009`.
**[D] WebSocket upgrade with wrong token returns 401** — `RPC-009`.
**[E] Unknown RPC method returns error code `-32601`** — `RPC-020`.
**[F] Malformed JSON request returns error code `-32700`** — `RPC-021`.
**[G] `node.info` returns capabilities including `runtime.process`** — `DAEMON-022`.
**[H] `node.info` succeeds on first call after daemon start** — `DAEMON-024`.

**Spec clauses**: `RPC-007`–`RPC-024`, `DAEMON-020`–`DAEMON-024`

---

#### P2-T7: Shell script migration and deprecation

Convert the behavioral assertions from `scripts/nexus/e2e-workspace-features.sh` and
`scripts/remote/e2e-shell-workflow.sh` into the Go test suite, then deprecate the scripts.

**Behaviors to migrate:**

From `e2e-workspace-features.sh`:
- Host↔workspace file sync (write on host, read in workspace; write in workspace, read on host)
  → `test/e2e/spec/sync_test.go`
- Spotlight proxy reachability (covered by P2-T3)
- Fork (covered by P2-T1)

From `e2e-shell-workflow.sh`:
- Interactive shell with `pwd` verification (covered by P2-T2)
- Daemon restart + reconnect flow → `test/e2e/spec/daemon_lifecycle_test.go`

**After migration**: Add `# MIGRATED: see test/e2e/spec/` comment to each shell script.
Do NOT delete the scripts yet — they may run on remote targets not covered by Go harness.

**Spec clauses**: `INV-021`–`INV-025`, `DAEMON-040`–`DAEMON-057`

---

### Phase 3 — Coverage Gate (~0.5d)

**P3-T1**: Implement `test/e2e/coverage/generate.go`:
- Regex scan of `// spec:` annotations across `test/e2e/**/*.go`.
- Cross-reference against all `MUST` and `MUST NOT` clauses in `docs/spec/**/*.md` (once written).
- Emit `test/e2e/coverage/coverage-map.md` (gitignored).
- Exit non-zero if any MUST/MUST NOT clause has no coverage entry.

**P3-T2**: Add `make check-spec-coverage` to `Taskfile.yml`, called by CI via `scripts/ci/nexus-e2e.sh`.

---

## Harness Changes Required

### `harness/fixtures.go` additions

```go
// MakeGitRepoWithContent creates a temp git repo pre-seeded with named files.
// files is a map of relative path → content.
func MakeGitRepoWithContent(t *testing.T, name string, files map[string]string) string

// MakeGitRepoWithBranch creates a temp git repo with a specified commit on `main`
// and an additional named branch pointing at the same commit.
func MakeGitRepoWithBranch(t *testing.T, name, branchName string) string
```

### `harness/pty_helper.go` (new)

```go
// PTYSession wraps a running nexus CLI subprocess behind a real PTY.
// Methods: SendLine, WaitForOutput, Close, ExitCode.
// Uses output-scanning (not fixed sleep) for all waiting.
type PTYSession struct { ... }

func NewPTYSession(t *testing.T, cmd *exec.Cmd) *PTYSession
func (s *PTYSession) SendLine(t *testing.T, line string)
func (s *PTYSession) WaitForOutput(t *testing.T, pattern string, timeout time.Duration) string
func (s *PTYSession) Resize(t *testing.T, cols, rows uint16)
func (s *PTYSession) Close(t *testing.T)
func (s *PTYSession) ExitCode(t *testing.T) int
```

### `harness/exec_helper.go` (new)

```go
// WorkspaceExec runs a non-interactive command in a workspace via pty.create
// with args=["-c", command] and captures the full output. Waits for pty.exit notification.
func (c *CLIHarness) WorkspaceExec(t *testing.T, workspaceID, command string) (stdout string, exitCode int)
```

### `harness/port_helper.go` (new)

```go
// FreePorts returns n free TCP ports on 127.0.0.1.
// Replaces the bash pick_free_port loop.
func FreePorts(t *testing.T, n int) []int

// WaitForPort blocks until a TCP connection to addr succeeds or deadline expires.
func WaitForPort(t *testing.T, addr string, timeout time.Duration)

// AssertPortClosed asserts that no TCP listener is reachable at addr within timeout.
func AssertPortClosed(t *testing.T, addr string, timeout time.Duration)
```

---

## Verification Criteria

A test is only considered to verify a behavior if it:

1. Actually exercises the behavior end-to-end from the client's perspective (not mocked).
2. Asserts the observable outcome, not just the absence of an error.
3. Includes a negative check where the spec says something MUST NOT happen.
4. Cites at least one spec clause via `// spec:`.

**Fork is not verified until**: (a) file content inside fork matches parent at snapshot time, (b)
git branch is correct inside running fork, (c) client worktree sync is confirmed bidirectionally.

**Interactive shell is not verified until**: non-trivial output (pwd, branch, env var) is observed
with deterministic output scanning (no fixed sleep).

**Spotlight is not verified until**: actual TCP traffic flows through the forward (not just record
existence).

---

## Task Graph

### Task List

| ID | Task | Depends On | Files Touched | Est. |
|----|------|-----------|---------------|------|
| P1-T1 | Remove `workspace.checkout` (RPC + CLI handler) | — | `internal/rpc/workspace/`, `cmd/nexus/commands/workspace/` | 0.5d |
| P1-T2 | Remove `workspace.relations.list` (RPC handler) | — | `internal/rpc/workspace/` | 0.25d |
| P1-T3 | Remove `workspace.tunnels.start/stop` from spotlight handler | — | `internal/rpc/spotlight/` | 0.25d |
| P1-T4 | Remove dead workspace handler registrations for `ports.*` / `tunnels.*` | P1-T3 | `internal/rpc/workspace/` | 0.25d |
| P2-T0 | Harness additions: `MakeGitRepoWithContent`, `PTYSession`, `WorkspaceExec`, `FreePorts` | P1-T1–P1-T4 | `test/e2e/harness/` | 0.5d |
| P2-T1 | Fork behavioral test (content, branch, worktree sync) | P2-T0 | `test/e2e/spec/fork_behavior_test.go` | 1d |
| P2-T2 | Interactive shell test (pwd, branch, write, resize, exit code) | P2-T0 | `test/e2e/spec/interactive_shell_test.go` | 0.5d |
| P2-T3 | Spotlight proxy traffic test (actual TCP, conflict, list, remove) | P2-T0 | `test/e2e/spec/spotlight_proxy_test.go` | 0.5d |
| P2-T4 | Lifecycle state machine test (illegal transitions + error shapes) | P2-T0 | `test/e2e/spec/lifecycle_state_test.go` | 0.5d |
| P2-T5 | Error taxonomy test (ERR-NNN → data.kind) | P2-T0 | `test/e2e/spec/error_taxonomy_test.go` | 0.5d |
| P2-T6 | Protocol framing test (healthz, auth, error codes) | P2-T0 | `test/e2e/spec/protocol_test.go` | 0.5d |
| P2-T7 | Shell script migration + deprecation comments | P2-T1, P2-T2, P2-T3 | `scripts/nexus/`, `scripts/remote/` | 0.25d |
| P3-T1 | Coverage scanner + map generator | P2-T1–P2-T6 | `test/e2e/coverage/` | 0.5d |
| P3-T2 | CI gate in Taskfile + nexus-e2e.sh | P3-T1 | `Taskfile.yml`, `scripts/ci/nexus-e2e.sh` | 0.25d |

### Dependency Graph

```
P1-T1, P1-T2, P1-T3 (parallel, no deps)
P1-T3 → P1-T4

P1-T1–P1-T4 → P2-T0

P2-T0 → P2-T1, P2-T2, P2-T3, P2-T4, P2-T5, P2-T6 (all parallel after T0)

P2-T1 + P2-T2 + P2-T3 → P2-T7

P2-T1–P2-T6 → P3-T1 → P3-T2
```

### Critical path

P1-T1–P1-T4 (1.25d) → P2-T0 (0.5d) → P2-T1 (1d) → P3-T1 (0.5d) → P3-T2 (0.25d) = **~3.5 days**

P2-T2 through P2-T6 run in parallel with P2-T1, so the wall-clock time is driven by P2-T1
(the most complex test). Total parallelized wall-clock: ~2.5d after P1 is done.

---

## Definition of Done

- [ ] Daemon registers zero deprecated methods: `workspace.checkout`, `workspace.relations.list`,
  `workspace.tunnels.start`, `workspace.tunnels.stop` are absent from the handler registry.
- [ ] Workspace handler does not register `workspace.ports.*` (spotlight handler owns those names
  without any overwrites).
- [ ] `TestForkBehavior_ContentParity`, `TestForkBehavior_BranchCorrectness`, and
  `TestForkBehavior_WorktreeSync` all pass.
- [ ] `TestInteractiveShell` passes with deterministic output scanning (no fixed sleeps).
- [ ] `TestSpotlightProxy_TrafficFlows` passes (actual HTTP request through forward).
- [ ] `TestErrorTaxonomy` covers all 12 `ERR-NNN` clauses above with `data.kind` assertions.
- [ ] `make check-spec-coverage` runs in CI and exits 0.
- [ ] No shell script behavior tested exclusively in bash — all critical behaviors have Go analogs.
