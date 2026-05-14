//go:build darwin

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/macvm"
)

// smolvmLibDir returns the default smolvm installation lib directory (dev fallback).
func smolvmLibDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".smolvm", "lib")
}

func resolveDarwinLibkrunPayload() (lib []byte, libfw []byte, err error) {
	if len(embeddedLibkrunDylib) > 0 && len(embeddedLibkrunfwDylib) > 0 {
		return embeddedLibkrunDylib, embeddedLibkrunfwDylib, nil
	}
	if len(embeddedLibkrunDylib) != 0 || len(embeddedLibkrunfwDylib) != 0 {
		return nil, nil, fmt.Errorf("incomplete embedded libkrun.dylib payload (rebuild with scripts/local/build-libkrun-darwin.sh)")
	}

	dir := smolvmLibDir()
	libPath := filepath.Join(dir, "libkrun.dylib")
	fwPath := filepath.Join(dir, "libkrunfw.dylib")
	lib, err = os.ReadFile(libPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read dev libkrun from %s: %w (embed empty; install smolvm or run build-libkrun-darwin.sh)", libPath, err)
	}
	libfw, err = os.ReadFile(fwPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read dev libkrunfw from %s: %w", fwPath, err)
	}
	return lib, libfw, nil
}

func installLibkrunDylibsDarwin(w io.Writer) (libDir string, err error) {
	libDir = filepath.Join(nexusDataShareDir(), "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return "", fmt.Errorf("create libkrun lib dir %s: %w", libDir, err)
	}
	libData, fwData, err := resolveDarwinLibkrunPayload()
	if err != nil {
		return "", err
	}

	destLib := filepath.Join(libDir, "libkrun.dylib")
	destFW := filepath.Join(libDir, "libkrunfw.dylib")
	if !needsInstall(destLib, libData) && !needsInstall(destFW, fwData) {
		return libDir, nil
	}

	fmt.Fprintf(w, "  extracting libkrun dylibs into %s...\n", libDir)
	if err := writeFileAtomic(destLib, libData, 0o755); err != nil {
		return "", fmt.Errorf("write libkrun.dylib: %w", err)
	}
	if err := writeFileAtomic(destFW, fwData, 0o755); err != nil {
		return "", fmt.Errorf("write libkrunfw.dylib: %w", err)
	}
	fmt.Fprintf(w, "  libkrun dylibs installed\n")
	return libDir, nil
}

func RunDarwinBootstrap(w io.Writer, emitJSON bool, driver string) error {
	if driver != "libkrun" && driver != "vm" && driver != "" {
		return nil
	}

	emitPhase(w, emitJSON, "preflight", "start", "checking macOS VM prerequisites")
	emitPhase(w, emitJSON, "preflight", "ok", "macOS Hypervisor.framework available")

	emitPhase(w, emitJSON, "asset-install", "start", "installing libkrun dylibs")
	libDir, err := installLibkrunDylibsDarwin(w)
	if err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: %w", err)
	}
	emitPhase(w, emitJSON, "asset-install", "ok", fmt.Sprintf("libkrun dylibs installed at %s", libDir))

	emitPhase(w, emitJSON, "runtime-verify", "start", "verifying dylibs")
	for _, name := range []string{"libkrun.dylib", "libkrunfw.dylib"} {
		p := filepath.Join(libDir, name)
		if _, err := os.Stat(p); err != nil {
			msg := fmt.Sprintf("%s not found at %s", name, libDir)
			emitPhase(w, emitJSON, "runtime-verify", "error", msg)
			return fmt.Errorf("runtime-verify: %s", msg)
		}
	}
	emitPhase(w, emitJSON, "runtime-verify", "ok", "libkrun dylibs verified")

	emitPhase(w, emitJSON, "kernel-install", "start", "extracting embedded VM kernel")
	kernelPath, err := extractEmbeddedKernel()
	if err != nil {
		emitPhase(w, emitJSON, "kernel-install", "error", err.Error())
		return fmt.Errorf("kernel-install: %w", err)
	}
	if kernelPath != "" {
		emitPhase(w, emitJSON, "kernel-install", "ok", fmt.Sprintf("kernel ready at %s", kernelPath))
	} else {
		emitPhase(w, emitJSON, "kernel-install", "ok", "skipped (no embedded kernel in this build)")
	}

	emitPhase(w, emitJSON, "rootfs-prefetch", "start", "ensuring base VM rootfs cache")
	rootcfg := macvm.DefaultManagerConfig()
	if prefetchErr := macvm.EnsureRootFS(context.Background(), rootcfg); prefetchErr != nil {
		emitPhase(w, emitJSON, "rootfs-prefetch", "error", prefetchErr.Error())
		fmt.Fprintf(w, "  warning: VM rootfs prefetch failed (will retry on workspace start): %v\n", prefetchErr)
	} else {
		emitPhase(w, emitJSON, "rootfs-prefetch", "ok", fmt.Sprintf("rootfs cache %s", rootcfg.RootFSCachePath))
	}
	return nil
}
