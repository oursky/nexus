# CLI

This reference is intentionally named `cli.md` because it covers Nexus control-plane interfaces, not only workspace internals.

Semantic boundary:

- `nexus` is the human/operator command interface.
- `workspace-daemon` is the programmatic runtime/API interface consumed by SDK and automation.

The sections below focus on daemon runtime behavior and RPC/HTTP APIs, which are the stable contract surface for remote control.

The workspace daemon is a Go-based server that provides remote file system and execution capabilities to the Nexus SDK via WebSocket.

## Overview

```
┌─────────────┐     WebSocket      ┌─────────────────┐
│ SDK Client  │ ◄────────────────► │  Workspace      │
│             │                    │  Daemon (Go)    │
└─────────────┘                    └────────┬────────┘
                                             │
                                      ┌──────▼──────┐
                                      │ Isolated    │
                                      │ Workspace   │
                                      │ (firecracker) │
                                      └─────────────┘
```

The daemon manages isolated Firecracker-backed workspaces using native Firecracker integration. The daemon communicates directly with Firecracker via Unix socket REST API and executes commands through a vsock guest agent (a binary running inside the VM that receives commands over the virtio-vsock interface and executes them on behalf of the daemon). All ingress to the workspace is via Spotlight port forwards — there is no direct host port exposure.

## Installation

```bash
# Build from source
cd packages/nexus
go build -o workspace-daemon ./cmd/daemon
```

## Running the Daemon

```bash
workspace-daemon \
  --port 8080 \
  --token <jwt-secret> \
  --workspace-dir /workspace
```

## Embedded Web UI

The daemon serves an embedded web control plane for workspace operations.

- UI path: `/ui` (legacy alias: `/portal`)
- Summary API: `GET /ui/api/summary`
- Workspace APIs (token-required):
  - `GET /ui/api/workspaces` - list workspaces
  - `POST /ui/api/workspaces` - create workspace
  - `POST /ui/api/workspaces/{id}/actions/{action}` - lifecycle action (`start`, `stop`, `restore`, `pause`, `resume`)
  - `POST /ui/api/workspaces/{id}/fork` - fork workspace
  - `DELETE /ui/api/workspaces/{id}` - remove workspace

Authentication for UI APIs supports:

- `X-Nexus-Token: <daemon token>` header (used by embedded UI)
- `Authorization: Bearer <token>` header
- `?token=<token>` query parameter

Open locally:

```bash
open "http://localhost:8080/ui"
```

## Configuration

| Flag | Description | Default |
|------|-------------|---------|
| `--port` | Server port | 8080 |
| `--token` | Authentication token | - |
| `--workspace-dir` | Workspace directory | /workspace |
| `--host` | Host to bind to | localhost |

## Intent Model

Nexus CLI and daemon surfaces are organized by operator intent. This keeps command discovery and SDK mapping stable.

### 1) Auth and Session

- Authenticate and establish control-plane connectivity.
- Typical surfaces: daemon token validation and client connection bootstrap.

### 2) Workspace Lifecycle

Canonical lifecycle verbs used across daemon APIs and SDK:

- `create`
- `list`
- `open`
- `start`
- `pause`
- `resume`
- `stop`
- `restore`
- `fork`
- `remove`

### 3) Execution and Filesystem

- Filesystem operations: read/write/stat/list/remove.
- Command execution operations: execute process and collect output.

### 4) Forwarding and Network Access

- Spotlight operations for exposing workspace service ports.
- Compose/default forward application flows.

### 5) Diagnostics and Readiness

- Capability checks and readiness polling.
- Runtime state introspection and service health checks.

## Components

### Server (`cmd/daemon/`)

- Main entry point for the daemon
- WebSocket server handling RPC calls

### Handlers (`pkg/`)

- File system handlers
- Command execution handlers

## RPC Methods

| Method | Description |
|--------|-------------|
| `workspace.create` | Create isolated remote workspace |
| `workspace.list` | List workspace records |
| `workspace.open` | Open workspace by id |
| `workspace.start` | Start workspace compute and mark running |
| `workspace.pause` | Pause a running workspace VM |
| `workspace.resume` | Resume a paused workspace VM |
| `workspace.stop` | Stop compute, persist workspace state |
| `workspace.restore` | Restore persisted workspace to running state |
| `workspace.fork` | Fork a workspace into a child workspace |
| `workspace.remove` | Remove workspace by id |
| `workspace.info` | Get workspace info |
| `fs.readFile` | Read file contents |
| `fs.writeFile` | Write file contents |
| `fs.mkdir` | Create directory |
| `fs.readdir` | List directory |
| `fs.exists` | Check path exists |
| `fs.stat` | Get file stats |
| `fs.rm` | Remove file/directory |
| `exec` | Execute command |
| `git.command` | Run scoped git action in workspace |
| `service.command` | Start/stop/restart/status/logs for workspace services |
| `spotlight.expose` | Expose remote service port locally (Spotlight-only ingress) |
| `spotlight.list` | List active Spotlight forwards |
| `spotlight.close` | Close Spotlight forward |
| `spotlight.applyDefaults` | Apply project spotlight defaults from `.nexus/workspace.json` |
| `spotlight.applyComposePorts` | Auto-forward all docker-compose published ports |
| `workspace.ready` | Poll readiness checks until success/timeout |
| `capabilities.list` | List available runtime and toolchain capabilities |
| `authrelay.mint` | Mint one-time auth relay token for exec injection |
| `authrelay.revoke` | Revoke auth relay token |
