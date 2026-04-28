---
name: nexus-dev
description: Build, run, and test the Nexus Go CLI and daemon (local and remote Linux) and the macOS app (NexusApp.app). This skill should be used when developing packages/nexus or packages/nexus-swift, regenerating the Swift RPC, using repo Taskfile workflows, or working with the remote Linux daemon plus Mac CLI flow.
---

# Nexus development (CLI + macOS app)

## Taskfile (repo root)

Set `REMOTE_HOST` in `.env.local` (see `.env.local.example`). Core tasks:


| Task                                   | What it does                                                                       |
| -------------------------------------- | ---------------------------------------------------------------------------------- |
| `task setup`                           | Prerequisites check + `go mod download`                                            |
| `task dev:remote`                      | `linux/amd64` build, deploy to `REMOTE_HOST`, restart daemon                       |
| `task dev:cli`                         | `dev:remote` + install Mac CLI to `~/.local/bin/nexus-dev`                         |
| `task dev:swift`                       | `dev:remote` + `generate:sdk` + `scripts/swift/build.sh` + `scripts/swift/open.sh` |
| `task generate:sdk`                    | Regenerate `NexusRPC.swift` from Go types                                          |
| `task build` / `task test` / `task ci` | Local Go compile, tests, CI-shaped checks                                          |


Remote scripts (same `REMOTE_HOST` / `REMOTE_BIN` as Taskfile): `scripts/remote/deploy.sh`, `daemon-restart.sh`, `daemon-status.sh`, `daemon-logs.sh`, `daemon-token.sh`.

## Dev/prod isolation

Dev and prod daemons are fully separated by binary name, port, and state directory so they never collide on the same remote host.

| Resource | Prod | Dev (default) |
|---|---|---|
| Remote binary | `~/.local/bin/nexus` | `~/.local/bin/nexus-dev` |
| Remote port | `7777` | `7778` |
| Remote state dir | `~/.local/state/nexus/` | `~/.local/state-dev/nexus/` |
| Local CLI | `~/.local/bin/nexus` | `~/.local/bin/nexus-dev` |
| Mac app profile | connects to prod daemon | connects to dev daemon (separate `nexus-dev daemon connect`) |

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
| Daemon port (remote Linux dev)      | `7778` by convention (dev); prod uses `7777`                                                             |
| Daemon log (local Mac)              | `~/.config/nexus/run/daemon.log`                                                                         |
| Daemon log (remote Linux dev)       | `${REMOTE_XDG_STATE_HOME:-~/.local/state-dev}/nexus/daemon.log`                                          |
| Client workspace state              | `~/.local/share/nexus/workspaces.json`                                                                   |
| Fork worktrees                      | `<gitRoot>/.worktrees/<name>/`                                                                           |
| Headless RPC (Mac app testing)      | `127.0.0.1:7778` — activate with `touch ~/.nexus-headless-rpc`                                           |


---

## Common workflows

### Remote daemon + Mac CLI + app (primary loop)

```bash
# Full Swift path: deploy, regenerate RPC, Xcode build, open app
task dev:swift
```

CLI-only after daemon changes:

```bash
task dev:cli
```

Deploy and restart remote daemon only:

```bash
task dev:remote
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

### Restart or inspect remote daemon (without editing Taskfile)

```bash
REMOTE_HOST=user@host scripts/remote/daemon-restart.sh
REMOTE_HOST=user@host scripts/remote/daemon-status.sh
REMOTE_HOST=user@host scripts/remote/daemon-logs.sh
```

### Dogfood process isolation in parallel repos/worktrees

Use when validating repos with `isolation.level: "process"`.

```bash
PATH="/tmp/nexus-dogfood-bin:$PATH" nexus daemon start
PATH="/tmp/nexus-dogfood-bin:$PATH" nexus daemon status --json
PATH="/tmp/nexus-dogfood-bin:$PATH" nexus sandbox create --repo "$PWD" --fresh
PATH="/tmp/nexus-dogfood-bin:$PATH" nexus sandbox start <workspace-id>
PATH="/tmp/nexus-dogfood-bin:$PATH" nexus sandbox exec <workspace-id> -- docker compose ps
```

For local dogfood, use the default profile (`nexus daemon connect` / stored profile) so the app picks up SSH + tunnel + token. The app does not use `NEXUS_DAEMON_URL` for routing.

### If the app is stuck at "Connecting..."

```bash
REMOTE_HOST=user@host scripts/remote/daemon-status.sh
pkill -x NexusApp 2>/dev/null || true
task dev:remote    # or dev:swift if you also need a fresh app build + RPC
scripts/swift/open.sh
```

Tail logs while reproducing:

```bash
REMOTE_HOST=user@host scripts/remote/daemon-logs.sh
```

If you see `"Method not found"`, the Mac client is newer than the remote daemon — run `task dev:remote` (or `task dev:swift`).

---

## Remote Linux daemon + Mac CLI (manual / first-time)

### First-time setup (fresh Linux box)

```bash
cd packages/nexus
GOOS=linux GOARCH=amd64 go build -o /tmp/nexus-linux ./cmd/nexus
ssh <host> "mkdir -p ~/.local/bin"
scp /tmp/nexus-linux <host>:~/.local/bin/nexus-dev
ssh <host> "XDG_STATE_HOME=~/.local/state-dev ~/.local/bin/nexus-dev daemon start --port 7778"
nexus-dev daemon connect <host>
```

Day-to-day, prefer `**task dev:remote**` from the repo root with `.env.local` instead of ad hoc `scp`.

### Workspace lifecycle

```bash
nexus-dev workspace create --name myws --repo ~/magic/my-project
nexus-dev workspace start myws
nexus-dev workspace list
nexus-dev workspace fork myws --name myws-feature --ref feature-branch
```

Fork worktrees live at `<gitRoot>/.worktrees/<name>`; `.worktrees/` is auto-added to `.git/info/exclude`.

### Fresh start (implode)

```bash
ssh <host> "XDG_STATE_HOME=~/.local/state-dev ~/.local/bin/nexus-dev daemon implode --force"
ssh <host> "XDG_STATE_HOME=~/.local/state-dev ~/.local/bin/nexus-dev daemon start --port 7778"
nexus-dev daemon connect <host>
```

### Token handling

- Mac CLI: token in Keychain after `nexus-dev daemon connect`
- Non-interactive SSH uses full path: `$HOME/.local/bin/nexus-dev daemon token`
- Inspect: `security find-generic-password -s nexus -a daemon-token -w`

### Logs and diagnostics

```bash
REMOTE_HOST=user@host scripts/remote/daemon-logs.sh
ssh <host> "cat ~/.local/state-dev/nexus/vms/<ws-id>/firecracker.log"
nexus-dev workspace list
```

---

## Testing the Mac app — Headless RPC (MANDATORY)

> **RULE: NEVER use screenshots, screen coordinates, or UI automation (AppleScript, Accessibility APIs, browser-use) to test the Mac app. These methods are brittle and unreliable. Always use the Headless RPC server.**

### How it works

`HeadlessRPCServer` is a local HTTP server built into the Mac app, listening on `127.0.0.1:7778`. It is activated by a sentinel file:

```bash
touch ~/.nexus-headless-rpc   # persists across app restarts; remove to disable
```

The app checks for this file on startup. If already running, restart:

```bash
pkill -x NexusApp; scripts/swift/open.sh
```

Verify it's active:

```bash
curl -sf http://127.0.0.1:7778/status   # → {"ok":true,"version":"1"}
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

# 2. Workspace management via Mac CLI (talks directly to remote daemon)
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

### Robust polling snippets

```bash
# Poll workspace state from /workspace/list (not /workspace/info)
WS_ID="ws-..."
for i in $(seq 1 120); do
  STATE=$(curl -s http://127.0.0.1:7778/workspace/list \
    | python3 -c "import sys,json; d=json.load(sys.stdin); w=[x for x in d.get('workspaces',[]) if x.get('id')=='$WS_ID']; print(w[0].get('state','missing') if w else 'missing')")
  echo "[$i] $STATE"
  [ "$STATE" = "running" ] && break
  sleep 2
done

sleep 3  # terminal service handshake settle

for i in $(seq 1 8); do
  TAB_JSON=$(curl -s -X POST http://127.0.0.1:7778/terminal/open \
    -H "Content-Type: application/json" \
    -d "{\"workspaceID\":\"$WS_ID\",\"name\":\"verify\"}")
  TAB=$(echo "$TAB_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('tabID',''))" 2>/dev/null || true)
  if [ -n "$TAB" ]; then
    echo "opened tab: $TAB"
    break
  fi
  echo "terminal/open retry $i: $TAB_JSON"
  sleep 2
done
```

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
| Headless RPC port | `127.0.0.1:7778` |
| Sentinel file | `~/.nexus-headless-rpc` |
| Implementation | `packages/nexus-swift/Sources/NexusCore/HeadlessRPCServer.swift` |
| Terminal registry | `packages/nexus-swift/Sources/NexusCore/TerminalRegistry.swift` |

### Unit tests

```bash
cd packages/nexus-swift && swift test --filter NexusAppTests
```

---

## Firecracker rootless — full test flow

This section covers the end-to-end flow for the rootless Firecracker setup: from building and deploying an updated daemon to verifying everything works inside a workspace VM using the Headless RPC.

### Architecture cheat-sheet

```
macOS (Mac app)
  └─ Headless RPC :7778   ← test harness
  └─ SSH tunnel → :7778   ← nexus-dev daemon on linuxbox (dev)
                    :7777   ← nexus daemon on linuxbox (prod — never touched by dev tasks)

linuxbox (~/.local/bin/nexus-dev daemon, state in ~/.local/state-dev/nexus/)
  ├─ rootfs.ext4 (8 GB, Ubuntu, tools)  ← ~/.local/share/nexus/vm/
  ├─ vmlinux.bin                         ← ~/.local/share/nexus/vm/
  ├─ nexus-firecracker-agent (embedded in nexus binary, injected into rootfs)
  └─ per-workspace VM
       ├─ workspace.ext4  (XFS reflink clone of base.ext4)   ← /data/nexus/...
       └─ .nexus-host-config.ext4  (rebuilt per start)       ← <project-root>/
```

### Build + deploy cycle (THE right order)

**Always use `task dev:remote`.** It handles the correct build order automatically.

```bash
task dev:remote        # build agent → embed → build nexus → scp → restart daemon
task dev:swift         # same + regenerate Swift RPC + Xcode build + open app
```

**If doing it manually** (rare): the Firecracker agent is a Linux binary embedded in the nexus binary via `//go:embed agent-linux-amd64`. You must compile the agent first, then compile nexus:

```bash
cd packages/nexus

# Step 1 — compile agent for linux/amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o cmd/nexus/agent-linux-amd64 ./cmd/nexus-firecracker-agent/

# Step 2 — compile nexus (embeds agent-linux-amd64 automatically)
GOOS=linux GOARCH=amd64 go build -o /tmp/nexus-linux ./cmd/nexus/

# Step 3 — deploy
scp /tmp/nexus-linux newman@linuxbox:/tmp/nexus-new
ssh newman@linuxbox "
  XDG_STATE_HOME=~/.local/state-dev ~/.local/bin/nexus-dev daemon stop 2>/dev/null || true
  pkill -f firecracker 2>/dev/null || true
  sleep 1
  install -m 0755 /tmp/nexus-new ~/.local/bin/nexus-dev
"
```

**Common trap**: running only `go build ./cmd/nexus/` without re-compiling `cmd/nexus-firecracker-agent/` first embeds the old agent binary. The daemon will detect the size/hash difference and inject properly, but any new agent-side features (e.g. `ensureGuestBasePackages`, iptables-legacy fix) will not be present in the VM. Always build both.

### Agent injection mechanics

On `nexus daemon start`, the daemon:
1. Extracts the embedded agent to `~/.local/bin/nexus-firecracker-agent` (SHA-256 compared, not size — avoids false "no change" on same-size builds)
2. Compares the extracted agent's SHA-256 to `~/.local/state/nexus/rootfs-agent.sha256`
3. If different (or hash file missing), runs `debugfs` to inject the agent binary into `rootfs.ext4`
4. Saves the new hash

After a **fresh rootfs build** the hash file is deleted automatically so the agent is always re-injected. If the agent seems stale in the VM:

```bash
ssh newman@linuxbox "rm -f ~/.local/state-dev/nexus/rootfs-agent.sha256"
# then restart daemon (provision or daemon restart)
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

To force a clean rootfs rebuild (e.g. after patching `patchRootfsForRoot`):
```bash
ssh newman@linuxbox "rm -f ~/.local/share/nexus/vm/rootfs.ext4 ~/.local/state-dev/nexus/rootfs-agent.sha256"
# then provision
```

### Provisioning via Headless RPC (preferred for testing)

Always provision through the Mac app's Headless RPC to simulate the real user flow:

```bash
# Provision: uploads binary, starts daemon, builds rootfs if missing, injects agent
curl -s -X POST http://127.0.0.1:7778/daemon/provision \
  -H "Content-Type: application/json" \
  -d '{"sshTarget":"newman@linuxbox"}' --max-time 300 \
  | python3 -c "
import sys,json
d = json.load(sys.stdin)
for p in d.get('phases',[]): print(p['step'], p['phase'], '|', p['message'])
print('status:', d.get('status','?'))
"
```

Expected output when all is well:
```
bootstrap preflight | preflight: KVM accessible
bootstrap asset-install | asset-install: all assets present
bootstrap runtime-verify | runtime-verify: runtime verified and state persisted
bootstrap daemon-launch | daemon-launch: background process pid=...
```

### Full end-to-end test sequence

```bash
# ── 0. Prerequisites ──────────────────────────────────────────────────────────
touch ~/.nexus-headless-rpc       # enable headless RPC (once)
curl -sf http://127.0.0.1:7778/status   # must return {"ok":true}

# ── 1. Build + provision ──────────────────────────────────────────────────────
task dev:remote                   # or manual steps above

curl -s -X POST http://127.0.0.1:7778/daemon/provision \
  -H "Content-Type: application/json" \
  -d '{"sshTarget":"newman@linuxbox"}' --max-time 300 | python3 -c "
import sys,json; d=json.load(sys.stdin)
for p in d.get('phases',[]): print(p['phase'], p['message'])"

# ── 2. Start workspace (stays "starting" ~30s on first boot) ─────────────────
WS_ID="ws-1776919582972649535"   # or from: nexus workspace list

curl -s -X POST http://127.0.0.1:7778/workspace/start \
  -H "Content-Type: application/json" -d "{\"workspaceID\":\"$WS_ID\"}"

# Poll until running
for i in $(seq 1 60); do
  STATE=$(curl -s http://127.0.0.1:7778/workspace/list | python3 -c "
import sys,json; ws=[w for w in json.load(sys.stdin).get('workspaces',[]) if '$WS_ID' in w['id']]; print(ws[0]['state'] if ws else '?')")
  echo "[$i] $STATE"
  [ "$STATE" = "running" ] && break
  sleep 5
done

# ── 3. Open terminal and verify tools ─────────────────────────────────────────
sleep 3   # brief settle after running state

TAB=$(curl -s -X POST http://127.0.0.1:7778/terminal/open \
  -H "Content-Type: application/json" \
  -d "{\"workspaceID\":\"$WS_ID\",\"name\":\"verify\"}" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('tabID','ERROR'))")
echo "Tab: $TAB"

rpc_run() {
  local cmd="$1" delay="${2:-3}"
  curl -s -X POST http://127.0.0.1:7778/terminal/write \
    -H "Content-Type: application/json" \
    -d "{\"tabID\":\"$TAB\",\"text\":\"$cmd\n\"}" > /dev/null
  sleep "$delay"
  curl -s "http://127.0.0.1:7778/terminal/read?tabID=$TAB" \
    | python3 -c "
import sys,json,re
out=re.sub(r'\x1b\[[^a-zA-Z]*[a-zA-Z]|\[\?2004[hl]|\r','',json.load(sys.stdin).get('output',''))
print(out[-400:])"
}

rpc_run "node --version; npm --version; make --version | head -1; docker --version"
rpc_run "docker ps"                  # dockerd starts in background, wait 3s
rpc_run "echo TERM=\$TERM"          # should be xterm-256color
rpc_run "cat /var/lib/nexus-tools-installed-v3"   # "ok" = stamp written
rpc_run "ls /workspace"             # project files visible
```

### ANSI stripping — always do this

Terminal output contains escape sequences that corrupt assertions. Strip them before any `grep` or string comparison:

```python
import re
def strip_ansi(s):
    return re.sub(r'\x1b\[[^a-zA-Z]*[a-zA-Z]|\[\?2004[hl]|\r', '', s)
```

In one-liners:
```bash
curl -s "http://127.0.0.1:7778/terminal/read?tabID=$TAB" \
  | python3 -c "import sys,json,re; out=re.sub(r'\x1b\[[^a-zA-Z]*[a-zA-Z]|\[\?2004[hl]|\r','',json.load(sys.stdin).get('output','')); print(out)"
```

### Key assertions to verify after a deploy

| Check | Command | Expected |
|-------|---------|----------|
| Tools installed | `node --version; make --version; docker --version` | version strings, no "not found" |
| Stamp written | `cat /var/lib/nexus-tools-installed-v3` | `ok` |
| Docker daemon | `docker ps` | header line (CONTAINER ID...) |
| TERM | `echo $TERM` | `xterm-256color` |
| Codex auth | `ls /root/.codex/` | includes `auth.json` |
| Workspace files | `ls /workspace` | project contents |
| Disk space | `df -h /` | < 70% used (8 GB rootfs) |
| Second boot speed | stop + start + poll running | < 10 s |

### Host config drive — what gets passed through

Built at `<project-root>/.nexus-host-config.ext4` on every workspace start. Contains:
- `.gitconfig`, `.ssh/known_hosts`, `.ssh/config`, `.ssh/authorized_keys`
- `.config/gh/` — GitHub CLI auth
- `.config/opencode/`, `.opencode/` — opencode config
- `.config/claude/` — Claude CLI credentials
- `.codex/auth.json`, `.codex/config.json` — Codex CLI OAuth token
- `.nexus-env` — `export OPENAI_API_KEY=...` etc., sourced in `/root/.profile`

To pass API keys: add them to `~/.bashrc` or `~/.profile` on the Linux host and restart the daemon (so the daemon's own process env has them when it calls `buildAPIKeyEnvFile`).

---

## Common mistakes + how to avoid them

### 1. Forgetting to rebuild the agent before nexus
**Symptom**: new agent code (tools install, docker fix, TERM) not present in VM.
**Cause**: `go build ./cmd/nexus/` embeds whichever `agent-linux-amd64` binary was last compiled.
**Fix**: always use `task dev:remote`, or manually run both build steps in order.

### 2. Stale agent hash file after rootfs rebuild
**Symptom**: `init /usr/local/bin/nexus-firecracker-agent failed (error -2)` in Firecracker log.
**Cause**: `rootfs.ext4` was deleted and rebuilt, but `rootfs-agent.sha256` still matches the agent binary — so `ensureFirecrackerGuestAgent` thinks the rootfs already has the agent.
**Fix**: the code now deletes the hash file after `buildRootlessRootfs`. If you ever see this, run:
```bash
ssh newman@linuxbox "rm -f ~/.local/state/nexus/rootfs-agent.sha256"
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
ssh newman@linuxbox "rm ~/.local/share/nexus/vm/rootfs.ext4 ~/.local/state-dev/nexus/rootfs-agent.sha256"
```
Then provision to rebuild.

### 7. `Method not found` in Mac app
**Cause**: the Mac app's Swift RPC stubs are newer than the remote daemon.
**Fix**: `task dev:remote` (or `task dev:swift` to also rebuild the app).

### 8. Workspace stuck at "starting" forever
**Symptom**: workspace never transitions to `running`.
**Triage**:
```bash
ssh newman@linuxbox "grep -v 'accepted connection' /data/nexus/firecracker-vms/<ws-id>/firecracker.log | tail -20"
```
Look for:
- `failed (error -2)` → agent not in rootfs (stale hash, see #2)
- `apt-get install failed` → package issue; check full log
- `vsock connection refused` → VM booted but agent crashed

---

## Diagnostics reference

### Daemon health
```bash
# Is it running and what version?
ssh newman@linuxbox "XDG_STATE_HOME=~/.local/state-dev ~/.local/bin/nexus-dev daemon status"

# Tail live logs
ssh newman@linuxbox "tail -f ~/.local/state-dev/nexus/daemon.log"

# Or via script
REMOTE_HOST=newman@linuxbox scripts/remote/daemon-logs.sh
```

### VM / Firecracker
```bash
# Agent boot messages (non-connection noise filtered out)
ssh newman@linuxbox "grep -v 'accepted\|API server\|Kernel command\|Command line' \
  /data/nexus/firecracker-vms/<ws-id>/firecracker.log"

# Is the VM process alive?
ssh newman@linuxbox "pgrep -a firecracker"

# Rootfs and squashfs
ssh newman@linuxbox "ls -lh ~/.local/share/nexus/vm/"

# Agent hash
ssh newman@linuxbox "cat ~/.local/state-dev/nexus/rootfs-agent.sha256 | cut -c1-16"
ssh newman@linuxbox "sha256sum ~/.local/bin/nexus-firecracker-agent | cut -c1-16"
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
rpc_run "cat /var/lib/nexus-tools-installed-v3" # stamp present?
rpc_run "cat /tmp/nexus-agent-dockerd.log"  # dockerd startup errors
rpc_run "ls /run/nexus-host/"               # config drive contents
rpc_run "env | grep -iE 'openai|anthropic|gemini' | sed 's/=.*/=***/' | head"
rpc_run "mount | grep -E '/workspace|/tmp'"
```

### Config drive inspection (from host)
```bash
ssh newman@linuxbox "debugfs -R 'ls -l /' /home/newman/magic/<project>/.nexus-host-config.ext4 2>/dev/null"
```

### XFS data volume
```bash
ssh newman@linuxbox "df -h /data"          # XFS volume health
ssh newman@linuxbox "xfs_info /data"       # confirm XFS with reflink enabled
ssh newman@linuxbox "ls -lh /data/nexus/firecracker-vms/"
```

---

## Debugging tips

**Daemon crashing silently** — `ps aux | grep nexus-daemon`. `UE`/`UNE` in `ps` often means codesign issues for embedded binaries; rebuild via Xcode and ensure `Resources/nexus-daemon` is staged.

**Local sandbox exec outside repo root** — if commands run from `~/.nexus/workspaces/instances/...`, check `pkg/server/pty/handler.go` (`localWorkDirForOpen`).

**Port still held after kill** — `lsof -i :63987` and `lsof -i :<local-driver-port>`.

**XCUITest hangs at activation** — avoid `switch` in `ViewBuilder` for activation; use `if` / `else` with `Group` on macOS.

**App not connecting** — WebSocket expects `Authorization: Bearer <token>`; query `?token=` is not supported. Token order: `NEXUS_DAEMON_TOKEN` env → macOS keychain (`nexus-daemon-token` by default).

**App not reaching daemon** — profile must have `sshTarget`; tunnel up; token matches (`scripts/remote/daemon-status.sh`, `nexus daemon connect <host>`).

**Repeated `401`** — `security find-generic-password -s nexus -a daemon-token -w`; re-run `nexus daemon connect <host>` for remote.

`**nexus-dev daemon connect` exit 127** — deploy binary to `$HOME/.local/bin/nexus-dev` on the remote host.


**Port mismatch** — `nexus daemon status --json` per repo/worktree when multiple daemons run.
