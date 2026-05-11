//go:build linux

//nolint:unused
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── XDG user-scoped paths ─────────────────────────────────────────────────────

func xdgShareNexus() string {
	if s := os.Getenv("XDG_DATA_HOME"); s != "" {
		return filepath.Join(s, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}

func xdgStateNexus() string {
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(expandTilde(s), "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "nexus")
}

func expandTilde(path string) string {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func xdgLocalBin() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

func rootlessVMDir() string {
	return filepath.Join(xdgShareNexus(), "vm")
}

func rootlessRootlessDirState() string {
	return filepath.Join(xdgShareNexus(), "rootless")
}

// ── Canonical rootless asset paths ───────────────────────────────────────────

func rootlessKernelPath() string {
	return filepath.Join(rootlessVMDir(), "vmlinux.bin")
}

func rootlessRootfsPath() string {
	return filepath.Join(rootlessVMDir(), "rootfs.ext4")
}

// rootlessRootfsDirPath returns the directory-form of the rootfs used by
// libkrun container mode (krun_set_root). Created alongside rootfs.ext4.
func rootlessRootfsDirPath() string {
	return filepath.Join(rootlessVMDir(), "rootfs-dir")
}

func rootlessPasstPath() string {
	return filepath.Join(xdgLocalBin(), "passt")
}

// rootlessLibkrunLibDir returns the canonical location for libkrun shared
// libraries installed by the rootless bootstrap.
func rootlessLibkrunLibDir() string {
	return filepath.Join(xdgShareNexus(), "lib")
}

func rootlessStateFilePath() string {
	return filepath.Join(rootlessRootlessDirState(), "state.json")
}

func rootlessSubnetFilePath() string {
	return filepath.Join(xdgShareNexus(), "rootless", "bridge-subnet")
}

// smolvmSourceDir returns the default smolvm installation directory.
// smolvm (https://github.com/smol-machines/smolvm) ships libkrun + libkrunfw.
func smolvmSourceDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".smolvm", "lib")
}

// rootlessLibkrunBinDir returns the canonical location for the nexus-libkrun-vm
// helper binary extracted at bootstrap.
func rootlessLibkrunBinDir() string {
	return filepath.Join(xdgShareNexus(), "bin")
}

// LibkrunVMBinPath returns the path where the nexus-libkrun-vm binary is
// extracted during bootstrap. The manager uses this to spawn VMs.
func LibkrunVMBinPath() string {
	return filepath.Join(rootlessLibkrunBinDir(), "nexus-libkrun-vm")
}

// ── State model ───────────────────────────────────────────────────────────────

type rootlessState struct {
	SchemaVersion int    `json:"schema_version"`
	InstalledAt   string `json:"installed_at"`
	NexusVersion  string `json:"nexus_version"`
	// Driver is the runtime driver that was bootstrapped (currently always "libkrun").
	Driver            string `json:"driver,omitempty"`
	NetworkBackend    string `json:"network_backend"`
	NetworkBackendBin string `json:"network_backend_bin"`
	KernelPath        string `json:"kernel_path"`
	RootfsPath        string `json:"rootfs_path"`
	KVMAccess         string `json:"kvm_access"`
	LibkrunLibDir     string `json:"libkrun_lib_dir,omitempty"`
}

// ── Phase event (for --json output) ──────────────────────────────────────────

type bootstrapPhaseEvent struct {
	Phase   string `json:"phase"`
	Status  string `json:"status"` // "start" | "ok" | "error"
	Message string `json:"message,omitempty"`
}

func emitPhase(w io.Writer, emitJSON bool, phase, status, msg string) {
	if emitJSON {
		ev := bootstrapPhaseEvent{Phase: phase, Status: status, Message: msg}
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "%s\n", data)
	} else if msg != "" {
		fmt.Fprintf(w, "[%s] %s: %s\n", phase, status, msg)
	} else {
		fmt.Fprintf(w, "[%s] %s\n", phase, status)
	}
}

// ── Prerequisite checks ───────────────────────────────────────────────────────

func checkKVMAccess() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf(
			"prerequisite_error.kvm_access: /dev/kvm is not accessible (O_RDWR): %w\n\n"+
				"Remediation:\n"+
				"  sudo usermod -aG kvm $USER\n"+
				"  # Then log out and log back in (or run: newgrp kvm)",
			err,
		)
	}
	f.Close()
	return nil
}

func checkNetworkBackend() error {
	path := rootlessPasstPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if p, err := exec.LookPath("passt"); err == nil {
		_ = p
		return nil
	}
	return fmt.Errorf(
		"prerequisite_error.network_backend_missing: passt not found at %s or in PATH\n\n"+
			"Remediation: nexus daemon start will auto-install passt during asset-install phase",
		path,
	)
}

// ── Main entry point ──────────────────────────────────────────────────────────

// RunRootlessBootstrap runs preflight + asset-install + runtime-verify in sequence.
// On success, the daemon-launch phase can proceed.
// emitJSON controls whether phase events are emitted as JSON lines (for --json flag).
func RunRootlessBootstrap(w io.Writer, emitJSON bool, driver string) error {
	// ── Phase: preflight ───────────────────────────────────────────────────────
	emitPhase(w, emitJSON, "preflight", "start", "checking prerequisites")

	if err := checkKVMAccess(); err != nil {
		emitPhase(w, emitJSON, "preflight", "error", err.Error())
		return err
	}
	emitPhase(w, emitJSON, "preflight", "ok", "KVM accessible")

	// ── Phase: asset-install (idempotent) ─────────────────────────────────────
	emitPhase(w, emitJSON, "asset-install", "start", "installing runtime assets")

	if err := ensureRootlessDirs(); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: create dirs: %w", err)
	}

	// libkrun shared libraries.
	if err := installLibkrunLibsRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: libkrun: %w", err)
	}

	// passt is required for both drivers in VM mode networking.
	if err := installPasstRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: passt: %w", err)
	}

	if err := installVMKernelRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: kernel: %w", err)
	}

	// libkrun requires a block-backed rootfs image so guest
	// root behaves like a real VM filesystem (root can install arbitrary packages).
	if err := installVMRootfsRootless(w, emitJSON); err != nil {
		emitPhase(w, emitJSON, "asset-install", "error", err.Error())
		return fmt.Errorf("asset-install: rootfs: %w", err)
	}

	emitPhase(w, emitJSON, "asset-install", "ok", "all assets present")

	// ── Phase: runtime-verify ─────────────────────────────────────────────────
	emitPhase(w, emitJSON, "runtime-verify", "start", "verifying runtime")

	if err := verifyRootlessRuntime(driver); err != nil {
		emitPhase(w, emitJSON, "runtime-verify", "error", err.Error())
		return fmt.Errorf("runtime-verify: %w", err)
	}

	if err := persistRootlessState(driver); err != nil {
		// Non-fatal: log but don't fail startup
		emitPhase(w, emitJSON, "runtime-verify", "ok", fmt.Sprintf("verified (state persist skipped: %v)", err))
	} else {
		emitPhase(w, emitJSON, "runtime-verify", "ok", "runtime verified and state persisted")
	}

	// daemon-launch phase is emitted by the caller after this returns.
	return nil
}

// ── Directory setup ───────────────────────────────────────────────────────────

func ensureRootlessDirs() error {
	dirs := []string{
		xdgLocalBin(),
		rootlessVMDir(),
		rootlessRootlessDirState(),
		xdgStateNexus(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// ── Runtime verification ──────────────────────────────────────────────────────

// verifyRootlessRuntime confirms all runtime assets are present.
// For libkrun driver this means libkrun libs, passt, kernel and rootfs.
// For sandbox driver only the rootfs is checked (when applicable).
func verifyRootlessRuntime(driver string) error {
	isLibkrun := driver == "libkrun" || driver == ""

	if isLibkrun {
		// libkrun shared library.
		libDir := rootlessLibkrunLibDir()
		if _, err := os.Stat(filepath.Join(libDir, "libkrun.so.1")); err != nil {
			return fmt.Errorf("libkrun.so.1 not found at %s", libDir)
		}

		// Verify passt.
		passt := rootlessPasstPath()
		if _, err := os.Stat(passt); err != nil {
			return fmt.Errorf("passt not found at %s", passt)
		}

		// Verify kernel.
		if _, err := os.Stat(rootlessKernelPath()); err != nil {
			return fmt.Errorf("kernel not found at %s", rootlessKernelPath())
		}
	}

	// Verify rootfs.
	if _, err := os.Stat(rootlessRootfsPath()); err != nil {
		return fmt.Errorf("rootfs not found at %s", rootlessRootfsPath())
	}

	return nil
}

// ── State persistence ─────────────────────────────────────────────────────────

func persistRootlessState(driver string) error {
	effectiveDriver := driver
	if effectiveDriver == "" {
		effectiveDriver = "libkrun"
	}
	state := rootlessState{
		SchemaVersion:     1,
		InstalledAt:       time.Now().UTC().Format(time.RFC3339),
		Driver:            effectiveDriver,
		NetworkBackend:    "tsi", // built into libkrun
		NetworkBackendBin: "",
		KernelPath:        rootlessKernelPath(),
		RootfsPath:        rootlessRootfsPath(),
		KVMAccess:         "ok",
		LibkrunLibDir:     rootlessLibkrunLibDir(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rootlessStateFilePath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(rootlessStateFilePath(), data, 0o644)
}
