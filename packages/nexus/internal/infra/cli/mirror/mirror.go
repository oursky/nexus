// Package mirror syncs a local directory to a remote daemon host using
// the embedded Mutagen binary. The convention matches ProjectMirrorSync.swift
// so that sessions created by the CLI and the Mac app are interchangeable.
//
// Remote path: ~/.local/share/nexus/mirrors/<slug>/
// Session name: nexus-mirror-<slug>
// Slug: projectID with "/" replaced by "-"
package mirror

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/mutagenbin"
)

// Spec describes a mirror to create or resume.
type Spec struct {
	// LocalPath is the absolute Mac-side directory to sync.
	LocalPath string
	// ProjectID is the nexus project id used to derive the session name and slug.
	ProjectID string
	// SSHTarget is the SSH destination, e.g. "newman@linuxbox".
	SSHTarget string
	// SSHPort overrides the SSH port; 0 means use the default (22).
	SSHPort int
	// Ignores is a list of paths/globs to exclude from the sync session.
	// Passed as --ignore flags to mutagen sync create.
	Ignores []string
}

// Result holds the outcome of Ensure.
type Result struct {
	// RemotePath is the absolute path on the remote host.
	RemotePath string
	// SessionName is the Mutagen session name.
	SessionName string
}

// Slug computes the project slug used in remote paths and session names.
func Slug(projectID string) string {
	return strings.ReplaceAll(projectID, "/", "-")
}

// SessionName returns the Mutagen session name for a project.
func SessionName(projectID string) string {
	return "nexus-mirror-" + Slug(projectID)
}

// RemoteMirrorPath returns the expected remote path for a project mirror.
// It does NOT contact the remote; call Ensure to create/verify the directory.
func RemoteMirrorPath(projectID string) string {
	return "~/.local/share/nexus/mirrors/" + Slug(projectID)
}

// Ensure creates the remote mirror directory (via SSH), then starts or resumes
// the Mutagen sync session, and flushes to wait for the initial sync to complete.
func Ensure(spec Spec) (*Result, error) {
	if spec.LocalPath == "" {
		return nil, fmt.Errorf("mirror: LocalPath is required")
	}
	if spec.ProjectID == "" {
		return nil, fmt.Errorf("mirror: ProjectID is required")
	}
	if spec.SSHTarget == "" {
		return nil, fmt.Errorf("mirror: SSHTarget is required")
	}

	if _, err := os.Stat(spec.LocalPath); err != nil {
		return nil, fmt.Errorf("mirror: local path %q: %w", spec.LocalPath, err)
	}

	slug := Slug(spec.ProjectID)
	session := "nexus-mirror-" + slug
	remoteDirExpr := "~/.local/share/nexus/mirrors/" + slug

	// 1. Prepare the remote directory and resolve its absolute path.
	remoteAbs, err := prepareRemoteDir(spec, remoteDirExpr)
	if err != nil {
		return nil, fmt.Errorf("mirror: prepare remote dir: %w", err)
	}

	// 2. Get the Mutagen binary (embedded on macOS/Linux amd64/arm64).
	mutagenPath, err := mutagenbin.Path()
	if err != nil {
		return nil, fmt.Errorf("mirror: %w", err)
	}

	// 3. Ensure Mutagen daemon is running.
	if err := ensureMutagenDaemon(mutagenPath); err != nil {
		return nil, fmt.Errorf("mirror: mutagen daemon: %w", err)
	}

	// 4. Create the session if it doesn't already exist.
	if !sessionExists(mutagenPath, session) {
		betaURL := spec.SSHTarget + ":" + remoteAbs
		if err := createSession(mutagenPath, session, spec.LocalPath, betaURL, spec.SSHPort, spec.Ignores); err != nil {
			return nil, fmt.Errorf("mirror: create session: %w", err)
		}
	}

	// 5. Flush — blocks until the current sync cycle completes.
	if err := flushSession(mutagenPath, session); err != nil {
		return nil, fmt.Errorf("mirror: flush session: %w", err)
	}

	return &Result{
		RemotePath:  remoteAbs,
		SessionName: session,
	}, nil
}

// Stop terminates the Mutagen session and removes the remote mirror directory.
// Errors are logged but do not halt the cleanup.
func Stop(spec Spec) error {
	slug := Slug(spec.ProjectID)
	session := "nexus-mirror-" + slug
	remoteDirExpr := "~/.local/share/nexus/mirrors/" + slug

	mutagenPath, mutagenErr := mutagenbin.Path()
	if mutagenErr == nil {
		args := []string{"sync", "terminate", session}
		cmd := exec.Command(mutagenPath, args...)
		cmd.Stdout = os.Stderr // log to stderr; non-fatal
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	if spec.SSHTarget != "" {
		rmArgs := sshArgs(spec.SSHTarget, spec.SSHPort,
			"rm -rf ~/.local/share/nexus/mirrors/"+slug)
		rmCmd := exec.Command("ssh", rmArgs...)
		rmCmd.Stdout = os.Stderr
		rmCmd.Stderr = os.Stderr
		_ = rmCmd.Run()
	}

	_ = remoteDirExpr // silence unused warning
	return nil
}

// ── internal helpers ─────────────────────────────────────────────────────────

func prepareRemoteDir(spec Spec, dirExpr string) (string, error) {
	bash := "mkdir -p " + dirExpr + " && cd " + dirExpr + " && pwd"
	args := sshArgs(spec.SSHTarget, spec.SSHPort, bash)
	out, err := exec.Command("ssh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("ssh: %w", err)
	}
	abs := strings.TrimSpace(string(out))
	if abs == "" || !strings.HasPrefix(abs, "/") {
		return "", fmt.Errorf("unexpected remote pwd output: %q", abs)
	}
	return abs, nil
}

func ensureMutagenDaemon(mutagenPath string) error {
	// Check if already running.
	probe := exec.Command(mutagenPath, "daemon", "status")
	probe.Stdout = nil
	probe.Stderr = nil
	if err := probe.Run(); err == nil {
		return nil // already running
	}
	// Start it.
	start := exec.Command(mutagenPath, "daemon", "start")
	start.Stdout = os.Stderr
	start.Stderr = os.Stderr
	return start.Run()
}

func sessionExists(mutagenPath, sessionName string) bool {
	out, err := exec.Command(mutagenPath, "sync", "list").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), sessionName)
}

func createSession(mutagenPath, sessionName, localPath, betaURL string, sshPort int, ignores []string) error {
	args := []string{
		"sync", "create",
		"--name", sessionName,
		"--sync-mode", "two-way-safe",
		"--watch-polling-interval", "5",
		"--symlink-mode", "ignore",
	}
	if sshPort > 0 && sshPort != 22 {
		args = append(args, "--ssh-port", fmt.Sprintf("%d", sshPort))
	}
	for _, ig := range ignores {
		args = append(args, "--ignore", ig)
	}
	args = append(args, localPath, betaURL)

	var stderr bytes.Buffer
	cmd := exec.Command(mutagenPath, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func flushSession(mutagenPath, sessionName string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(mutagenPath, "sync", "flush", sessionName)
	cmd.Stdout = os.Stderr
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func sshArgs(target string, port int, bash string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no"}
	if id := strings.TrimSpace(os.Getenv("NEXUS_E2E_SSH_IDENTITY")); id != "" {
		args = append(args, "-i", id)
	}
	if port > 0 && port != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", port))
	}
	// Pass the entire bash command as a single quoted argument so SSH doesn't
	// split it; otherwise 'bash -lc <cmd>' receives only the first word.
	args = append(args, target, "bash -lc '"+strings.ReplaceAll(bash, "'", "'\\''")+"'")
	return args
}
