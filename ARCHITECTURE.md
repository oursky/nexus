# Nexus Architecture

## Table of Contents

1. [System Overview](#system-overview)
2. [Components](#components)
3. [Request Flow](#request-flow)
4. [Layer Architecture](#layer-architecture)
5. [VM Architecture](#vm-architecture)
6. [Workspace Lifecycle](#workspace-lifecycle)
7. [Auth & Security](#auth--security)
8. [Package Index](#package-index)

---

## System Overview

Nexus is a remote workspace daemon that runs isolated development environments inside libkrun micro-VMs on a Linux host. Users connect via a local CLI or a macOS app over SSH-tunnelled WebSocket.

```mermaid
graph LR
    subgraph Client
        CLI["nexus CLI"]
        App["NexusApp (macOS)"]
    end

    subgraph "SSH Tunnel"
        ST["sshtunnel"]
    end

    subgraph "Daemon Host (Linux)"
        NL["transport.NetworkListener<br/>WebSocket + Bearer auth"]
        US["transport.Listener<br/>Unix socket"]
        Daemon["nexus daemon"]
        VM[("nexus-libkrun-vm<br/>per workspace")]
        GA[("nexus-guest-agent<br/>inside VM")]
    end

    CLI -->|"ws://localhost:<port>"| ST
    App -->|"ws://localhost:<port>"| ST
    ST -->|"SSH -L"| NL
    NL --> Daemon
    US --> Daemon
    Daemon -->|"spawn"| VM
    VM -->|"vsock"| GA
```

---

## Components

### Binaries

| Binary | Source | Role |
|--------|--------|------|
| `nexus` | `cmd/nexus/` | CLI + daemon. `daemon start` runs the server; all other subcommands are RPC clients. |
| `nexus-guest-agent` | `cmd/nexus-guest-agent/` | In-VM agent. JSON-RPC over vsock for exec, PTY, port-forwarding, mounts. |
| `nexus-libkrun-vm` | `cmd/nexus-libkrun-vm/` | CGO VMM wrapper. Spawned per workspace; links `libkrun.so` and becomes the VM monitor. |
| `schema` | `cmd/schema/` | JSON schema generator for RPC contracts. |

### Why the VMM is a separate binary

The main daemon is CGO-free. `libkrun.so` requires CGO and takeover semantics (`krun_start_enter` never returns). By spawning `nexus-libkrun-vm` as a child process, the daemon avoids linking `libkrun.so` directly.

```mermaid
graph TD
    Daemon["nexus daemon<br/>(Go, pure)"] -->|"writes VMSpec JSON"| Spec[("/tmp/nexus-vm-*.json")]
    Daemon -->|"exec"| VMM["nexus-libkrun-vm<br/>(CGO + libkrun.so)"]
    Spec --> VMM
    VMM -->|"krun_start_enter"| VM[("libkrun microVM")]
    VM -->|"vsock CID=2"| GA["nexus-guest-agent"]
    VMM -->|"passt fd"| Passt["passt process"]
```

---

## Request Flow

### CLI / App to Daemon

```mermaid
sequenceDiagram
    actor User
    participant CLI as nexus CLI / App
    participant Tunnel as SSH Tunnel
    participant NL as NetworkListener
    participant Reg as rpc/registry
    participant H as RPC Handler
    participant S as app Service
    participant I as infra

    User->>CLI: nexus workspace start
    CLI->>Tunnel: ssh -L <local>:127.0.0.1:<remote>
    CLI->>NL: WebSocket upgrade + Bearer token
    NL->>CLI: 101 Switching Protocols
    CLI->>NL: JSON-RPC: workspace.start {"id":"ws-1"}
    NL->>Reg: Dispatch("workspace.start", params)
    Reg->>H: invoke handler
    H->>S: service.Start(ctx, "ws-1")
    S->>I: store.Get, runtime.Start
    I-->>S: Workspace, Instance
    S-->>H: nil
    H-->>Reg: {"workspace": {...}}
    Reg-->>NL: result
    NL-->>CLI: JSON-RPC response
```

### Daemon to Guest Agent (VM Session)

```mermaid
sequenceDiagram
    participant Daemon as nexus daemon
    participant M as libkrun.Manager
    participant VMM as nexus-libkrun-vm
    participant GA as nexus-guest-agent
    participant PTY as guest shell

    Daemon->>M: Spawn(ctx, spec)
    M->>VMM: exec nexus-libkrun-vm --config=spec.json
    VMM->>GA: krun_start_enter (VM boots)
    GA->>GA: mount virtiofs + overlayfs
    GA->>GA: start sshd, dockerd

    Note over Daemon,PTY: Later: pty.create RPC
    Daemon->>M: vsock dial port 10789
    M->>GA: JSON-RPC: {"id":"1","type":"shell.open",...}
    GA->>PTY: pty.StartWithSize(bash)
    PTY-->>GA: stdout
    GA-->>Daemon: {"type":"chunk","stream":"stdout","data":"..."}
    Daemon-->>Client: WebSocket push: pty.data
```

---

## Layer Architecture

Dependency rule: **lower layers never import higher layers.**

```mermaid
graph BT
    domain["domain/"]
    infra["infra/"]
    app["app/"]
    rpc["rpc/"]
    transport["transport/"]
    daemon["daemon/"]
    cmd["cmd/nexus"]

    infra --> domain
    app --> domain
    rpc --> app
    transport --> rpc
    daemon --> infra
    daemon --> app
    daemon --> rpc
    daemon --> transport
    daemon --> identity
    cmd --> daemon
    cmd --> app
    cmd --> infra/cli
```

### Layer Responsibilities

| Layer | Responsibility | Import Rule |
|-------|----------------|-------------|
| `domain/` | Entities, state machines, repository interfaces, sentinel errors | Zero internal imports |
| `infra/` | Repository implementations, DB, filesystem, VM runtime drivers | `domain/` only |
| `app/` | Use-case orchestration; multi-step workflows | `domain/` interfaces only |
| `rpc/` | Transport adapters; JSON-RPC deserialization/serialization | `app/` via narrow interfaces |
| `transport/` | Socket listeners, WebSocket upgrade, push notifications | `rpc/registry` only |
| `daemon/` | Composition root; constructs and wires all layers | All layers |

---

## VM Architecture

### Per-Workspace Process Tree

```mermaid
graph TD
    Daemon["nexus daemon"] -->|"fork+exec"| VMM["nexus-libkrun-vm<br/>--config spec.json"]
    Daemon -->|"fork+exec"| Passt["passt<br/>--fd 3 --foreground"]

    VMM -->|"krun_start_enter"| VM[("libkrun microVM")]
    VM -->|"virtiofs"| HostFS["host project dir<br/>(read-only lower)"]
    VM -->|"virtio-blk"| Rootfs["rootfs.ext4<br/>(reflink clone, rw)"]
    VM -->|"virtio-blk"| WsImg["workspace.ext4<br/>(overlay upper, rw)"]
    VM -->|"virtio-blk"| DockerImg["docker-data.ext4<br/>(sparse 50GiB)"]
    VM -->|"virtio-blk"| ConfigImg["hostconfig.ext4<br/>(dotfiles, creds)"]

    VM -->|"vsock:10789"| GA["nexus-guest-agent"]
    GA -->|"mount"| Overlay["overlayfs<br/>lower+upper= /workspace"]
    GA -->|"start"| SSHD["sshd"]
    GA -->|"start"| Docker["dockerd"]
    GA -->|"proxy"| SF["spotlight forwards<br/>(vsock:10792)"]
```

### Disk Layout (Hybrid Mode)

```mermaid
graph LR
    subgraph "Host Filesystem"
        Base["base.ext4<br/>(cached per repo)"]
        RootBase["rootfs.ext4<br/>(baked base image)"]
    end

    subgraph "Per-Workspace Directory"
        Root["rootfs.ext4<br/>(reflink clone)"]
        Ws["workspace.ext4<br/>(reflink clone from base)"]
        Dock["docker-data.ext4<br/>(sparse)"]
        Snap["snapshots/<br/>(fork images)"]
    end

    Base -->|"cp --reflink=always"| Ws
    RootBase -->|"cp --reflink=always"| Root
```

### Overlayfs Assembly Inside the Guest

```mermaid
graph TB
    Virtiofs["virtiofs nexus-workspace<br/>host project dir<br/>(read-only)"] -->|"lowerdir"| Overlay["overlayfs /workspace"]
    Block["/dev/vdb workspace.ext4<br/>(read-write)"] -->|"upperdir + workdir"| Overlay
    Overlay -->|"merged view"| Shell["bash /workspace"]
```

### Base Image → Workspace Image Flow

```mermaid
sequenceDiagram
    participant S as Manager.Spawn
    participant E as EnsureBaseImage
    participant B as buildBaseImage
    participant C as copyFile

    S->>E: repoRoot, basesDir, manifestHash
    E->>E: compute cache key<br/>SHA256(repoRoot + manifestHash + version)
    alt cache miss
        E->>B: repoRoot, imagePath
        B->>B: compute size = 2×projectSize + overhead<br/>clamp 2–20 GiB
        B->>B: mkfs.ext4 -F -d repoRoot imagePath
        E->>E: store in basesDir/<key>/base.ext4
    else cache hit
        E-->>S: return cached path
    end
    S->>C: cp --reflink=always base.ext4 workspace.ext4
    C-->>S: O(1) CoW clone
```

### Baking Flow

```mermaid
sequenceDiagram
    participant M as libkrun.Manager
    participant Bake as BakeRootfsIfNeeded
    participant VMM as nexus-libkrun-vm
    participant GA as nexus-guest-agent

    Bake->>Bake: check stamps<br/>host: ~/.local/state/nexus/rootfs-baked-v7<br/>image: /var/lib/nexus-tools-base-v7
    alt stamps match
        Bake-->>M: skip
    else bake required
        Bake->>Bake: reflink clone rootfs
        Bake->>Bake: inject guest agent via debugfs
        Bake->>Bake: create workspace + docker ext4
        Bake->>Bake: start passt for internet
        Bake->>VMM: spawn with nexus.bake=1
        VMM->>GA: boot VM
        GA->>GA: apt-get install docker nodejs ...
        GA->>GA: npm install -g opencode codex claude
        GA->>GA: sync && poweroff
        Bake->>Bake: poll hvc0 for "agent bake: all tools installed"
        Bake->>Bake: e2fsck -f -y baked.rootfs
        Bake->>Bake: write stamp via debugfs
        Bake->>Bake: atomic replace base rootfs
    end
```

### Networking

```mermaid
graph LR
    subgraph Host
        Daemon["nexus daemon"]
        Passt["passt process"]
        SSH["ssh -L ..."]
    end

    subgraph "libkrun VM"
        GA["nexus-guest-agent"]
        GuestSSH["sshd :22"]
        GuestSvc["service :8080"]
    end

    Daemon -->|"AF_UNIX socketpair<br/>fd→passt, fd→libkrun"| Passt
    Passt -->|"virtio-net / TSI"| GA
    GA -->|"port 10792<br/>spotlight forward"| GuestSvc
    SSH -->|"127.0.0.1:<random>→:22"| GuestSSH
```

**MAC and IP assignment:** deterministically derived from `workspaceID` via FNV-1a hash. Guest IPv4 lives in the gateway's `/16` subnet.

---

## Workspace Lifecycle

### State Machine

```mermaid
stateDiagram-v2
    [*] --> created: Create
    created --> starting: Start
    starting --> running: success
    starting --> created: failure / rollback
    running --> stopped: Stop
    running --> paused: Pause
    paused --> running: Resume
    paused --> stopped: Stop
    stopped --> starting: Start
    stopped --> running: Start (fast path)
    stopped --> restored: Restore
    restored --> running: Start
    created --> removed: Remove
    stopped --> removed: Remove
    restored --> removed: Remove
    running --> removed: Remove (invalid — guard missing)
```

States: `created` → `starting` → `running` → `paused` → `stopped` → `restored` → `removed`

**Note:** `paused` is defined in the enum but has no RPC handlers yet; transitions to/from `paused` are unreachable.

### Fork vs Restore

```mermaid
graph LR
    subgraph Fork
        P["parent workspace"]
        C["child workspace<br/>(new ID)"]
        P -->|"copy images<br/>CoW reflink"| C
    end

    subgraph Restore
        W["workspace<br/>(same ID)"]
        Snap["snapshot images"]
        Snap -->|"re-point runtime"| W
    end
```

| | Fork | Restore |
|---|---|---|
| Record | **New** child ID | **Same** workspace ID |
| Lineage | Sets `ParentWorkspaceID` | No change |
| Images | Copies to `.snapshots/<childID>.*.ext4` | Reuses existing snapshot |
| Use case | Branch a new workspace | Resume from saved state |

### Driver-Specific Fork Behaviour

**libkrun driver:**
1. Stop parent VM briefly (`CheckpointFork`) for filesystem consistency.
2. `cp --reflink=always` parent workspace + docker-data images to child snapshot paths.
3. Restart parent VM.

**sandbox driver:**
1. `git worktree add <childPath> <ref>`.
2. `git diff HEAD | git apply --3way` to replay parent's uncommitted changes.

---

## Auth & Security

### Transport Security Model

```mermaid
graph LR
    subgraph "Unix Socket"
        US["transport.Listener<br/>~/.local/state/nexus/nexusd.sock"]
        US -->|"no auth<br/>filesystem ACL only"| Daemon
    end

    subgraph "Network"
        NL["transport.NetworkListener<br/>TCP + WebSocket"]
        NL -->|"Bearer <token><br/>constant-time compare"| Daemon
    end
```

| Transport | Authentication | Notes |
|---|---|---|
| Unix socket | **None** | Any process with socket access can call any RPC |
| WebSocket / TCP | Static Bearer token | Auto-generated at daemon start; compared via `subtle.ConstantTimeCompare` |

**Caveat:** `internal/identity/` and `LocalTokenProvider` (JWT validation) exist but are **not yet wired** into the active transport path.

### Token Storage (Client-Side)

```mermaid
graph LR
    CLI["nexus CLI"] -->|"store token"| TS["auth/tokenstore"]
    TS -->|"macOS"| KC["Keychain<br/>/usr/bin/security"]
    TS -->|"Linux + D-Bus"| SS["SecretService<br/>GNOME Keyring"]
    TS -->|"Linux headless"| FS["FileStore<br/>~/.config/nexus/daemon-token"]
```

---

## Package Index

### Core Application (`internal/`)

| Package | Description |
|---------|-------------|
| `app/pty` | PTY session registry and in-process management |
| `app/spotlight` | Port-forward lifecycle orchestration |
| `app/workspace` | Workspace lifecycle: create, start, stop, fork, restore, delete |
| `domain/project` | Project entity, repository interface |
| `domain/runtime` | `Driver` interface for VM/sandbox backends |
| `domain/spotlight` | Forward entity, repository interface |
| `domain/workspace` | Workspace entity, state machine, repository interface |
| `infra/store` | SQLite persistence; implements all domain repository interfaces |
| `infra/store/migrations` | Goose migration files |
| `infra/fsworkspace` | Filesystem operations for workspace directories on daemon host |
| `infra/config` | Nexusfile config parsing |
| `infra/dockercompose` | Docker Compose port discovery |
| `infra/hostpaths` | XDG base directory helpers |
| `infra/runtime/libkrun` | libkrun microVM adapter: VM lifecycle, baking, image management |
| `infra/runtime/sandbox` | Process-isolation fallback backend |
| `infra/runtime/toolchain` | Guest toolchain readiness probe (codex/opencode/claude) |
| `infra/secrets/inject` | Secrets injection into workspace environments |
| `rpc/workspace` | Workspace lifecycle RPC handlers |
| `rpc/project` | Project CRUD RPC handlers |
| `rpc/spotlight` | Spotlight + `workspace.ports.*` handlers |
| `rpc/pty` | PTY session handlers |
| `rpc/daemon` | `node.info`, `daemon.log.tail` |
| `rpc/fs` | Filesystem RPC handlers |
| `rpc/auth` | `authrelay.mint`, `authrelay.revoke` |
| `rpc/registry` | `MapRegistry` — flat method dispatch table |
| `rpc/errors` | JSON-RPC error types |
| `transport` | Unix socket + TCP/WebSocket/TLS listeners, push notifications |
| `daemon` | Composition root — constructs and wires all layers |

### Cross-Cutting

| Package | Description |
|---------|-------------|
| `identity` | Authentication principal; `Provider` interface; `LocalTokenProvider` |
| `auth/tokenstore` | Secure token storage: Keychain / SecretService / file fallback |
| `creds/agentprofile` | Agent profile credentials |
| `creds/bundle` | Credential bundling for `workspace.create` |
| `creds/inject` | Credential injection into guest environments |
| `creds/relay` | Auth relay broker for short-lived workspace tokens |
| `tunnel` | Daemon-side SSH tunnel manager (raw `ssh` process) |

### CLI-Only (`internal/infra/cli/`)

| Package | Description |
|---------|-------------|
| `cli/daemonclient` | Auto-start local daemon; healthz polling |
| `cli/profile` | Daemon connection profiles; secure token storage integration |
| `cli/sshtunnel` | Client-side SSH tunnel manager (`ssh -fNL`) |
| `cli/mutagenbin` | Legacy Mutagen binaries (retained for build compatibility) |

### Build / Metadata

| Package | Description |
|---------|-------------|
| `build` | Build metadata via ldflags (legacy — consolidate into buildinfo) |
| `buildinfo` | Build metadata via ldflags (version, commit, time) |
| `profile` | Profile management (legacy — consolidate into cli/profile) |

### Binaries (`cmd/`)

| Package | Description |
|---------|-------------|
| `nexus` | CLI + daemon entrypoint |
| `nexus/commands/daemon` | Daemon start/stop/status CLI |
| `nexus/commands/project` | Project CLI commands |
| `nexus/commands/spotlight` | Spotlight CLI commands |
| `nexus/commands/workspace` | Workspace CLI commands |
| `nexus/commands/rpc` | RPC client helpers: `MuxConn`, `EnsureDaemon`, `Do` |
| `nexus/commands/libkrunvm` | Hidden libkrun-vm command (superseded by standalone binary) |
| `nexus-guest-agent` | In-VM guest agent (Linux only) |
| `nexus-libkrun-vm` | Standalone libkrun VM helper (CGO) |
| `schema` | JSON schema generator |
