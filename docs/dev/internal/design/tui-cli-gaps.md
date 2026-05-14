# TUI vs CLI Feature Gap Analysis

> Generated: 2026-05-14  
> Branch: feat/ci-release-tui  
> Scope: `packages/nexus/cmd/nexus/commands/` vs `packages/nexus/internal/tui/`

---

## Covered (TUI has full parity)

| Feature | CLI command | TUI action | Notes |
|---------|------------|------------|-------|
| List workspaces | `workspace list` | Left-pane workspace list | Includes project name, state badge |
| Create workspace | `workspace create` | `n`/`+` → create wizard | Name, repo, ref fields |
| Start workspace | `workspace start` | `s` (list or detail) | |
| Stop workspace | `workspace stop` | `x` (list or detail) | |
| Delete workspace | `workspace remove` | `d` + confirm `y` | |
| Workspace detail | `workspace info` | `enter` → detail view | All key fields displayed |
| Interactive shell | `workspace shell` | `t` (list or detail) | Runs `nexus workspace shell` via ExecProcess |
| Sync start | `workspace sync start` | `y` → `n` → local-path + direction | bidirectional/up/down supported |
| Sync stop | `workspace sync stop` | `y` → `x` | By session ID |
| Sync pause | `workspace sync pause` | `y` → `p` | |
| Sync resume | `workspace sync resume` | `y` → `r` | |
| Sync list | `workspace sync list` | `y` panel | Shows session IDs, paths, status, direction |
| Spotlight add forward | `spotlight port add` + `spotlight start` | sidebar `n` (port prompt) | Via `spotlight.start` RPC |
| Spotlight remove forward | `spotlight port remove` | sidebar `x` / detail `x` | Via `workspace.ports.remove` RPC |
| Spotlight list | `spotlight list` | sidebar + detail `l` | Refreshed every 3 s |
| Spotlight toggle all | `spotlight start` / `spotlight stop` | sidebar `a` | Discover-then-start all or stop all |
| SSH / editor hints | `workspace open-editor --check` | detail `c` → connect panel | ProxyJump, SSH command, Cursor/VSCode hints |
| Daemon connect (wizard) | `daemon connect <host>` | Connect wizard (first-run) | Host, port, SSH identity prompts |
| Fork workspace | `workspace fork` | detail `f` → child name prompt | Minimal parity |
| Project names in list | `project list` | Background fetch; names shown in workspace list | |

---

## Partial (TUI has the feature but misses something)

| Feature | CLI command | TUI gap | Impact |
|---------|------------|---------|--------|
| **Spotlight SSH tunnel** | `spotlight start` establishes `ssh -L` tunnel | **Fixed by this PR** (Task A): TUI now starts `MultiTunnel` when forwards become active and shows `✓`/`✗` tunnel indicator | High |
| Fork options | `workspace fork --name <n> --ref <branch>` | TUI only prompts for child name; `--ref` is not exposed | Medium – branch forks require CLI |
| Shell workdir | `workspace shell --workdir <path>` | TUI always uses `/workspace`; no workdir prompt | Medium – common for monorepos |
| Workspace restore | `workspace restore <ws>` | Accessible only via spotlight panel context; not a first-class TUI action | Medium – disaster recovery is CLI-only |
| Sync stats | `workspace sync status` (byte counts, file counts, conflicts) | TUI sync panel shows status/direction but no I/O stats | Low – informational only |
| Portal / tunnel ports | `workspace portal` | Detail view shows `Ports:` from `TunnelPorts`; no action to expose them | Low |
| Daemon status | `daemon status` | TUI shows `● connected` / `● disconnected`; no version, uptime, socket path | Low – developer diagnostic |
| Spotlight `--no-tunnel` | `spotlight start` skips tunnel for local | TUI skips correctly when LocalPort > 0 or Host is localhost | No gap (works correctly) |
| Open editor (interactive) | `workspace open-editor` launches Cursor/VSCode via `open` URL | TUI shows the deep-link string in the connect panel but does not launch it | Medium – copy-paste workaround needed |
| SSH-VM direct | `workspace ssh-vm` (ProxyJump into libkrun VM) | TUI connect panel shows the SSH command string; no one-click exec | Low |

---

## Missing (CLI has it, TUI doesn't)

| Feature | CLI command | Priority | Notes |
|---------|------------|---------|-------|
| **Workspace restore** | `workspace restore` | Phase 2 | Restore from snapshot; should be a detail-view key binding |
| **Daemon disconnect** | `daemon disconnect` | Phase 2 | Users need to switch between remote and local daemons inside TUI; currently requires CLI |
| **Profile list / switch** | `daemon profile` | Phase 2 | TUI has no way to list or switch between saved profiles |
| **Workspace export** | `workspace export` | Phase 2 | Export to NXPACK bundle; used for backup/sharing |
| **Workspace import** | `workspace import` | Phase 2 | Import NXPACK bundle |
| **Daemon stop** | `daemon stop` | Phase 3 | Rarely needed interactively; could be a hidden key |
| **Daemon version** | `daemon version` | Phase 3 | Show daemon build version |
| **Volume management** | `workspace volume` (create/delete/list/info/rename/attach/detach/snapshot/sync/mount/unmount) | Phase 3 | Full volume CRUD is a large surface; TUI currently shows no volume data. Could start with a read-only volume panel |
| **Spotlight `--ref` at start** | `spotlight start` per-forward `RemotePort`/`TargetHost` override | Phase 2 | TUI currently discovers and uses these correctly via `spotlight.list`; no user override exposed |
| **Workspace run (script)** | `workspace run <script>` / `workspace shell` piped stdin | Out of scope | Automation/CI use case; TUI is interactive by design |
| **Project CRUD** | `project create/get/remove` | Phase 3 | TUI shows project names but can't manage projects |
| **`workspace ready`** | `workspace ready` | Out of scope | CI/scripting; waits until workspace is running |
| **VM bake** | `vm bake` | Out of scope | Infrastructure admin; not end-user facing |
| **Bundle run** | `bundle run` (hidden) | Out of scope | Internal; used by self-executing bundles |
| **Daemon implode** | `daemon implode` | Out of scope | Destructive admin; intentionally CLI-only |
| **Daemon token** | `daemon token` | Out of scope | Security-sensitive; keep in CLI only |

---

## TUI-only (TUI has features the CLI doesn't)

| Feature | Notes |
|---------|-------|
| **Three-pane split layout** | Workspace list + live PTY terminal + spotlight sidebar simultaneously |
| **Inline PTY split view** | Real-time terminal output in the TUI without leaving the list |
| **Session tab bar** | Persists workspace session history across TUI restarts (`~/.local/state/nexus/tui-sessions.json`) |
| **Auto-attach** | `--auto-attach` flag re-enters last active workspace shell on startup |
| **No-profile first-run wizard** | Detects missing profile, offers to start local daemon or configure remote |
| **Mouse support** | Click-to-select workspace, scroll, pane focus switching |
| **Spotlight SSH tunnel indicator** | `✓`/`✗` per-forward tunnel liveness shown inline (added by this PR) |
| **Sidebar discover-ports** | Shows ports detected inside the workspace that aren't yet forwarded |
| **Workspace dashboard** | All workspaces visible simultaneously with state badges |

---

## Priority Summary

### Phase 2 — Should add soon

1. **Workspace restore** (`r` key in detail view) — disaster recovery should not require CLI
2. **Profile switch / daemon disconnect** — multi-profile users are blocked without this; currently requires quitting TUI, running `nexus daemon connect <other>`, restarting TUI
3. **Workspace export/import** — bundle round-trip is a key workflow for sharing workspaces
4. **Fork `--ref`** — missing branch selection blocks branch-per-feature workflows

### Phase 3 — Nice to have

5. **Volume panel (read-only at minimum)** — volumes are invisible in the TUI; even listing attached volumes would help
6. **Project CRUD** — low-friction project management
7. **Daemon stop** — hidden power-user key binding
8. **Sync stats** — byte counters, conflict counts in sync panel

### Out of scope

- `workspace run`, `workspace ready`, `vm bake`, `bundle run`, `daemon implode`, `daemon token` — CI/scripting/admin surface that doesn't fit the interactive TUI model

---

## Specific Questions from Audit

### `workspace sync` panel coverage

The TUI sync panel (`y`) covers start, stop, pause, resume, and list — **full parity with the `workspace sync` subcommand group**. Missing: per-session status stats (byte/file counts), which are Phase 3 / informational only.

### `workspace fork` coverage

TUI covers `--name`. Missing: `--ref` (branch for fork). Impact: users who want to fork to a different branch must use the CLI. Phase 2.

### `workspace restore` accessibility

Not accessible from TUI at all. Should be a `R` key binding in the detail view. Phase 2.

### `daemon connect/disconnect` — profile switching

TUI has a first-run wizard to configure a profile, but offers no way to switch profiles once connected. Users must exit TUI and run `nexus daemon connect <other-host>`. Blocking for multi-environment users. Phase 2.

### Spotlight — SSH tunnel gap (fixed)

The `spotlight start` CLI (`start.go`/`run.go`) calls `sshtunnel.NewMultiWithOptions(host, sshPort, identity, false)` and `mt.Start(fwds, 5s)` to open a single SSH process forwarding all ports in one `-L` spec each. The TUI called `spotlight.start` RPC to create daemon-side forwards but never established the client-side SSH tunnel. This is now fixed in this PR: `startSidebarTunnelCmd` replicates the same `sshtunnel.MultiTunnel` pattern, started in a background goroutine, with `tunnelStartedMsg` delivering the result back to the Update loop. Tunnel is closed on quit, workspace switch, or when all forwards are removed.

### Spotlight — other gaps

- No per-forward protocol display (TCP/HTTP/etc.) — the `Forward.Protocol` field exists but TUI doesn't render it. Minor cosmetic, Phase 3.
- No persistent spotlight port config editing (add/remove from the config, not just the live forwards). The `spotlight port add/remove/list` CLI commands manage the port config; TUI only manages live `spotlight.Forward` records. Phase 3.

### `workspace shell` — `--command` flag

CLI accepts `workspace shell <ws> --workdir <path>` and reads stdin as a script when not a TTY. TUI's `t` key always spawns an interactive shell at `/workspace`. Adding a workdir prompt would close this gap. Phase 2.

### `workspace ports` — full CRUD

The `workspace ports.remove` RPC is used by TUI sidebar `x`. Adding a forward (`n`) calls `spotlight.start`. Listing is done via `spotlight.list`. There is no `workspace portsList` call surfaced in the TUI (only `spotlight.list`). The `workspace portal` command calls `workspace.portsList` for tunnel ports. Minor distinction — effectively covered. Phase 3 / low.

### `bundle` commands

Hidden from help, used by self-executing NXPACK bundles. No TUI surface needed. Out of scope.

### `vm bake`

Infrastructure admin (Linux only, builds rootfs layers). Out of scope for TUI.

### Error handling — preflight errors

CLI shows stderr from failed RPCs as formatted error messages. TUI shows errors in `m.statusLine` (single line at top, max ~80 chars). Long error messages are truncated. Phase 3: consider an error-detail modal or scrollable status area.
