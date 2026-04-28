# Nexus System Specification — Chapter 05: CLI Commands

> **Status**: Normative  
> Exit code 1 = operational error; exit code 2 = usage error (wrong flags/args).

---

## Global conventions — `CLI-001`–`CLI-006`

**`CLI-001`** — All commands connect to the daemon via `EnsureDaemon()` (Unix socket RPC) or
`EnsureMux()` (WebSocket mux RPC for commands requiring push notifications). Connection is
established by:
1. If `NEXUS_E2E_DAEMON_WEBSOCKET` is set: dial that URL with `NEXUS_DAEMON_TOKEN`
2. Otherwise: load default profile and open an SSH tunnel to the daemon's WebSocket endpoint

**`CLI-002`** — If the daemon is unreachable, all commands exit with code 1 and print to stderr.

**`CLI-003`** — Unknown subcommands exit with code 2 and print usage to stderr.

**`CLI-004`** — All commands print human-readable output to stdout on success.

**`CLI-005`** — `--json` flag (where supported) prints the result as a JSON object/array to stdout.

**`CLI-006`** — Workspace arguments (`<id-or-name>`) are resolved by `ResolveWorkspaceID`, which
first tries exact ID match, then falls back to name lookup.

---

## `nexus daemon start` — `CLI-010`–`CLI-019`

**`CLI-010`** — Flags:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--db` | string | `<data-dir>/nexus.db` | SQLite database path |
| `--socket` | string | `<data-dir>/nexusd.sock` | Unix socket path |
| `--kernel` | string | `$NEXUS_VM_KERNEL` | VM kernel image path (libkrun) |
| `--rootfs` | string | `$NEXUS_VM_ROOTFS` | VM rootfs image path (libkrun) |
| `--workdir-root` | string | (default under state dir) | libkrun VM work dir root |
| `--driver` | string | (auto) | `libkrun` or `sandbox` override |
| `--node-name` | string | hostname | Node identity name |
| `--network` | bool | `true` | Enable WebSocket network listener |
| `--bind` | string | `127.0.0.1` | Network listener bind address |
| `--port` | int | `7777` | Network listener port |
| `--tls` | string | `off` | TLS mode: `off` \| `auto` \| `required` |
| `--tls-cert` | string | — | TLS cert PEM file (for `required` mode) |
| `--tls-key` | string | — | TLS key PEM file (for `required` mode) |
| `--token` | string | — | Static bearer token (auto-generated if blank + network enabled) |
| `--foreground` | bool | `false` | Stay in foreground; skip self-daemonize |
| `--sandbox` | bool | `false` | Use process sandbox backend (hidden, testing only) |

**`CLI-011`** — Default self-daemonize behaviour: `nexus daemon start` re-execs itself with
`NEXUS_DAEMON_FOREGROUND=1`, routes logs to `<socket-dir>/daemon.log`, and waits up to 30 seconds
for the socket file to appear before reporting success or failure.

**`CLI-012`** — `--foreground` skips self-daemonize and blocks until SIGINT/SIGTERM.

**`CLI-013`** — Token resolution: `--token` flag → `NEXUS_DAEMON_TOKEN` env → auto-generated via
tokenstore (persisted to `<data-dir>/token` or equivalent). Token is required when `--network`
is enabled.

**`CLI-014`** — libkrun is the default VM backend on Linux (when `--sandbox` is NOT set). On macOS,
libkrun is not supported; `nexus daemon start` without `--sandbox` exits with code 1 and an
error message on macOS.

**`CLI-015`** — Guest agent injection: on Linux libkrun builds, the daemon injects the
`nexus-guest-agent` binary into the rootfs at startup. Injection is skipped if the binary
hash matches the cached hash.

**`CLI-016`** — Auto-setup: on Linux with `StartSetupFn` wired (release builds), the daemon runs
host bootstrap (libkrun libraries, kernel, rootfs) before starting. This is skipped in the
re-exec'd background child.

**`CLI-017`** — Exit code 1 if daemon fails to start (socket bind failure, DB failure, invalid
config, missing rootfs/kernel on Linux release builds).

**`CLI-018`** — The readiness signal for tests and scripts is the socket file appearing on disk.
For WebSocket-connected tests, `/healthz` returning 200 is the readiness signal.

**`CLI-019`** — `NEXUS_DAEMON_SERVE=1` env var: not a daemon start flag; used by e2e harness to
control network listener behaviour in test mode.

---

## `nexus daemon stop` — `CLI-020`

**`CLI-020`** — No positional args. Flags: `--socket string` (default: data-dir socket),
`--port int` (default 7777). Sends SIGTERM to the running daemon process. Exits 0 on success,
1 if daemon is not running.

---

## `nexus workspace create` — `CLI-030`–`CLI-035`

**`CLI-030`** — Flags:

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--repo` | yes | — | Repository URL or local path |
| `--name` | yes | — | Workspace name |
| `--ref` | no | `main` | Git ref |
| `--profile` | no | `default` | Agent profile |
| `--backend` | no | — | Runtime backend (`libkrun` \| `process`) |

**`CLI-031`** — Optional first positional argument: repo shorthand (takes precedence over `--repo`
if both are provided — exact precedence is implementation-defined; prefer `--repo` for clarity).

**`CLI-032`** — On success, prints the created workspace ID and name to stdout.

**`CLI-033`** — Calls `workspace.create` with `{"spec": {...}}` (nested).

**`CLI-034`** — Exits 2 if `--repo` or `--name` is missing.

**`CLI-035`** — Post-success: workspace is in state `created`.

---

## `nexus workspace list` — `CLI-036`

**`CLI-036`** — No flags, no positional args. Prints workspace ID, name, and state for each
non-removed workspace. Calls `workspace.list`.

---

## `nexus workspace info` — `CLI-037`–`CLI-038`

**`CLI-037`** — Positional arg: `<id-or-name>` (required). Optional: `--json`.

**`CLI-038`** — Calls `workspace.info {"id": resolvedID}`. `--json` prints the full JSON object.

---

## `nexus workspace start` — `CLI-039`–`CLI-041`

**`CLI-039`** — Positional arg: `<id>` (required). No flags.

**`CLI-040`** — Calls `workspace.start {"id": ...}`.

**`CLI-041`** — After start, the CLI also calls `workspace.discover-ports {"id": wsID}` to
display any immediately-available port information. This is a display-only step; failure is
non-fatal.

---

## `nexus workspace stop` — `CLI-042`

**`CLI-042`** — Positional arg: `<id>` (required). No flags. Calls `workspace.stop {"id": ...}`.

---

## `nexus workspace remove` — `CLI-043`

**`CLI-043`** — Positional arg: `<id>` (required). Flags: `--force` (skip confirmation prompt).
Aliases: `rm`, `delete`. Calls `workspace.remove {"id": ...}`.

---

## `nexus workspace fork` — `CLI-044`–`CLI-046`

**`CLI-044`** — Positional arg: `<workspace>` (required).

**`CLI-045`** — Flags:
- `--ref string` — REQUIRED. The git ref for the forked workspace. Omitting it exits with code 2.
- `--name string` — Optional. Child workspace name (daemon generates one if omitted).

**`CLI-046`** — Calls `workspace.fork {"id": resolvedID, "childWorkspaceName": name, "childRef": ref}`.

---

## `nexus workspace restore` — `CLI-047`

**`CLI-047`** — Positional arg: `<workspace>` (required). No flags. Calls
`workspace.restore {"id": resolvedID}`. Restores from most recent snapshot; no snapshot selection.

---

## `nexus workspace ready` — `CLI-048`

**`CLI-048`** — Positional arg: `<workspace>` (required). No flags. Calls
`workspace.ready {"id": resolvedID}`. Exits 0 if ready, 1 if not ready.

---

## `nexus workspace shell` — `CLI-049`–`CLI-051`

**`CLI-049`** — Positional arg: `<workspace>` (required). Flags: `--workdir string` (default `/workspace`).

**`CLI-050`** — Opens an interactive PTY session via `pty.create` (shell=default, args=["-l"]),
streams data bidirectionally using mux connection. Requires WebSocket/mux.

**`CLI-051`** — Exit code mirrors the shell exit code received via `pty.exit` notification.

---

## `nexus workspace exec` — `CLI-052`–`CLI-056`

**`CLI-052`** — Use: `exec <workspace> -- <command> [args...]`. Alias: `run` (stopgap; see
`CLI-056`). Positional: workspace ID + command after `--`.

**`CLI-053`** — Flags: `--workdir string` (default `/workspace`).

**`CLI-054`** — Calls `pty.create` with `shell="/bin/sh"`, `args=["-c", "<command>"]`,
`workDir=<workdir>`. Streams stdout. Exits when `pty.exit` is received.

**`CLI-055`** — Exit code mirrors the command exit code.

**`CLI-056`** — The `run` alias is a STOPGAP. The intended future design for `nexus workspace run`
is an ephemeral workspace lifecycle: create a workspace → start it → exec the given command →
automatically remove the workspace when the command exits (analogous to Daytona's `daytona run`).
This ephemeral behavior is NOT yet implemented. Until implemented, `run` and `exec` are identical.

---

## `nexus workspace portal` — `CLI-057` [STUB / BROKEN]

**`CLI-057`** — Positional arg: `<workspace>` (required). This command calls `workspace.portsList`
(a method that does NOT exist on the daemon). It is a stub. It MUST NOT be relied upon. Do NOT
write e2e tests for this command until the underlying RPC method is implemented.

---

## `nexus spotlight start` — `CLI-060`–`CLI-066`

**`CLI-060`** — Positional arg: `<workspace-id>` (required). No flags.

**`CLI-061`** — Behaviour:
1. Calls `workspace.discover-ports {"id": workspaceID}` to get discovered ports
2. If no ports discovered, prints "no ports discovered" and exits 0
3. Loads SSH profile; stops any previous spotlight for that profile via
   `spotlight.stop {"workspaceId": <prev>}`
4. For each discovered port: calls `spotlight.start {"workspaceId", "spec"}` to create a
   daemon-side forward, then opens an SSH tunnel from client to daemon
5. On any failure mid-loop: rolls back by calling `spotlight.stop {"workspaceId": workspaceID}`
6. Persists `workspaceID` to client state file
7. Prints `<service> → localhost:<boundPort>` for each forwarded port

**`CLI-062`** — Port remapping: if the desired `localPort` is already in use (another process or
workspace), the SSH tunnel binds an ephemeral port and prints a remapping notice.

**`CLI-063`** — Exit code 1 on any forward or tunnel failure (after rollback).

---

## `nexus spotlight list` — `CLI-064`

**`CLI-064`** — Use: `list <workspace-id>` (alias `ls`). Positional arg: `<workspace-id>` (required).
Flags: `--json`. Calls `spotlight.list {"workspaceId": ...}`. Prints forward ID, workspace,
local port, remote port, state.

---

## `nexus spotlight stop` — `CLI-065`–`CLI-066`

**`CLI-065`** — Use: `stop <workspace-id>`. Positional arg: `<workspace-id>` (required, NOT a
forward ID). Flags: `--force` (skip TTY confirmation prompt).

**`CLI-066`** — Calls `spotlight.stop {"workspaceId": workspaceID}` which closes ALL forwards for
the workspace. Also clears client state and closes all cached SSH tunnels.

---

## `nexus spotlight port add` — `CLI-067`–`CLI-069`

**`CLI-067`** — Use: `add <workspace-id>`. Positional arg: `<workspace-id>` (required).

**`CLI-068`** — Flags:
- `--port int` — REQUIRED (local port to expose). Returns error if 0.
- `--remote-port int` — Optional (defaults to `--port` value if 0).
- `--protocol string` — Optional (e.g. `tcp`, `http`).

**`CLI-069`** — Calls `workspace.ports.add {"workspaceId", "spec": {localPort, remotePort, protocol}}`.

---

## `nexus spotlight port list` — `CLI-070`

**`CLI-070`** — Use: `list <workspace-id>` (alias `ls`). Positional: `<workspace-id>` (required).
Flags: `--json`. Calls `workspace.ports.list {"workspaceId": ...}`.

---

## `nexus spotlight port remove` — `CLI-071`–`CLI-072`

**`CLI-071`** — Use: `remove <workspace-id> <forward-id>` (alias `rm`). Two positional args
required: workspace ID and forward ID.

**`CLI-072`** — Flags: `--force` (skip TTY confirmation). Calls
`workspace.ports.remove {"workspaceId", "forwardId"}`.

---

## `nexus project create` — `CLI-080`

**`CLI-080`** — Flags: `--name string` (required), `--repo string` (required), `--root-path string` (optional).
Calls `project.create {"name", "repoUrl", "rootPath?"}`.

---

## `nexus project list` — `CLI-081`

**`CLI-081`** — No flags, no positional args. Calls `project.list {}`.

---

## `nexus project get` — `CLI-082`

**`CLI-082`** — Positional arg: `<id-or-name>` (required). Calls `project.get {"id": resolvedID}`.

---

## `nexus project remove` — `CLI-083`

**`CLI-083`** — Positional arg: `<id-or-name>` (required). Calls `project.remove {"id": resolvedID}`.

---

## `nexus dev up` — `CLI-090`–`CLI-094`

**`CLI-090`** — Use: `up` (no positional args). Flags:
- `--name string` — Override app name
- `--port int` — Override dev port
- `--build-init` — Force re-run of build.init (invalidate cache)
- `--detach, -d` — Run in background and return immediately
- `--json` — Output workspace info as JSON

**`CLI-091`** — Behaviour: resolve Nexusfile (or infer conventions) → create/start dev workspace → run `dev.up` → auto-forward `dev.port` (and discovered compose ports) → print summary.

**`CLI-092`** — The dev workspace is tagged with label `dev=true`. `nexus dev up` finds an existing dev workspace by this label and reuses it if the project root matches.

**`CLI-093`** — Exit code 0 on success; 1 if daemon unreachable, workspace start fails, or `dev.up` exits non-zero; 2 for invalid flags.

**`CLI-094`** — Port readiness: if `dev.port` is known, poll TCP connect every 500ms up to 60s. If unreachable, print warning but do not fail.

---

## `nexus dev down` — `CLI-095`–`CLI-097`

**`CLI-095`** — Use: `down` (no positional args). Flags: `--force` (skip confirmation), `--json`.

**`CLI-096`** — Behaviour: find dev workspace by label `dev=true` → run `dev.down` if defined → stop all Spotlight forwards → stop workspace.

**`CLI-097`** — Exit code 1 if no active dev session found for the current project.

---

## `nexus dev status` — `CLI-098`

**`CLI-098`** — Use: `status` (no positional args). Prints app name, workspace ID, running `dev.up` command, active forwards, and declared deploy domains.

---

## `nexus config validate` — `CLI-100`–`CLI-101`

**`CLI-100`** — Use: `validate` (no positional args). Flags: `--json`.

**`CLI-101`** — Validates Nexusfile in current directory (or auto-detects conventions if file missing). Performs full pipeline: parse → legacy detection → auto-detect → semantic validation. Prints validation errors or success message.

---

## `nexus config migrate` — `CLI-102`–`CLI-103`

**`CLI-102`** — Use: `migrate` (no positional args). Performs one-shot rewrite of legacy Nexusfile to new format.

**`CLI-103`** — Writes migrated content to `Nexusfile` in place. Backs up original to `Nexusfile.backup`. Prints migration summary and prompts user to review `build.image` and `build.init`.

---

## `nexus deploy plan` — `CLI-110` [STUB]

**`CLI-110`** — Reserved for future deploy pipeline integration. Resolves Nexusfile + platform profile + build outputs into a deploy plan. Not yet implemented.

---

## `nexus deploy apply` — `CLI-111` [STUB]

**`CLI-111`** — Reserved for future deploy pipeline integration. Applies resolved deploy plan. Not yet implemented.
