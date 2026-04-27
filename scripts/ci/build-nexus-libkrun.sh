#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NEXUS_DIR="$REPO_ROOT/packages/nexus"
SMOLVM_VERSION="${SMOLVM_VERSION:-v0.5.19}"

pushd "$NEXUS_DIR" >/dev/null
go generate ./cmd/nexus/

EMBED_DIR="$NEXUS_DIR/cmd/nexus"
BUILD_TAGS=""

# Check if libkrun support is present (nexus-libkrun-vm source exists).
if [[ -d "$NEXUS_DIR/cmd/nexus-libkrun-vm" ]]; then
  SMOLVM_TARBALL="smolvm-${SMOLVM_VERSION#v}-linux-x86_64.tar.gz"
  SMOLVM_URL="https://github.com/smol-machines/smolvm/releases/download/${SMOLVM_VERSION}/${SMOLVM_TARBALL}"
  LIBS_TMP="/tmp/smolvm-libs-${SMOLVM_VERSION}"

  if [[ ! -d "$LIBS_TMP" ]]; then
    curl -fsSL --retry 3 -o "/tmp/${SMOLVM_TARBALL}" "$SMOLVM_URL"
    mkdir -p "$LIBS_TMP"
    tar -xzf "/tmp/${SMOLVM_TARBALL}" --strip-components=2 -C "$LIBS_TMP" \
      "smolvm-${SMOLVM_VERSION#v}-linux-x86_64/lib"
  fi

  cp "$LIBS_TMP/libkrun.so.1" "$EMBED_DIR/libkrun-embed.so"
  LIBKRUNFW_REAL=$(find "$LIBS_TMP" -maxdepth 1 -name 'libkrunfw.so.*.*' | sort | tail -1)
  cp "$LIBKRUNFW_REAL" "$EMBED_DIR/libkrunfw-embed.so"
  BUILD_TAGS="-tags libkrun"

  # Stage passt binary for embedding so the release binary is fully self-contained.
  PASST_EMBED="$EMBED_DIR/passt-embed"
  if [[ -x "$(command -v passt)" ]]; then
    cp "$(command -v passt)" "$PASST_EMBED"
    echo "  → staged system passt for embed ($(du -sh "$PASST_EMBED" | cut -f1))"
  else
    # aarch64 has no upstream static build from passt.top; x86_64 does.
    case "$(go env GOARCH)" in
      amd64|x86_64)
        echo "  downloading passt for embed..."
        curl -fsSL --retry 3 -o "$PASST_EMBED" "https://passt.top/builds/latest/x86_64/passt"
        chmod +x "$PASST_EMBED"
        echo "  → downloaded passt for embed ($(du -sh "$PASST_EMBED" | cut -f1))"
        ;;
      *)
        echo "ERROR: passt not found system-wide and no upstream static build for $(go env GOARCH)." >&2
        echo "Install passt first: sudo apt install passt" >&2
        exit 1
        ;;
    esac
  fi

  # On Linux CI runners, rebuild nexus-libkrun-vm from source so we don't
  # embed a stale committed binary. On macOS dev machines, CGO can't target
  # Linux libkrun.so, so we rely on the committed binary.
  if [[ "$(go env GOOS)" == "linux" && -n "${CI:-}" ]]; then
    echo "Rebuilding nexus-libkrun-vm from source (Linux CI runner)..."
    # Download header from libkrun source repo (not shipped in smolvm tarball).
    LIBKRUN_INC="/tmp/libkrun-include-${SMOLVM_VERSION}"
    mkdir -p "$LIBKRUN_INC"
    if [[ ! -f "$LIBKRUN_INC/libkrun.h" ]]; then
      curl -fsSL --retry 3 -o "$LIBKRUN_INC/libkrun.h" \
        "https://raw.githubusercontent.com/containers/libkrun/main/include/libkrun.h"
    fi
    CGO_ENABLED=1 \
      CGO_CFLAGS="-I$LIBKRUN_INC" \
      CGO_LDFLAGS="-L$LIBS_TMP -lkrun -Wl,-rpath,\$ORIGIN/../lib" \
      go build \
        -tags libkrun \
        -o "$EMBED_DIR/nexus-libkrun-vm" \
        ./cmd/nexus-libkrun-vm
    echo "  → rebuilt nexus-libkrun-vm from source ($(du -sh "$EMBED_DIR/nexus-libkrun-vm" | cut -f1))"

    # Build the kernel ELF from source and embed it in the nexus binary.
    # This replaces the slow/unreliable runtime download from Firecracker S3.
    echo "Building kernel from source for embed..."
    KERNEL_ASSET="$EMBED_DIR/assets/vmlinux"
    bash "$REPO_ROOT/scripts/nexus/build-kernel.sh" "$KERNEL_ASSET"
    echo "  → kernel built ($(du -sh "$KERNEL_ASSET" | cut -f1))"
  else
    echo "Building nexus with prebuilt nexus-libkrun-vm embed..."
  fi
else
  echo "nexus-libkrun-vm source not found — building without libkrun embed"
fi

# shellcheck disable=SC2086
CGO_ENABLED=0 go build $BUILD_TAGS -o /tmp/nexus-bin ./cmd/nexus/
rm -f "$EMBED_DIR/libkrun-embed.so" "$EMBED_DIR/libkrunfw-embed.so" "$EMBED_DIR/passt-embed"
# Preserve the compiled kernel in CI so the actions/cache post-step can save it.
# Local builds clean it up to avoid a dirty working tree.
if [[ -z "${CI:-}" ]]; then
  rm -f "$EMBED_DIR/assets/vmlinux"
fi

if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "NEXUS_E2E_BINARY=/tmp/nexus-bin" >> "$GITHUB_ENV"
fi
popd >/dev/null
