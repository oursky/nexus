#!/usr/bin/env bash
# scripts/remote/deploy-libkrun.sh
#
# Build and deploy the libkrun-enabled nexus binary to a remote Linux host.
# No pre-installed dependencies required — libkrun.so and libkrun.h are
# downloaded automatically from smolvm GitHub releases.
#
# Build steps (run on the remote):
#   1. Download the smolvm release tarball (contains pre-built libkrun.so files).
#   2. Extract libkrun.h from the libkrun source repo (header-only, no build).
#   3. Build nexus-libkrun-vm (standalone CGO binary) linked against libkrun.so.
#   4. Stage the .so files + standalone binary as go:embed artifacts.
#   5. Build the main nexus binary (CGO_ENABLED=0, embeds all libkrun artifacts).
#   6. Clean up staging files from the source tree.
#
# Usage:
#   REMOTE_HOST=user@host scripts/remote/deploy-libkrun.sh
#
# Optional overrides:
#   REMOTE_BIN          Installation path for the nexus binary (default $HOME/.local/bin/nexus-dev)
#   SMOLVM_VERSION      smolvm release tag to fetch libs from (default: v0.5.19)
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:?REMOTE_HOST is not set}"
REMOTE_BIN="${REMOTE_BIN:-\$HOME/.local/bin/nexus-dev}"
# v0.5.20 libkrun is built without virtio-net symbols.
# v0.5.19 exports krun_add_net_unixstream and krun_set_passt_fd.
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEXUS_PKG="$SCRIPT_DIR/../../packages/nexus"

echo "==> Syncing source to ${REMOTE_HOST}:~/magic/nexus/packages/nexus ..."
rsync -az --delete \
  "$NEXUS_PKG/" \
  "${REMOTE_HOST}:~/magic/nexus/packages/nexus/" \
  --exclude="tmp/" \
  --exclude="*.test" \
  --exclude="cmd/nexus/nexus-libkrun-vm" \
  --exclude="cmd/nexus/libkrun-embed.so" \
  --exclude="cmd/nexus/libkrunfw-embed.so"

echo "==> Building nexus (libkrun) on ${REMOTE_HOST}..."
ssh "${REMOTE_HOST}" SMOLVM_VERSION="${SMOLVM_VERSION}" bash << 'REMOTE'
set -euo pipefail
export GOPATH="$HOME/go"
export PATH="$HOME/.local/share/mise/installs/go/1.24.11/bin:$PATH"

WORKDIR="$HOME/magic/nexus/packages/nexus"
EMBED_DIR="$WORKDIR/cmd/nexus"
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"
SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
LIBKRUN_H_URL="https://raw.githubusercontent.com/containers/libkrun/main/include/libkrun.h"

go version
cd "$WORKDIR"
mkdir -p tmp

echo ""
echo "── Step 1: go mod download ──────────────────────────────────────────────"
go mod download

echo ""
echo "── Step 2: download smolvm ${SMOLVM_VERSION} (pre-built libkrun.so) ─────"
# Cache in tmp/ so repeated runs are fast.
if [[ ! -f "tmp/${SMOLVM_TARBALL}" ]]; then
  echo "  fetching ${SMOLVM_URL}"
  curl -fsSL --progress-bar -o "tmp/${SMOLVM_TARBALL}" "${SMOLVM_URL}"
else
  echo "  cache hit: tmp/${SMOLVM_TARBALL}"
fi

# Extract just the lib/ directory.
mkdir -p tmp/smolvm-libs
tar -xzf "tmp/${SMOLVM_TARBALL}" \
    --strip-components=2 \
    -C tmp/smolvm-libs \
    "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
echo "  extracted: $(ls tmp/smolvm-libs)"

# Validate virtio-net capability. Missing symbol is allowed (launcher falls
# back to TSI), but we surface it so operators understand networking mode.
if nm -D tmp/smolvm-libs/libkrun.so.1 | awk '{print $3}' | awk '/^krun_add_net_unixstream$/{found=1} END{exit !found}'; then
  echo "  detected: krun_add_net_unixstream (virtio-net available)"
else
  echo "  warning: smolvm ${SMOLVM_VERSION} libkrun.so.1 lacks krun_add_net_unixstream; launcher will use TSI networking backend" >&2
fi

echo ""
echo "── Step 3: download libkrun.h (header for CGO, no compilation) ─────────"
mkdir -p tmp/include
if [[ ! -f tmp/include/libkrun.h ]]; then
  curl -fsSL -o tmp/include/libkrun.h "${LIBKRUN_H_URL}"
  echo "  downloaded libkrun.h"
else
  echo "  cache hit: tmp/include/libkrun.h"
fi

echo ""
echo "── Step 3b: build nexus-guest-agent (in-VM init, embedded in nexus) ──────"
CGO_ENABLED=0 go build \
  -trimpath -ldflags='-s -w' \
  -o "${EMBED_DIR}/agent-linux-amd64" \
  ./cmd/nexus-guest-agent
echo "  → ${EMBED_DIR}/agent-linux-amd64 built ($(du -sh ${EMBED_DIR}/agent-linux-amd64 | cut -f1))"

echo ""
echo "── Step 4: build nexus-libkrun-vm (CGO standalone binary) ──────────────"
LIBKRUN_LIB="$WORKDIR/tmp/smolvm-libs"
LIBKRUN_INC="$WORKDIR/tmp/include"
CGO_ENABLED=1 \
  CGO_CFLAGS="-I${LIBKRUN_INC}" \
  CGO_LDFLAGS="-L${LIBKRUN_LIB} -lkrun -Wl,-rpath,\$ORIGIN/../lib" \
  go build \
    -tags libkrun \
    -o tmp/nexus-libkrun-vm \
    ./cmd/nexus-libkrun-vm
echo "  → tmp/nexus-libkrun-vm built ($(du -sh tmp/nexus-libkrun-vm | cut -f1))"

echo ""
echo "── Step 5: stage go:embed artifacts ────────────────────────────────────"
# Use the real files (libkrun.so.1 and libkrunfw.so.5.3.0), not the symlinks.
cp tmp/smolvm-libs/libkrun.so.1        "${EMBED_DIR}/libkrun-embed.so"
LIBKRUNFW_REAL=$(find tmp/smolvm-libs -maxdepth 1 -name 'libkrunfw.so.*.*' | sort | tail -1)
if [[ -z "${LIBKRUNFW_REAL}" ]]; then
  echo "ERROR: could not find libkrunfw.so.*.* in tmp/smolvm-libs" >&2
  exit 1
fi
cp "${LIBKRUNFW_REAL}"                  "${EMBED_DIR}/libkrunfw-embed.so"
cp tmp/nexus-libkrun-vm                "${EMBED_DIR}/nexus-libkrun-vm"
echo "  → staged libkrun-embed.so ($(du -sh ${EMBED_DIR}/libkrun-embed.so | cut -f1))"
echo "  → staged libkrunfw-embed.so ($(du -sh ${EMBED_DIR}/libkrunfw-embed.so | cut -f1))"
echo "  → staged nexus-libkrun-vm ($(du -sh ${EMBED_DIR}/nexus-libkrun-vm | cut -f1))"

echo ""
echo "── Step 6: build main nexus (no CGO, embeds libkrun artifacts) ─────────"
CGO_ENABLED=0 \
  go build \
    -tags libkrun \
    -o tmp/nexus \
    ./cmd/nexus
echo "  → tmp/nexus built ($(du -sh tmp/nexus | cut -f1))"

echo ""
echo "── Step 7: clean staging files from source tree ────────────────────────"
# Keep nexus-libkrun-vm in cmd/nexus/ — it's committed to git so that
# deploy.sh can build libkrun-enabled binaries without CGO on the build machine.
# Run: git add packages/nexus/cmd/nexus/nexus-libkrun-vm && git commit
rm -f "${EMBED_DIR}/libkrun-embed.so" \
      "${EMBED_DIR}/libkrunfw-embed.so" \
      "${EMBED_DIR}/agent-linux-amd64"
echo "  → done (nexus-libkrun-vm kept in ${EMBED_DIR}/ — remember to git commit it)"
REMOTE

echo ""
echo "==> Pulling nexus-libkrun-vm → packages/nexus/cmd/nexus/nexus-libkrun-vm ..."
NEXUS_PKG_LOCAL="${SCRIPT_DIR}/../../packages/nexus"
scp "${REMOTE_HOST}:~/magic/nexus/packages/nexus/cmd/nexus/nexus-libkrun-vm" \
    "${NEXUS_PKG_LOCAL}/cmd/nexus/nexus-libkrun-vm"
echo "  → nexus-libkrun-vm updated locally ($(du -sh ${NEXUS_PKG_LOCAL}/cmd/nexus/nexus-libkrun-vm | cut -f1))"
echo "  → run: git add packages/nexus/cmd/nexus/nexus-libkrun-vm && git commit -m 'chore: update nexus-libkrun-vm'"

echo ""
echo "==> Deploying to ${REMOTE_HOST}:${REMOTE_BIN} ..."
ssh "${REMOTE_HOST}" "~/.local/bin/nexus daemon stop 2>/dev/null || true; sleep 1"
ssh "${REMOTE_HOST}" "\
  rm -f ${REMOTE_BIN} && \
  cp ~/magic/nexus/packages/nexus/tmp/nexus ${REMOTE_BIN} && \
  chmod +x ${REMOTE_BIN}"

echo ""
echo "==> Pulling built binary → packages/nexus-swift/Resources/nexus-linux-amd64 ..."
SWIFT_RES="${SCRIPT_DIR}/../../packages/nexus-swift/Resources"
scp "${REMOTE_HOST}:~/magic/nexus/packages/nexus/tmp/nexus" \
    "${SWIFT_RES}/nexus-linux-amd64"
echo "  → nexus-linux-amd64 updated ($(du -sh ${SWIFT_RES}/nexus-linux-amd64 | cut -f1))"

echo ""
echo "==> Starting daemon (libkrun) on ${REMOTE_HOST} ..."
ssh "${REMOTE_HOST}" "bash -l -c '${REMOTE_BIN} daemon start --driver=libkrun'" &
START_PID=$!
sleep 5
kill "${START_PID}" 2>/dev/null || true  # background start fires and detaches

echo ""
echo "Done."
echo "  Binary   : ${REMOTE_HOST}:${REMOTE_BIN}"
echo "  Status   : ssh ${REMOTE_HOST} ${REMOTE_BIN} daemon status"
echo "  Logs     : REMOTE_HOST=${REMOTE_HOST} scripts/remote/daemon-logs.sh"
