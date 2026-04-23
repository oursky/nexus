#!/usr/bin/env bash
# scripts/remote/deploy-libkrun.sh
# Sync and build the libkrun experiment binary on the remote Linux host.
#
# Usage: REMOTE_HOST=user@host scripts/remote/deploy-libkrun.sh
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set}"
REMOTE_BIN="${REMOTE_BIN:-~/.local/bin/nexus}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"
WORKTREE="$SCRIPT_DIR/../../.worktrees/libkrun-experiment/packages/nexus"

echo "==> Syncing source to ${REMOTE_HOST}..."
rsync -az --delete \
  "$WORKTREE/" \
  "${REMOTE_HOST}:~/magic/nexus/.worktrees/libkrun-experiment/packages/nexus/" \
  --exclude="tmp/" \
  --exclude="*.test"

echo "==> Building nexus (libkrun) on ${REMOTE_HOST}..."
# Build on the remote host where libkrun.so is available
ssh "${REMOTE_HOST}" bash << 'REMOTE'
set -euo pipefail
export GOPATH="$HOME/go"
export PATH="$HOME/.local/share/mise/installs/go/1.24.11/bin:$PATH"
WORKDIR="$HOME/magic/nexus/.worktrees/libkrun-experiment/packages/nexus"
LIBKRUN_LIB="$HOME/.smolvm/lib"
LIBKRUN_INC="$HOME/.local/include"

go version

echo "Downloading Go modules..."
cd "$WORKDIR"
go mod download

echo "Building nexus with -tags libkrun..."
mkdir -p tmp
CGO_ENABLED=1 \
  CGO_CFLAGS="-I${LIBKRUN_INC}" \
  CGO_LDFLAGS="-L${LIBKRUN_LIB} -lkrun -Wl,-rpath,${LIBKRUN_LIB}" \
  go build -tags libkrun -o tmp/nexus-libkrun ./cmd/nexus
echo "Build succeeded: tmp/nexus-libkrun"
REMOTE

echo "==> Installing to ${REMOTE_HOST}:${REMOTE_BIN}..."
REMOTE_WORKDIR="$HOME/magic/nexus/.worktrees/libkrun-experiment/packages/nexus"
ssh "${REMOTE_HOST}" "cp ~/magic/nexus/.worktrees/libkrun-experiment/packages/nexus/tmp/nexus-libkrun ${REMOTE_BIN} && chmod +x ${REMOTE_BIN}"
echo "==> Deployed: ${REMOTE_HOST}:${REMOTE_BIN}"
