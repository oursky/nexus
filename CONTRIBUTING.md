# Contributing to Nexus

## Prerequisites

| Tool | Version | Install |
| --- | --- | --- |
| [Go](https://go.dev/dl/) | ≥ 1.21 | `brew install go` |
| [go-task](https://taskfile.dev/installation/) | latest | `brew install go-task` |
| SSH-accessible Linux host | — | any VPS or bare-metal box |

## Getting Started

```sh
git clone https://github.com/your-org/nexus.git
cd nexus
cp .env.local.example .env.local   # set REMOTE_HOST=user@your-linux-host
task setup
```

## Dev Loops

All development targets a remote Linux daemon. Set `REMOTE_HOST` in `.env.local`, then:

| Command | What it does |
| --- | --- |
| `task dev:remote` | Cross-compile for linux/amd64, deploy, restart daemon |
| `task dev:cli` | `dev:remote` + install local CLI binary to `~/.local/bin/nexus` |
| `task dev:swift` | `dev:remote` + regenerate Swift SDK + build and open NexusApp |

## Other Tasks

```sh
task build    # compile check (local)
task test     # run Go tests
task ci       # full CI equivalent (go-fix + coverage + core)
task clean    # remove build artifacts
```

Run `task --list` for all available tasks.

## Repository Structure

```
packages/
  nexus/        Go daemon + CLI (workspace lifecycle, RPC, Firecracker, Mutagen)
scripts/
  remote/       SSH/SCP scripts called by Taskfile
  local/        local install scripts
  ci/           CI scripts
```

## Taskfile Conventions

**Taskfile is an entrypoint only** — it calls scripts under `scripts/`, never inlines SSH or shell logic.

- Shell logic goes in `scripts/`; remote operations go in `scripts/remote/`
- All scripts use `set -euo pipefail` and are executable
- Tasks pass config via `env:` and call the script

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
