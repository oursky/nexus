---
name: nexus-dev
description: Build, run, and test the Nexus Go CLI and daemon (local Linux) and the macOS app (NexusApp.app). This skill should be used when developing packages/nexus or packages/nexus-swift, regenerating the Swift RPC, using repo Taskfile workflows, or working with the local Linux daemon plus Mac CLI flow.
---

# Nexus development (CLI + macOS app)

> **You are on the Linux host.** The canonical dev environment is linux-first: you develop on this Linux machine (engine-03 / linuxbox) and SSH *to* the Mac (`newman@minion`) only for building the macOS app. All daemon builds, deployments, and tests happen locally on this host.

## Quick start (local deploy)

```bash
# Build and install locally, then restart the daemon
task dev:local

# Or manually:
cd packages/nexus
go build -tags dev -o ~/.local/bin/nexus-dev ./cmd/nexus
~/.local/bin/nexus-dev daemon start --port 7778 --driver sandbox

# Use named instances for isolation
task dev:daemon:start NAME=test1 PORT=7780 DRIVER=sandbox
```

## Taskfile (repo root)

Set `MAC_HOST` in `.env.local` (see `.env.local.example`) for Mac app builds. Core tasks:


| Task                                   | What it does                                                                       |
| -------------------------------------- | ---------------------------------------------------------------------------------- |
| `task setup`                           | Prerequisites check + `go mod download`                                            |
| `task dev:local`                       | Build locally + restart dev daemon on this Linux host                              |
| `task dev:cli`                         | Build and install local CLI binary to `~/.local/bin/nexus-dev`                     |
| `task dev:swift`                       | `dev:local` + stage resources + sync to Mac + build + open NexusApp                |
| `task generate:sdk`                    | Regenerate `NexusRPC.swift` from Go types                                          |
| `task build` / `task test` / `task ci` | Local Go compile, tests, CI-shaped checks                                          |


Local scripts: `scripts/local/deploy.sh`, `scripts/local/daemon-restart.sh`, `scripts/local/daemon-named.sh`.

## Dev/prod isolation

Dev and prod daemons are fully separated by binary name, port, state directory, and data directory so they never collide on the same host.

| Resource | Prod | Dev (default) | Named instance |
|---|---|---|---|
| Binary | `~/.local/bin/nexus` | `~/.local/bin/nexus-dev` | same as dev binary |
| Port | `7777` | `7778` | `7780+` (auto or explicit) |
| State dir | `~/.local/state/nexus/` | `~/.local/state-dev/nexus/` | `~/.local/state-nexus-<name>/nexus/` |
| Data dir (XDG_DATA_HOME) | `~/.local/share/nexus/` | `~/.local/share-dev/nexus/` | `~/.local/share/nexus-<name>/` |
| VM workdir (libkrun images) | `/data/nexus/libkrun-vms` | `/data/nexus/nexus-dev` | `~/.local/share/nexus-<name>/libkrun-vms` |
| Unix socket | `~/.local/state/nexus/nexusd.sock` | `~/.local/state-dev/nexus/nexusd.sock` | `~/.local/state-nexus-<name>/nexus/nexusd.sock` |
| Local CLI | `~/.local/bin/nexus` | `~/.local/bin/nexus-dev` | same as dev binary |
| Mac app profile | connects to prod daemon | connects to dev daemon (separate `nexus-dev daemon connect`) | separate profile per port |

> **Known gap — macOS Keychain token**: both `nexus` and `nexus-dev` write to the same Keychain entry (`service="nexus"`, `account="daemon-token"`). If both run on the same Mac simultaneously, the last `daemon connect` call wins. Workaround: avoid running prod and dev Mac daemons concurrently on the same Mac. A Swift fix to scope by binary name is tracked but not yet shipped.

Configure in `.env.local` (copy from `.env.local.example`). Defaults are already set to dev values — no file needed to start dev work. Prod daemon is never touched by any `task dev:*` command.

### Mac app: local dev build vs TestFlight prod

- **Local dev build** (`task dev:swift`): built from source, runs as `NexusApp.app` from Xcode. Connect it to the dev daemon: `nexus-dev daemon connect <host>`. This stores a separate Keychain profile.
- **TestFlight prod app**: uses the prod daemon at port `7777`. Connect via `nexus daemon connect <host>`.

The two apps use different Keychain entries because the CLI binary name differs (`nexus` vs `nexus-dev`). Run both simultaneously — they target different daemons on different ports with different state dirs.

To confirm which daemon a running app is connected to, check the active profile:
```bash
nexus-dev daemon status   # dev daemon status
nexus daemon status       # prod daemon status
```

## Key paths


| Thing                               | Path                                                                                                     |
| ----------------------------------- | -------------------------------------------------------------------------------------------------------- |
| Swift package                       | `packages/nexus-swift/`                                                                                  |
| Go CLI + daemon commands            | `packages/nexus/cmd/nexus/`                                                                              |
| Daemon bundled into Xcode app       | `packages/nexus-swift/Resources/nexus-daemon` (copied at build; stage with `go build`, see CONTRIBUTING) |
| Use repo-built daemon without embed | `NEXUS_USE_SOURCE_DAEMON=1` in the app environment (not default)                                         |
| Built app bundle (Xcode)            | `packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app`                                  |
| Daemon token (macOS)                | Keychain — service `nexus`, account `daemon-token`                                                       |
| Daemon token (Linux headless)       | `~/.local/share/nexus/daemon-token` (0600, fallback only)                                                |
| Daemon port (local Mac)             | default `63987`; process-isolation worktrees use `64100-64999`                                           |
| Daemon port (Linux dev)             | `7778` by convention (dev); prod uses `7777`                                                             |
| Daemon log (local Mac)              | `~/.config/nexus/run/daemon.log`                                                                         |
| Daemon log (Linux dev)              | `~/.local/state-dev/nexus/daemon.log`                                                                    |
| Client workspace state              | `~/.local/share/nexus/workspaces.json`                                                                   |
| Fork worktrees                      | `<gitRoot>/.worktrees/<name>/`                                                                           |
| Headless RPC — Debug build          | `127.0.0.1:7778` — activate with `touch ~/.nexus-headless-rpc`                                           |
| Headless RPC — Release/TestFlight   | `127.0.0.1:7779` — same sentinel; port set via `#if DEBUG` in `HeadlessRPCServer.swift`                  |


---

## Dev flow (Linux-first)

The canonical dev flow is **Linux-first**: you develop on this Linux machine and SSH *back* to the Mac (`newman@minion`) to build and test the app.

### Architecture

```
Linux host (you are here)
  ├─ nexus daemon(s) running with process sandbox
  │    port 7778, 7780, 7781, … — one per test scenario
  │    state: ~/.local/state-dev/nexus/ or ~/.local/state-nexus-<name>/nexus/
  ├─ source code at ~/magic/nexus  (inside a nexus workspace)
  └─ rsync → Mac  (scripts/workspace-sync.sh)
                   ↓
Mac (newman@minion)
  ├─ ~/magic/nexus  (receives rsync'd source)
  ├─ builds NexusApp via scripts/swift/build.sh
  └─ headless RPC :7778 tunnelled back to Linux for testing
```

### Setup (one-time)

Add to `.env.local` on the Linux host:

```bash
MAC_HOST=newman@minion
MAC_REPO_ROOT=~/magic/nexus
```

Ensure the Linux → Mac SSH key is set up:

```bash
ssh newman@minion echo ok
```

### Day-to-day dev loop

```bash
# 1. Build local CLI, restart daemon, and stage binaries
task dev:local
task stage:nexus-macos    # cross-compile darwin/arm64 nexus CLI
task stage:nexus-linux    # cross-compile linux/amd64 embedded binary
scripts/generate-sdk.sh   # regenerate NexusRPC.swift

# 2. Rsync repo to Mac + build + open app (all in one)
task dev:swift

# 3. Verify headless RPC is active on Mac
task dev:mac:test-headless
```

### Volume sync (workspace → Mac)

When you edit source inside a nexus process-sandbox workspace, sync it to the Mac before building:

```bash
# One-shot sync
WORKSPACE_PATH=~/.local/state-nexus-test1/nexus/workspaces/ws-xxx/workspace \
  task dev:workspace:sync

# Watch mode (re-syncs on every file change using inotifywait or 3s polling)
WORKSPACE_PATH=... WATCH=true task dev:workspace:sync

# Or call the script directly
MAC_HOST=newman@minion WORKSPACE_PATH=/path/to/workspace \
  scripts/workspace-sync.sh --watch
```

### Multiple named daemon instances

Dogfood by running multiple isolated nexus daemons — each with its own port, state dir, and data dir:

```bash
# Start instances (ports auto-assigned from 7780+)
task dev:daemon:start NAME=test1                    # → port 7780, driver=sandbox
task dev:daemon:start NAME=test2 PORT=7781          # explicit port
task dev:daemon:start NAME=libkrun1 DRIVER=libkrun  # libkrun driver

# List all instances
task dev:daemon:list

# Connect the Mac app to a specific instance
nexus-dev daemon connect <this-linux-host> --port 7780

# Stop
task dev:daemon:stop NAME=test1

# Or use the script directly for full options
scripts/local/daemon-named.sh start  mytest --port 7790 --driver sandbox
scripts/local/daemon-named.sh status mytest
scripts/local/daemon-named.sh logs   mytest --follow
scripts/local/daemon-named.sh list
scripts/local/daemon-named.sh stop   mytest
```

**Isolation per named instance** (`--name foo`):

| Resource | Path |
|---|---|
| State dir | `~/.local/state-nexus-<name>/nexus/` |
| Data dir  | `~/.local/share/nexus-<name>/` |
| Socket    | `~/.local/state-nexus-<name>/nexus/nexusd.sock` |
| Port      | user-specified or auto-assigned from 7780+ |

### Connect Mac app to a Linux daemon via headless RPC tunnel

To interact with the Mac app from Linux via the headless RPC, tunnel through SSH:

```bash
# Mac headless RPC is at 127.0.0.1:7778 on the Mac
# Forward it to Linux's 127.0.0.1:17778
ssh -fN -L 17778:127.0.0.1:7778 newman@minion

curl -sf http://127.0.0.1:17778/status
# Use LOCAL_TUNNEL_PORT env var to control the local port:
LOCAL_TUNNEL_PORT=17778 MAC_HOST=newman@minion scripts/remote/mac-test-headless.sh
```

---

## Nexus-in-Nexus Development

Develop nexus from inside a workspace (process sandbox or VM). The daemon runs on the Linux host; you work inside the workspace with sync keeping `/workspace` in sync with the host path.

### Setup

Create a workspace pointing at the nexus repo, start it, and open a terminal:

```bash
nexus-dev workspace create --name nexus-dev --repo ~/magic/nexus
nexus-dev workspace start ws-xxx
```

### Sync workflow

Use `workspace.sync-start` to sync code between host and workspace:

```bash
nexus-dev sync start ws-xxx --local-path ~/magic/nexus
nexus-dev sync status ws-xxx
nexus-dev sync pause ws-xxx
nexus-dev sync resume ws-xxx
nexus-dev sync stop ws-xxx
```

### Building inside the workspace

Build, test, and run the daemon from inside the workspace:

```bash
cd /workspace/packages/nexus
go build ./...
go test ./...
```

### Agent integration

opencode and codex are installed via the config drive. Invoke them inside the workspace:

```bash
cd /workspace
opencode  # starts interactive session
opencode run 'fix the bug in service.go'  # non-interactive
```

### Limitations

- Docker inside process sandbox: not supported
- Nested VMs in libkrun: not supported

---
## Common workflows

### Local daemon + Mac CLI + app (primary loop)

```bash
# Full Swift path: build locally, restart daemon, regenerate RPC, Xcode build, open app
task dev:swift
```

CLI-only after daemon changes:

```bash
task dev:cli
```

Build and restart local daemon only:

```bash
task dev:local
```

### macOS app resources

The Xcode project copies `Resources/nexus` and Linux helper binaries (`Resources/nexus-linux-amd64`, `Resources/nexus-linux-arm64`) into the app bundle.

Stage the macOS CLI binary with:

```bash
go build -C packages/nexus -o packages/nexus-swift/Resources/nexus ./cmd/nexus
```

For Linux staging, prefer:

```bash
scripts/swift/stage-linux-nexus.sh amd64   # or arm64 / both
```

This script builds/stages the embedded guest-agent artifact first, avoiding `pattern agent-linux-amd64: no matching files found`.

Then build via `task dev:swift` or `scripts/swift/build.sh` + `scripts/swift/open.sh`.

### Restart or inspect local daemon

```bash
# Restart
task dev:local

# Status
~/.local/bin/nexus-dev daemon status

# Logs
tail -f ~/.local/state-dev/nexus/daemon.log

# Stop
~/.local/bin/nexus-dev daemon stop
```

### Dogfood process isolation in parallel repos/worktrees

Use when validating repos with `isolation.level: "process"`.

```bash
PATH="$HOME/.local/bin:$PATH" nexus daemon start
PATH="$HOME/.local/bin:$PATH" nexus daemon status --json
PATH="$HOME/.local/bin:$PATH" nexus sandbox create --repo "$PWD" --fresh
PATH="$HOME/.local/bin:$PATH" nexus sandbox start <workspace-id>
PATH="$HOME/.local/bin:$PATH" nexus sandbox exec <workspace-id> -- docker compose ps
```

For local process-sandbox dev, use the default profile (`nexus daemon connect` / stored profile) so the app picks up SSH + tunnel + token. The app does not use `NEXUS_DAEMON_URL` for routing.

### If the app is stuck at "Connecting..."

```bash
# Check daemon status
~/.local/bin/nexus-dev daemon status
pkill -x NexusApp 2>/dev/null || true
task dev:local    # or dev:swift if you also need a fresh app build + RPC
scripts/swift/open.sh
```

Tail logs while reproducing:

```bash
tail -f ~/.local/state-dev/nexus/daemon.log
```

If you see `"Method not found"`, the Mac client is newer than the daemon — run `task dev:local` (or `task dev:swift`).

---

## Testing the Mac app — Headless RPC (MANDATORY)

> **RULE: NEVER use screenshots, screen coordinates, or UI automation (AppleScript, Accessibility APIs, browser-use) to test the Mac app. These methods are brittle and unreliable. Always use the Headless RPC server.**

### How it works

`HeadlessRPCServer` is a local HTTP server built into the Mac app. The port depends on the build variant (`#if DEBUG` in `HeadlessRPCServer.swift`):

| Build | Port |
|-------|------|
| Debug (Xcode / `scripts/swift/open.sh`) | `127.0.0.1:7778` |
| Release (TestFlight / prod) | `127.0.0.1:7779` |

It is activated by a sentinel file:

```bash
touch ~/.nexus-headless-rpc   # persists across app restarts; remove to disable
```

The app checks for this file on startup. If already running, restart:

```bash
pkill -x NexusApp; scripts/swift/open.sh
```

Verify it's active (Debug build):

```bash
curl -sf http://127.0.0.1:7778/status   # → {"ok":true,"version":"1"}
# For Release/TestFlight:
# curl -sf http://127.0.0.1:7779/status
```

### Endpoints

| Method | Path | Body / Query | Purpose |
|--------|------|-------------|---------|
| `GET` | `/status` | — | Liveness check |
| `GET` | `/terminal/tabs` | — | List all open terminal tabs |
| `POST` | `/terminal/open` | `{"workspaceID":"ws-…","name":"optional"}` | Open a new terminal tab for a workspace |
| `POST` | `/terminal/write` | `{"tabID":"pty-…","text":"cmd\n"}` | Send text to a tab (include `\n` to submit) |
| `GET` | `/terminal/read` | `?tabID=pty-…` | Drain buffered output since last read |
| `POST` | `/terminal/clear` | `{"tabID":"pty-…"}` | Clear buffered output |

### Standard test pattern

```bash
# 1. Enable headless RPC (once)
touch ~/.nexus-headless-rpc

# 2. Workspace management via Mac CLI (talks directly to local daemon)
/Users/newman/.local/bin/nexus-dev workspace create --name myws --repo /home/user/magic/my-project
/Users/newman/.local/bin/nexus-dev workspace start ws-<id>
/Users/newman/.local/bin/nexus-dev workspace list

# 3. Open a terminal tab in the Mac app
TAB=$(curl -sf -X POST http://127.0.0.1:7778/terminal/open \
  -H "Content-Type: application/json" \
  -d '{"workspaceID":"ws-<id>","name":"myws"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['tabID'])")

# 4. Run commands and read output
curl -sf -X POST http://127.0.0.1:7778/terminal/write \
  -H "Content-Type: application/json" \
  -d "{\"tabID\":\"$TAB\",\"text\":\"whoami && pwd && docker --version\n\"}"
sleep 2
curl -sf "http://127.0.0.1:7778/terminal/read?tabID=$TAB" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['output'])"
```

### Startup diagnostics learned in practice

- Use `GET /workspace/list` as the source of truth for workspace lifecycle state (`created|starting|running|...`).
- Do **not** use `GET /workspace/info` for state polling; it may only return connection metadata (`workspacePath`, `ports`) and no `state`.
- During heavy startup (first run after bake/stamp changes), `/workspace/list` can intermittently return empty/non-JSON responses. Poll with retries.
- `POST /terminal/open` can return `{"error":"tab creation failed"}` briefly right after state flips to `running`; add a short settle (`sleep 2-3`) and retry.
- Prefer:
  1) wait for `running` from `/workspace/list`
  2) sleep 2-3 seconds
  3) retry `/terminal/open` up to ~8 times with 1-2s backoff

### Shell helper for tests

```bash
rpc_exec() {
  local tab="$1" cmd="$2" delay="${3:-2}"
  curl -sf -X POST http://127.0.0.1:7778/terminal/write \
    -H "Content-Type: application/json" \
    -d "{\"tabID\":\"$tab\",\"text\":\"$cmd\n\"}" > /dev/null
  sleep "$delay"
  curl -sf "http://127.0.0.1:7778/terminal/read?tabID=$tab" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['output'])"
}

# Usage:
rpc_exec "$TAB" "git status --short" 2
rpc_exec "$TAB" "docker compose ps" 3
```

### What to assert

- **Terminal opens**: `tabID` is returned and output contains a shell prompt (`#` or `$`)
- **Workspace is mounted**: `ls /workspace` shows project files
- **PTY gate**: opening a terminal on an un-started workspace returns `{"error":"workspace … is not ready (state: created)…"}`
- **Fork isolation**: file written in parent workspace is invisible in fork workspace

### Key paths

| Thing | Value |
|---|---|
| Headless RPC port (Debug) | `127.0.0.1:7778` |
| Headless RPC port (Release/TestFlight) | `127.0.0.1:7779` |
| Sentinel file | `~/.nexus-headless-rpc` |
| Implementation | `packages/nexus-swift/Sources/NexusCore/HeadlessRPCServer.swift` |
| Terminal registry | `packages/nexus-swift/Sources/NexusCore/TerminalRegistry.swift` |

### Unit tests

```bash
cd packages/nexus-swift && swift test --filter NexusAppTests
```

---

## Build + deploy cycle

**Always use `task dev:local` or `task dev:swift`.** They handle the correct build order automatically.

```bash
task dev:local        # build agent → embed → build nexus → install → restart daemon
task dev:swift        # same + stage resources + sync to Mac + build + open app
```

**If doing it manually** (rare): the guest agent is a Linux binary embedded in the nexus binary via `//go:embed agent-linux-amd64`. You must compile the agent first, then compile nexus:

```bash
cd packages/nexus

# Step 1 — compile agent for linux/amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o cmd/nexus/agent-linux-amd64 ./cmd/nexus-firecracker-agent/

# Step 2 — compile nexus (embeds agent-linux-amd64 automatically)
go build -tags dev -o ~/.local/bin/nexus-dev ./cmd/nexus

# Step 3 — restart daemon
~/.local/bin/nexus-dev daemon stop 2>/dev/null || true
~/.local/bin/nexus-dev daemon start --port 7778
```

**Common trap**: running only `go build ./cmd/nexus/` without re-compiling `cmd/nexus-firecracker-agent/` first embeds the old agent binary. The daemon will detect the size/hash difference and inject properly, but any new agent-side features will not be present in the VM. Always build both.

### Agent injection mechanics

On `nexus daemon start`, the daemon:
1. Extracts the embedded agent to `~/.local/bin/nexus-firecracker-agent` (SHA-256 compared, not size)
2. Compares the extracted agent's SHA-256 to `~/.local/state/nexus/rootfs-agent.sha256`
3. If different (or hash file missing), runs `debugfs` to inject the agent binary into `rootfs.ext4`
4. Saves the new hash

After a **fresh rootfs build** the hash file is deleted automatically so the agent is always re-injected. If the agent seems stale in the VM:

```bash
rm -f ~/.local/state-dev/nexus/rootfs-agent.sha256
# then restart daemon
```

### Agent injection mechanics — libkrun

libkrun uses a **separate** agent binary (`nexus-guest-agent`, not `nexus-firecracker-agent`) and injects it into the libkrun base rootfs. The mechanics are the same SHA-based skip, but the paths differ:

| | Firecracker | libkrun |
|---|---|---|
| Agent binary (extracted) | `~/.local/bin/nexus-firecracker-agent` | `~/.local/bin/nexus-guest-agent` |
| Hash file | `$XDG_STATE_HOME/nexus/rootfs-agent.sha256` | `$XDG_STATE_HOME/nexus/rootfs-agent.sha256` (same key) |
| Base rootfs injected into | `~/.local/share/nexus/vm/rootfs.ext4` | `/data/nexus/vm/rootfs.ext4` |
| Per-workspace copy | `/data/nexus/firecracker-vms/<ws-id>/rootfs.ext4` | `/data/nexus/libkrun-vms/<ws-id>/rootfs.ext4` |
| Bake stamp | n/a | `$XDG_STATE_HOME/nexus/rootfs-baked-v7` |

**Key difference**: the bake stamp (`rootfs-baked-v7`) controls tool installation and is checked separately from the agent hash. A new agent fix only requires deleting the agent hash file — the bake stamp does not need to be deleted unless tools changed.

**Verify injection after deploy**:
```bash
# Agent fix string present in base rootfs?
strings /data/nexus/vm/rootfs.ext4 | grep -c 'YOUR_FIX_STRING'
# Hash file updated?
cat ~/.local/state/nexus/rootfs-agent.sha256 | cut -c1-16
sha256sum ~/.local/bin/nexus-guest-agent | cut -c1-16
# Both should match
```

### Rootfs lifecycle

| File | Location | When rebuilt |
|------|----------|-------------|
| `ubuntu.squashfs` | `~/.local/share/nexus/vm/` | Once, downloaded on first provision |
| `rootfs.ext4` | `~/.local/share/nexus/vm/` | Only when missing; delete to force rebuild |
| `rootfs-agent.sha256` | `~/.local/state-dev/nexus/` | Deleted after rootfs rebuild; updated after agent injection |
| `workspace.ext4` | `/data/nexus/firecracker-vms/<ws-id>/` | XFS reflink of `base.ext4`, per workspace |
| `.nexus-host-config.ext4` | `<project-root>/` | Rebuilt fresh on every workspace `Start()` call |

**First boot** (no stamp): workspace stays in `starting` ~15–40s while `apt-get install` runs inside the VM. This is correct and intentional — the PTY listener only opens after packages are installed. Stamp written to `/var/lib/nexus-tools-installed-v3` in the rootfs.

**Subsequent boots** (stamp found): workspace reaches `running` in ~4s.

To force a clean rootfs rebuild:
```bash
rm -f ~/.local/share/nexus/vm/rootfs.ext4 ~/.local/state-dev/nexus/rootfs-agent.sha256
# then restart daemon
```

---

## Diagnostics reference

### Daemon health
```bash
# Is it running and what version?
~/.local/bin/nexus-dev daemon status

# Tail live logs
tail -f ~/.local/state-dev/nexus/daemon.log

# Full log path
ls -la ~/.local/state-dev/nexus/daemon.log
```

### VM / Firecracker
```bash
# Agent boot messages (non-connection noise filtered out)
grep -v 'accepted\|API server\|Kernel command\|Command line' \
  /data/nexus/firecracker-vms/<ws-id>/firecracker.log

# Is the VM process alive?
pgrep -a firecracker

# Rootfs and squashfs
ls -lh ~/.local/share/nexus/vm/

# Agent hash
cat ~/.local/state-dev/nexus/rootfs-agent.sha256 | cut -c1-16
sha256sum ~/.local/bin/nexus-firecracker-agent | cut -c1-16
# These must match for the currently-running agent to be up to date
```

### Inside a running VM (via headless RPC terminal)
```bash
TAB=$(curl -s -X POST http://127.0.0.1:7778/terminal/open \
  -H "Content-Type: application/json" \
  -d '{"workspaceID":"<ws-id>","name":"diag"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['tabID'])")

rpc_run() {
  curl -s -X POST http://127.0.0.1:7778/terminal/write \
    -H "Content-Type: application/json" \
    -d "{\"tabID\":\"$TAB\",\"text\":\"$1\n\"}" > /dev/null
  sleep "${2:-2}"
  curl -s "http://127.0.0.1:7778/terminal/read?tabID=$TAB" \
    | python3 -c "import sys,json,re; print(re.sub(r'\x1b\[[^a-zA-Z]*[a-zA-Z]|\[\?2004[hl]|\r','',json.load(sys.stdin).get('output',''))[-300:])"
}

rpc_run "df -h /"                           # disk usage
rpc_run "cat /var/lib/nexus-tools-installed-v3" # Firecracker stamp present?
rpc_run "cat /var/lib/nexus-tools-base-v7"      # libkrun stamp present?
rpc_run "cat /tmp/nexus-agent-dockerd.log"  # dockerd startup errors
rpc_run "ls /run/nexus-host/"               # config drive contents
rpc_run "env | grep -iE 'openai|anthropic|gemini' | sed 's/=.*/=***/' | head"
rpc_run "mount | grep -E '/workspace|/tmp'"
```

### Config drive inspection (from host)
```bash
debugfs -R 'ls -l /' /home/newman/magic/<project>/.nexus-host-config.ext4 2>/dev/null
```

### XFS data volume
```bash
df -h /data          # XFS volume health
xfs_info /data       # confirm XFS with reflink enabled
ls -lh /data/nexus/firecracker-vms/
```

---

## Debugging tips

**Daemon crashing silently** — `ps aux | grep nexus-daemon`. `UE`/`UNE` in `ps` often means codesign issues for embedded binaries; rebuild via Xcode and ensure `Resources/nexus-daemon` is staged.

**Local sandbox exec outside repo root** — if commands run from `~/.nexus/workspaces/instances/...`, check `pkg/server/pty/handler.go` (`localWorkDirForOpen`).

**Port still held after kill** — `lsof -i :7778` and `lsof -i :<local-driver-port>`.

**XCUITest hangs at activation** — avoid `switch` in `ViewBuilder` for activation; use `if` / `else` with `Group` on macOS.

**App not connecting** — WebSocket expects `Authorization: Bearer <token>`; query `?token=` is not supported. Token order: `NEXUS_DAEMON_TOKEN` env → macOS keychain (`nexus-daemon-token` by default).

**App not reaching daemon** — profile must have `sshTarget`; tunnel up; token matches (`~/.local/bin/nexus-dev daemon status`, `nexus-dev daemon connect <host>`).

**Repeated `401`** — `security find-generic-password -s nexus -a daemon-token -w`; re-run `nexus-dev daemon connect <host>` for remote.

`**nexus-dev daemon connect` exit 127** — ensure binary exists at `~/.local/bin/nexus-dev`.

**Port mismatch** — `nexus-dev daemon status --json` per repo/worktree when multiple daemons run.

---

## Common mistakes + how to avoid them

### 1. Forgetting to rebuild the agent before nexus
**Symptom**: new agent code (tools install, docker fix, TERM) not present in VM.
**Cause**: `go build ./cmd/nexus/` embeds whichever `agent-linux-amd64` binary was last compiled.
**Fix**: always use `task dev:local`, or manually run both build steps in order.

### 2. Stale agent hash file after rootfs rebuild
**Symptom**: `init /usr/local/bin/nexus-firecracker-agent failed (error -2)` in Firecracker log.
**Cause**: `rootfs.ext4` was deleted and rebuilt, but `rootfs-agent.sha256` still matches the agent binary — so `ensureFirecrackerGuestAgent` thinks the rootfs already has the agent.
**Fix**: the code now deletes the hash file after `buildRootlessRootfs`. If you ever see this, run:
```bash
rm -f ~/.local/state/nexus/rootfs-agent.sha256
```

### 3. Checking workspace state too early
**Symptom**: workspace reaches `running` but terminal immediately fails with "tab creation failed".
**Cause**: the Mac app's WebSocket connection may need a moment to re-handshake after the daemon boots.
**Fix**: add a `sleep 3` after state reaches `running` before opening a terminal.

### 4. Reading terminal output without stripping ANSI
**Symptom**: `grep` / string assertions fail even though the right output is there.
**Fix**: always pipe through the ANSI-stripping one-liner shown above.

### 5. `docker ps` failing with nftables error
**Symptom**: `iptables: Failed to initialize nft: Protocol not supported` in dockerd log.
**Cause**: Firecracker 5.10 kernel doesn't support nftables; Ubuntu 24.04's `iptables` is nftables-backed.
**Fix**: already handled — agent runs `update-alternatives --set iptables iptables-legacy` before starting dockerd. If you see this again, the agent wasn't re-injected.

### 6. Rootfs disk full (ENOSPC in apt-get)
**Symptom**: `df -h /` shows 100%; npm or apt errors with "no space left on device".
**Cause**: old rootfs was 4 GB; current is 8 GB. If you have an old rootfs:
```bash
rm -f ~/.local/share/nexus/vm/rootfs.ext4 ~/.local/state-dev/nexus/rootfs-agent.sha256
```
Then restart daemon to rebuild.

### 7. `Method not found` in Mac app
**Cause**: the Mac app's Swift RPC stubs are newer than the daemon.
**Fix**: `task dev:local` (or `task dev:swift` to also rebuild the app).

### 8. Workspace stuck at "starting" forever
**Symptom**: workspace never transitions to `running`.
**Triage**:
```bash
grep -v 'accepted connection' /data/nexus/firecracker-vms/<ws-id>/firecracker.log | tail -20
```
Look for:
- `failed (error -2)` → agent not in rootfs (stale hash, see #2)
- `apt-get install failed` → package issue; check full log
- `vsock connection refused` → VM booted but agent crashed

### 9. libkrun: agent fix not taking effect after deploy (hash file false-positive skip)
**Symptom**: deployed a new guest-agent with a fix, but the fix is not present inside the VM.
**Cause**: `ensureGuestAgent` compares the SHA-256 of the extracted `nexus-guest-agent` binary against `~/.local/state/nexus/rootfs-agent.sha256`. If both were written during the same deploy (agent extracted → hash recorded → rootfs NOT re-injected because hashes match on first compare), or the build was cached and produced the same binary SHA, the injection into `rootfs.ext4` is silently skipped.
**Verify**: `strings ~/.local/share/nexus/vm/rootfs.ext4 | grep -c 'YOUR_FIX_STRING'` — if `0`, the rootfs doesn't have the fix.
**Fix**: delete the hash file to force re-injection on the next daemon start:
```bash
rm -f ~/.local/state/nexus/rootfs-agent.sha256
# Stop + start daemon so ensureGuestAgent runs
~/.local/bin/nexus-dev daemon stop 2>/dev/null || true; sleep 2; ~/.local/bin/nexus-dev daemon start
# Confirm injection happened
strings ~/.local/share/nexus/vm/rootfs.ext4 | grep -c 'YOUR_FIX_STRING'
# Must return > 0
```
Note: per-workspace rootfs copies (`/data/nexus/libkrun-vms/<ws-id>/rootfs.ext4`) are made from the base at spawn time — existing running workspaces must be stopped and restarted to pick up the injected fix.
