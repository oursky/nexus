package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
)

// Exporter creates .nxbundle archives from a running workspace.
type Exporter struct{}

// NewExporter constructs an Exporter.
func NewExporter() *Exporter {
	return &Exporter{}
}

// Export looks up the named workspace via the daemon and writes a .nxbundle to outPath.
// The output path receives a ".nxbundle" suffix if not already present.
// It also writes a standalone runner shell script at the original outPath (without the .nxbundle suffix).
// Returns the runner script path and any error.
func (e *Exporter) Export(ctx context.Context, workspaceName, outPath string) (runnerPath string, err error) {
	// Save the runner path before adding the .nxbundle suffix.
	runnerPath = outPath
	if filepath.Ext(runnerPath) == ".nxbundle" {
		runnerPath = runnerPath[:len(runnerPath)-len(".nxbundle")]
	}

	// Ensure .nxbundle suffix.
	if filepath.Ext(outPath) != ".nxbundle" {
		outPath += ".nxbundle"
	}

	// Resolve workspace via daemon RPC.
	conn, connErr := rpc.EnsureDaemon()
	if connErr != nil {
		return "", fmt.Errorf("bundle: connect to daemon: %w", connErr)
	}
	defer conn.Close()

	wsID, wsIDErr := rpc.ResolveWorkspaceID(ctx, conn, workspaceName)
	if wsIDErr != nil {
		return "", fmt.Errorf("bundle: resolve workspace: %w", wsIDErr)
	}

	var wsResult struct {
		Workspace struct {
			WorkspaceName string `json:"workspaceName"`
			Ref           string `json:"ref"`
			Repo          string `json:"repo"`
			Backend       string `json:"backend"`
			RootPath      string `json:"rootPath"`
		} `json:"workspace"`
	}
	if infoErr := rpc.Do(conn, "workspace.info", map[string]any{"id": wsID}, &wsResult); infoErr != nil {
		return "", fmt.Errorf("bundle: fetch workspace info: %w", infoErr)
	}
	ws := wsResult.Workspace

	// Build manifest.
	now := time.Now().UTC().Format(time.RFC3339)
	manifest := BundleManifest{
		SchemaVersion: SchemaVersion,
		BundleVersion: BundleVersion,
		CreatedAt:     now,
		Source: SourceMetadata{
			WorkspaceName: ws.WorkspaceName,
			Ref:           ws.Ref,
			ProjectHint:   ws.Repo,
		},
		Compatibility: CompatibilityMeta{
			Arch:     []string{runtime.GOARCH},
			Backend:  []string{ws.Backend},
			OsFamily: []string{runtime.GOOS},
		},
		WorkspaceIntent: WorkspaceIntent{},
		Payload:         PayloadIndex{Entries: []PayloadEntry{}},
		Integrity: IntegrityMetadata{
			Algorithm: "sha256",
		},
	}

	// Attempt to populate WorkspaceIntent from project Nexusfile.
	if ws.RootPath != "" {
		intent, intentErr := WorkspaceIntentFromNexusfile(ws.RootPath)
		if intentErr == nil {
			manifest.WorkspaceIntent = intent
		}
	}

	// Serialise manifest without digest to get the canonical bytes.
	manifestBytes, marshalErr := MarshalManifest(manifest)
	if marshalErr != nil {
		return "", marshalErr
	}

	// Compute SHA256 of those canonical bytes and store the digest.
	h := sha256.New()
	h.Write(manifestBytes)
	manifest.Integrity.ManifestDigest = hex.EncodeToString(h.Sum(nil))

	// Re-serialise with digest filled in — these are the bytes stored in the bundle.
	// The importer must zero out the digest field, re-hash, and compare.
	finalBytes, finalErr := MarshalManifest(manifest)
	if finalErr != nil {
		return "", finalErr
	}

	// Write bundle as tar.gz using the final bytes (with digest).
	if tarErr := writeTarGz(outPath, finalBytes); tarErr != nil {
		return "", tarErr
	}

	// Write standalone runner shell script.
	if runnerErr := writeRunnerScript(runnerPath, outPath, ws.WorkspaceName, manifest.WorkspaceIntent); runnerErr != nil {
		return "", runnerErr
	}

	return runnerPath, nil
}

// writeRunnerScript writes a standalone shell script runner at dst.
// The runner executes workspace.up/down intent commands directly (CLI-129).
// workspace.init is executed once per imported runtime instance via a marker file (CLI-131).
// services[].start is NOT executed — it is deploy/runtime intent only (CLI-130).
func writeRunnerScript(dst, bundlePath, workspaceName string, intent WorkspaceIntent) error {
	upBody := shellCommandBody("up", intent.Up)
	downBody := shellCommandBody("down", intent.Down)
	initBody := shellCommandBody("init", intent.Init)

	script := "#!/bin/sh\n" +
		"# Nexus standalone workspace runner\n" +
		"# Workspace: " + workspaceName + "\n" +
		"# Bundle: " + bundlePath + "\n" +
		"# Intent preserved from Nexusfile (workspace.bake/init/up/down).\n" +
		"# services[].start is NOT executed — it is deploy-only intent (CLI-130).\n" +
		"set -e\n" +
		"\n" +
		"PROG=$(basename \"$0\")\n" +
		"WORKSPACE_NAME=\"" + workspaceName + "\"\n" +
		"STATE_DIR=\"${NEXUS_RUNNER_STATE_DIR:-${HOME}/.nexus/runner/" + workspaceName + "}\"\n" +
		"INIT_MARKER=\"${STATE_DIR}/.init-done\"\n" +
		"\n" +
		"# Embedded workspace.up intent (from exported Nexusfile).\n" +
		"do_up() {\n" + upBody + "}\n" +
		"\n" +
		"# Embedded workspace.down intent (from exported Nexusfile).\n" +
		"do_down() {\n" + downBody + "}\n" +
		"\n" +
		"# Embedded workspace.init intent — runs once per imported runtime instance.\n" +
		"do_init_commands() {\n" + initBody + "}\n" +
		"\n" +
		"do_init() {\n" +
		"  if [ -f \"${INIT_MARKER}\" ]; then\n" +
		"    echo \"runner: init: already completed (${INIT_MARKER})\"\n" +
		"    return 0\n" +
		"  fi\n" +
		"  mkdir -p \"${STATE_DIR}\"\n" +
		"  do_init_commands\n" +
		"  touch \"${INIT_MARKER}\"\n" +
		"  echo \"runner: init: completed\"\n" +
		"}\n" +
		"\n" +
		"usage() {\n" +
		"  cat <<EOF\n" +
		"Usage: $PROG <command> [args...]\n" +
		"\n" +
		"Standalone workspace runner for: $WORKSPACE_NAME\n" +
		"\n" +
		"Commands:\n" +
		"  run    Run a command in the workspace context\n" +
		"  start  Start the workspace (runs workspace.init once, then workspace.up)\n" +
		"  exec   Execute a command inside the workspace\n" +
		"  stop   Stop the workspace (executes workspace.down)\n" +
		"\n" +
		"Note: services[].start is deploy/runtime intent and is NOT executed by this runner.\n" +
		"\n" +
		"Flags:\n" +
		"  --help  Show this help message\n" +
		"EOF\n" +
		"}\n" +
		"\n" +
		"case \"$1\" in\n" +
		"  run|exec)\n" +
		"    shift\n" +
		"    if [ $# -eq 0 ]; then\n" +
		"      echo \"runner: exec requires a command\" >&2\n" +
		"      exit 2\n" +
		"    fi\n" +
		"    exec \"$@\"\n" +
		"    ;;\n" +
		"  start)\n" +
		"    do_init\n" +
		"    do_up\n" +
		"    ;;\n" +
		"  stop)\n" +
		"    do_down\n" +
		"    ;;\n" +
		"  --help|-h|\"\")\n" +
		"    usage\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  *)\n" +
		"    echo \"runner: unknown command: $1\" >&2\n" +
		"    usage >&2\n" +
		"    exit 2\n" +
		"    ;;\n" +
		"esac\n"

	//nolint:gosec // runner script must be executable (0755)
	return os.WriteFile(dst, []byte(script), 0o755)
}

// shellCommandBody generates the body of a shell function that executes each command
// in sequence, printing a trace line before each. Empty slices produce a no-op body.
func shellCommandBody(label string, cmds []string) string {
	if len(cmds) == 0 {
		return "  echo \"runner: " + label + ": no commands defined (no-op)\"\n"
	}
	body := ""
	for _, c := range cmds {
		// Use printf to print the trace, then eval the command.
		// Single-quote the command in the trace but eval it unquoted so the shell expands it.
		safe := shellSingleQuote(c)
		body += "  echo \"runner: " + label + ": " + safe + "\"\n"
		body += "  " + c + "\n"
	}
	return body
}

// shellSingleQuote escapes a string for safe inclusion inside a double-quoted shell string.
func shellSingleQuote(s string) string {
	// Replace any double-quote or backslash that would break the surrounding echo "...".
	result := ""
	for _, ch := range s {
		switch ch {
		case '"':
			result += "\\\""
		case '\\':
			result += "\\\\"
		case '$':
			result += "\\$"
		case '`':
			result += "\\`"
		default:
			result += string(ch)
		}
	}
	return result
}

// writeTarGz creates a .nxbundle (tar.gz) at dst containing manifest.json
// and a stub payload segment.
func writeTarGz(dst string, manifestBytes []byte) error {
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("bundle: create output file: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Write manifest.json.
	if err := writeTarEntry(tw, "manifest.json", manifestBytes); err != nil {
		return err
	}

	// Write stub payload segment (empty, signals future disk snapshot support).
	stub := []byte("# nexus bundle payload stub v1\n")
	if err := writeTarEntry(tw, "payload/workspace.stub", stub); err != nil {
		return err
	}

	return nil
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("bundle: write tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("bundle: write tar entry %s: %w", name, err)
	}
	return nil
}
