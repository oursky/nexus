package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
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

	var out []byte
	var err error
	if gitRefExists(gitRoot, ref) {
		out, err = exec.Command("git", "-C", gitRoot, "worktree", "add", worktreePath, ref).CombinedOutput()
	} else {
		// New branch name: `worktree add <path> <ref>` would fail with "invalid reference".
		out, err = exec.Command("git", "-C", gitRoot, "worktree", "add", "-b", ref, worktreePath, "HEAD").CombinedOutput()
	}
	if err != nil {
		return fmt.Errorf("git worktree add %s %s: %w\n%s", worktreePath, ref, err, out)
	}
	return nil
}

func gitRefExists(gitRoot, ref string) bool {
	cmd := exec.Command("git", "-C", gitRoot, "rev-parse", "--verify", ref)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}
