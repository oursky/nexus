# CLI Reference

## Connection Model

**Profile** — stored at `~/.config/nexus/profiles/default.json` after `nexus daemon connect`.

**SSH tunnel** — the CLI opens an SSH tunnel on demand (cached per session) and connects via WebSocket. The daemon runs on a Unix socket on the remote host; the tunnel maps a local port to the daemon's WebSocket listener.

**Auth** — Bearer token sent in the WebSocket `Authorization` header.

---

## Command Reference

### `nexus daemon`

Manage the connection profile and the remote daemon process.

#### `nexus daemon connect <user@host> [flags]`

Store a profile and fetch an auth token via SSH.

| Flag | Description | Default |
|------|-------------|---------|
| `--port PORT` | Daemon port on remote host | — |
| `--ssh-port PORT` | SSH port for tunnel | 22 |

```bash
nexus daemon connect newman@linuxbox --port 7777
```

#### `nexus daemon disconnect`

Remove the stored profile.

#### `nexus daemon start`

Start the daemon process on the remote host.

#### `nexus daemon stop`

Stop the daemon process on the remote host.

#### `nexus daemon status`

Show daemon status.

#### `nexus daemon token`

Print the current auth token from the stored profile.

---

### `nexus workspace`

Full workspace lifecycle management.

| Command | Description |
|---------|-------------|
| `nexus workspace list` | List all workspaces |
| `nexus workspace create` | Create a new workspace |
| `nexus workspace start <id>` | Start a workspace |
| `nexus workspace stop <id>` | Stop a workspace |
| `nexus workspace remove <id>` | Remove a workspace |
| `nexus workspace fork <id>` | Fork a workspace |
| `nexus workspace portal <id>` | Open workspace portal |
| `nexus workspace ready <id>` | Poll until workspace is ready |
| `nexus workspace restore <id>` | Restore workspace state |
| `nexus workspace run <id> <command>` | Run a command in the workspace |
| `nexus workspace shell <id>` | Open an interactive shell |
| `nexus workspace sshcheck <id>` | Check SSH connectivity to workspace guest |
| `nexus workspace serial-log <id>` | Show workspace serial log |
| `nexus workspace export <id> --out <path>` | Export workspace to a `.nxbundle` archive and standalone runner script |
| `nexus workspace import --from <path>` | Inspect a `.nxbundle` and verify host compatibility (use `--dry-run` to preview) |

---

### `nexus spotlight`

Port-forward management. Spotlight discovers Docker Compose ports in a workspace and creates daemon forwards + SSH tunnels to the local machine.

| Command | Description |
|---------|-------------|
| `nexus spotlight start <workspace-id>` | Discover all compose ports, create forwards + SSH tunnels |
| `nexus spotlight stop <workspace-id>` | Stop all forwards for the workspace |
| `nexus spotlight list` | List active spotlight forwards |
| `nexus spotlight port <workspace-id> <port>` | Show info for a specific port |
| `nexus spotlight port add <workspace-id> <port>` | Add a port forward |
| `nexus spotlight port list <workspace-id>` | List forwarded ports for workspace |
| `nexus spotlight port remove <workspace-id> <port>` | Remove a port forward |

---

### `nexus project`

Project management.

| Command | Description |
|---------|-------------|
| `nexus project list` | List projects |
| `nexus project create` | Create a project |
| `nexus project get <id>` | Get project details |
| `nexus project remove <id>` | Remove a project |
| `nexus project reconcile` | Reconcile project workspace repositories |

---

### `nexus exec`

Run a command in a workspace and stream its output. Alias: `nexus workspace exec` / `nexus workspace run`.

```bash
nexus exec <workspace> -- <command> [args...]
```

| Flag | Description |
|------|-------------|
| `--workdir` | Working directory inside the workspace (default `/workspace`) |

---

## RPC Methods

The daemon exposes JSON-RPC 2.0 over WebSocket. These methods are called by the CLI and SDK.

### Node

| Method | Description |
|--------|-------------|
| `node.info` | Node identity and capabilities |

### Workspace

| Method | Description |
|--------|-------------|
| `workspace.list` | List workspace records |
| `workspace.create` | Create a new workspace |
| `workspace.start` | Start workspace compute |
| `workspace.stop` | Stop workspace compute |
| `workspace.remove` | Remove workspace by id |
| `workspace.info` | Get workspace info |
| `workspace.fork` | Fork a workspace |
| `workspace.restore` | Restore workspace state |
| `workspace.ready` | Poll readiness until success/timeout |
| `workspace.discover-ports` | Discover Docker Compose published ports |
| `workspace.sshcheck` | Check SSH connectivity to workspace guest |
| `workspace.serial-log` | Read workspace serial log |
| `workspace.nexusfile` | Read Nexusfile intent from the workspace's repo directory on the daemon host |

### Spotlight

| Method | Description |
|--------|-------------|
| `spotlight.start` | Start port forwards for a workspace |
| `spotlight.stop` | Stop port forwards for a workspace |
| `spotlight.list` | List active forwards |

### Project

| Method | Description |
|--------|-------------|
| `project.list` | List projects |
| `project.create` | Create a project |
| `project.get` | Get project by id |
| `project.remove` | Remove project by id |
| `project.reconcile` | Reconcile project workspace repositories |

### Filesystem

| Method | Description |
|--------|-------------|
| `fs.readFile` | Read file contents |
| `fs.writeFile` | Write file contents |
| `fs.mkdir` | Create directory |
| `fs.readdir` | List directory contents |
| `fs.exists` | Check if path exists |
| `fs.stat` | Get file stats |
| `fs.rm` | Remove file or directory |

### Execution

| Method | Description |
|--------|-------------|
| `pty.*` | PTY session operations (create, list, write, resize, rename, close, reattach) |

### Daemon

| Method | Description |
|--------|-------------|
| `daemon.log.tail` | Tail daemon log lines |

### Auth

| Method | Description |
|--------|-------------|
| `authrelay.mint` | Mint a one-time auth relay token |
| `authrelay.revoke` | Revoke an auth relay token |
