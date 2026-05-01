package bundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/vm/runner"
	"github.com/spf13/cobra"
)

// runCommand implements `nexus bundle run <bundlepath> [args...]`.
//
// This is invoked by the shell stub embedded in every self-executing NXPACK
// bundle: `exec nexus bundle run "$0" "$@"`.
//
// Behaviour:
//  1. Extract the NXPACK bundle to ~/.cache/nexus/bundles/<hash>/ (idempotent)
//  2. If workspaceIntent.Bake is non-empty and not yet stamped, run bake inside VM
//  3. Run workspaceIntent.Up inside the VM (daemonless via libkrun)
func runCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <bundle.nxbundle> [command [args...]]",
		Short: "Run a self-executing workspace bundle",
		Long: `Run a self-executing NXPACK workspace bundle.

The bundle is extracted to ~/.cache/nexus/bundles/<id>/ on first run (idempotent).
Workspace commands run inside an isolated microVM — no nexus daemon required.

If workspaceIntent.Bake commands are defined and have not yet run for this
extracted bundle, they are executed inside the VM first (one-time setup).
Then workspaceIntent.Up commands are run inside the VM.

If additional arguments are provided they are executed inside the VM instead
of the workspace.up intent.

This command is typically invoked automatically by the bundle's shell stub:
  ./myworkspace.nxbundle [args...]
`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundlePath := args[0]
			runArgs := args[1:]

			// Resolve absolute path early (stub passes $0 which may be relative).
			abs, err := filepath.Abs(bundlePath)
			if err != nil {
				return fmt.Errorf("bundle run: resolve path: %w", err)
			}
			bundlePath = abs

			r := runner.Runner{}

			// Extract bundle (idempotent).
			eb, err := r.ExtractBundle(bundlePath)
			if err != nil {
				return fmt.Errorf("bundle run: extract: %w", err)
			}

			ctx := context.Background()

			// Run bake commands inside VM (first-time only).
			if err := runBake(ctx, r, eb); err != nil {
				return err
			}

			// If caller passed explicit args, run those inside the VM.
			if len(runArgs) > 0 {
				return r.Run(ctx, eb, runArgs)
			}

			// No args: run workspace.up intent inside the VM.
			// Passing nil lets the runner use WorkspaceIntent.Up from the manifest.
			return r.Run(ctx, eb, nil)
		},
	}
	return cmd
}

// bakeStampPath returns the path to the bake completion stamp file.
func bakeStampPath(cacheDir string) string {
	return filepath.Join(cacheDir, ".baked")
}

// runBake executes workspaceIntent.Bake commands inside the VM if they have
// not already been run for this extracted bundle. The bake stamp file
// (~/.cache/nexus/bundles/<hash>/.baked) prevents re-running on subsequent
// invocations.
func runBake(ctx context.Context, r runner.Runner, eb runner.ExtractedBundle) error {
	bake := eb.Manifest.WorkspaceIntent.Bake
	if len(bake) == 0 {
		return nil
	}

	stamp := bakeStampPath(eb.CacheDir)
	if _, err := os.Stat(stamp); err == nil {
		// Already baked.
		return nil
	}

	fmt.Fprintln(os.Stderr, "bundle run: running bake commands (first-time setup)...")
	for _, c := range bake {
		fmt.Fprintf(os.Stderr, "bundle run: bake: %s\n", c)
	}

	// Run all bake commands as a single shell invocation inside the VM.
	bakeCmd := buildShellCmd(bake)
	if err := r.Run(ctx, eb, bakeCmd); err != nil {
		return fmt.Errorf("bundle run: bake failed: %w", err)
	}

	// Write bake stamp so subsequent runs skip bake.
	if err := os.WriteFile(stamp, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("bundle run: write bake stamp: %w", err)
	}
	return nil
}

// buildShellCmd wraps a slice of shell commands into a single ["sh", "-c", "cmd1 && cmd2 ..."].
func buildShellCmd(cmds []string) []string {
	if len(cmds) == 0 {
		return nil
	}
	joined := cmds[0]
	for _, c := range cmds[1:] {
		joined += " && " + c
	}
	return []string{"/bin/sh", "-c", joined}
}
