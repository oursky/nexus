# Contributing to Nexus

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| [Go](https://go.dev/dl/) | ≥ 1.21 | `brew install go` |
| [Node.js](https://nodejs.org/) | ≥ 20 | `brew install node` |
| [pnpm](https://pnpm.io/installation) | latest | `npm install -g pnpm` |
| [go-task](https://taskfile.dev/installation/) | latest | `brew install go-task` |

## Getting Started

```sh
git clone https://github.com/your-org/nexus.git
cd nexus
cp .env.local.example .env.local   # edit as needed
task setup                          # check prereqs + install dependencies
task build                          # compile all packages
```

## Running Tests

```sh
task test
```

Runs `go test ./...` in `packages/nexus` and `pnpm test` in `packages/sdk/js`.

## Local Development

### Daemon (hot-reload)

```sh
task dev:daemon
```

Starts the Go daemon via [air](https://github.com/cosmtrek/air). Install air first:

```sh
go install github.com/cosmtrek/air@latest
```

## Remote Deployment

Set `REMOTE_HOST` in `.env.local`:

```sh
REMOTE_HOST=user@your-server
```

Then cross-compile and deploy to the remote host:

```sh
task deploy:remote
```

Restart the daemon on the remote:

```sh
task daemon:restart
```

## Repository Structure

```
packages/
  nexus/        Go daemon + CLI (workspace lifecycle, RPC, spotlight)
  sdk/js/       TypeScript SDK (@nexus/sdk) for remote workspace control
```

## Available Tasks

Run `task --list` to see all available tasks.

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
