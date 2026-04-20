package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/localws"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/mirror"
	cliprofile "github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func forkCommand() *cobra.Command {
	var childName string
	var childRef string

	cmd := &cobra.Command{
		Use:   "fork <workspace>",
		Short: "Fork a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if childRef == "" {
				return fmt.Errorf("--ref is required")
			}
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return err
			}
			defer conn.Close()

			parentID, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, args[0])
			if err != nil {
				return err
			}

			var result struct {
				Workspace workspace.Workspace `json:"workspace"`
			}
			params := map[string]any{
				"id":                 parentID,
				"childWorkspaceName": childName,
				"childRef":           childRef,
			}
			if err := rpc.Do(conn, "workspace.fork", params, &result); err != nil {
				return fmt.Errorf("workspace fork: %w", err)
			}

			child := result.Workspace
			fmt.Fprintf(cmd.OutOrStdout(), "forked workspace %s  (id: %s)\n", child.WorkspaceName, child.ID)

			// ── Mac-side git worktree ─────────────────────────────────────
			// Look up the parent's git root from client-side state. If found,
			// create a new worktree at that ref so the user has a local copy
			// checked out to the forked branch.
			parentRec, ok := localws.GetRecord(parentID)
			if !ok {
				// No local state for parent — nothing to set up on Mac side.
				return nil
			}

			gitRoot := parentRec.GitRoot
			// Worktrees live at <gitRoot>/.worktrees/<name> so they stay
			// alongside the project rather than in a global ~/nexus-workspaces.
			worktreesDir := filepath.Join(gitRoot, ".worktrees")
			worktreePath := filepath.Join(worktreesDir, child.WorkspaceName)

			if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not create .worktrees dir: %v\n", err)
				return nil
			}
			// Keep .worktrees/ out of git status/log without touching .gitignore.
			addToGitExclude(gitRoot, ".worktrees/")

			fmt.Fprintf(cmd.OutOrStdout(), "creating worktree %s at %s…\n", childRef, worktreePath)
			if err := gitWorktreeAdd(gitRoot, worktreePath, childRef); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: git worktree add failed: %v\n", err)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "local worktree:  %s\n", worktreePath)

			// ── Mirror worktree to remote (if SSH profile) ────────────────
			if p, err := cliprofile.LoadDefault(); err == nil && p.Host != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "mirroring %s to %s…\n", worktreePath, p.Host)
				_, mirrorErr := mirror.Ensure(mirror.Spec{
					LocalPath: worktreePath,
					ProjectID: filepath.Base(worktreePath),
					SSHTarget: p.Host,
					SSHPort:   p.SSHPort,
				})
				if mirrorErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: mirror failed: %v\n", mirrorErr)
				}
			}

			// ── Save client-side state ────────────────────────────────────
			rec := localws.WorkspaceRecord{
				WorkspaceID:   child.ID,
				WorkspaceName: child.WorkspaceName,
				LocalPath:     worktreePath,
				GitRoot:       gitRoot,
				IsWorktree:    true,
			}
			if err := localws.SaveRecord(rec); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not save local workspace state: %v\n", err)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&childName, "name", "", "child workspace name (default: <parent>-fork)")
	cmd.Flags().StringVar(&childRef, "ref", "", "branch/ref for the fork (required)")
	return cmd
}

// addToGitExclude appends entry to <gitRoot>/.git/info/exclude if not already
// present. This keeps the path out of git status without modifying .gitignore.
func addToGitExclude(gitRoot, entry string) {
	excludePath := filepath.Join(gitRoot, ".git", "info", "exclude")
	_ = os.MkdirAll(filepath.Dir(excludePath), 0o755)

	data, _ := os.ReadFile(excludePath)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return // already present
		}
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "\n# nexus fork worktrees\n%s\n", entry)
}

// gitWorktreeAdd runs "git worktree add <path> <ref>" from the given git root.
func gitWorktreeAdd(gitRoot, worktreePath, ref string) error {
	// Remove stale path if it exists but is not a valid git worktree.
	if _, err := os.Stat(worktreePath); err == nil {
		check := exec.Command("git", "rev-parse", "--is-inside-work-tree")
		check.Dir = worktreePath
		if check.Run() != nil {
			if err := os.RemoveAll(worktreePath); err != nil {
				return fmt.Errorf("remove stale worktree path: %w", err)
			}
		} else {
			// Already a valid worktree — nothing to do.
			return nil
		}
	}

	out, err := exec.Command("git", "-C", gitRoot, "worktree", "add", worktreePath, ref).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add %s %s: %w\n%s", worktreePath, ref, err, out)
	}
	return nil
}
