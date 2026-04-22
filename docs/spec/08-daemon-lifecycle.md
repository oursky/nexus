# Nexus System Specification — Chapter 08: Daemon Lifecycle

> **Status**: Normative

---

## Data directory — `DAEMON-030`–`DAEMON-032`

**`DAEMON-030`** — Default data directory resolution (in priority order):
1. `$XDG_STATE_HOME/nexus` (if `XDG_STATE_HOME` is set)
2. `~/.local/state/nexus`
3. `/var/lib/nexus` (when home directory is unavailable)

**`DAEMON-031`** — Default socket path: `<data-dir>/nexusd.sock`.

**`DAEMON-032`** — Default database path: `<data-dir>/nexus.db`.

---

## Startup sequence — `DAEMON-040`–`DAEMON-052`

**`DAEMON-040`** — On start (when not the background re-exec'd child), the daemon runs the host
prerequisites setup (Firecracker binary, kernel, rootfs, networking) via `StartSetupFn`. This is
skipped in test/sandbox mode (no `StartSetupFn`).

**`DAEMON-041`** — The daemon opens (or creates) the SQLite database. Failure to open the DB is
fatal (exit 1).

**`DAEMON-042`** — If a stale socket file exists at the configured socket path, the daemon MUST
remove it before binding the new socket. A failure to remove the stale socket is fatal.

**`DAEMON-043`** — The daemon registers all RPC handlers in this order:
1. Workspace handler (`workspace.*`, `workspace.discover-ports`)
2. Spotlight handler (`spotlight.*`, `workspace.ports.*`) — overwrites no methods since
   workspace handler no longer registers these names
3. PTY handler (`pty.*`)
4. FS handler (`fs.*`)
5. Node info handler (`node.info`)
6. Project handler (`project.*`)
7. Auth relay handler (`authrelay.*`)

**`DAEMON-044`** — After binding the socket and registering all handlers, the daemon is ready to
accept connections. This MUST happen before the socket file appears on disk (the file appearing
is the readiness signal for background-mode startup).

**`DAEMON-045`** — `node.info` MUST succeed on the first call after the socket appears.

**`DAEMON-046`** — Guest agent injection (Firecracker mode): the daemon writes the embedded
`nexus-firecracker-agent` binary into the rootfs via `debugfs` at startup. This happens in the
parent process before re-exec, so the background child sees a consistent rootfs. Injection is
skipped when the binary SHA-256 hash matches the cached value in `<data-dir>/rootfs-agent.sha256`.

**`DAEMON-047`** — If Firecracker is enabled but `--rootfs` or `--kernel` is not provided (on
Linux release builds), the daemon exits with code 1 and an error directing the user to run
`nexus setup`.

**`DAEMON-048`** — Network listener startup: if `--network` is true, the daemon starts the HTTP
server on `<bind>:<port>`. The server MUST be ready to accept connections before the socket file
appears (both are synchronous in the start sequence).

**`DAEMON-049`** — Self-daemonize: the parent process re-execs the binary with
`NEXUS_DAEMON_FOREGROUND=1` as a new session leader (detached). The parent polls the socket path
every 200ms for up to 30 seconds. If the socket does not appear within 30 seconds, the parent
returns an error. With `--foreground`, this step is skipped.

**`DAEMON-050`** — `NEXUS_DAEMON_FOREGROUND=1` in env skips the setup step and the re-exec. The
process runs as the daemon directly.

---

## Network security — `DAEMON-053`–`DAEMON-058`

**`DAEMON-051`** — Bearer token is REQUIRED when the network listener is enabled. The token is
resolved from: `--token` flag → `NEXUS_DAEMON_TOKEN` env → auto-generated and persisted via
tokenstore.

**`DAEMON-052`** — If `--bind` is not a loopback address, TLS MUST be `auto` or `required`. The
daemon MUST refuse to start with `--tls off` on a non-loopback bind address.

**`DAEMON-053`** — The daemon MUST NOT log the token value at any log level.

**`DAEMON-054`** — WebSocket connections without a valid `Authorization: Bearer <token>` header
MUST be rejected with HTTP 401. This applies even if the token is auto-generated.

**`DAEMON-055`** — The Unix socket has no authentication. Security is enforced by filesystem
permissions on the socket file. The daemon MUST create the socket with restrictive permissions
(owner-only read/write).

---

## Shutdown sequence — `DAEMON-060`–`DAEMON-067`

**`DAEMON-060`** — On SIGTERM or SIGINT, the daemon initiates graceful shutdown.

**`DAEMON-061`** — During graceful shutdown, the daemon MUST close all active spotlight listeners
(port-forward proxies).

**`DAEMON-062`** — During graceful shutdown, the daemon MUST close all active PTY sessions
(kill local processes; send `shell.close` to Firecracker guests).

**`DAEMON-063`** — During graceful shutdown, the daemon MUST close the SQLite database.

**`DAEMON-064`** — During graceful shutdown, the daemon MUST remove the Unix socket file.

**`DAEMON-065`** — Graceful shutdown has an implementation-defined timeout. After the timeout,
outstanding goroutines are abandoned.

**`DAEMON-066`** — The daemon MUST exit with code 0 after clean shutdown.

**`DAEMON-067`** — SIGKILL is not graceful; no cleanup guarantees apply. Stale socket files may
remain and will be cleaned up on next startup (see `DAEMON-042`).
