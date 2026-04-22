package sandbox

import (
	"fmt"
	"os/exec"
	"strings"
)

// ForkWorktree creates a new git worktree at childPath from the repository
// rooted at parentPath. The child worktree starts at the same commit as the
// parent's current HEAD and includes all staged and unstaged working-tree
// changes so the fork is a faithful replica of the parent's in-progress state.
//
// Implementation:
//  1. Create a detached worktree at HEAD (committed state).
//  2. Generate a unified diff of all working-tree changes (staged + unstaged).
//  3. Apply the diff in the child worktree (3-way merge, ignore whitespace).
//
// New/untracked files that are NOT git-tracked are intentionally excluded
// because git diff only covers tracked files.  This matches the behaviour most
// developers would expect for a "fork this branch" operation.
func ForkWorktree(parentPath, childPath string) error {
	// Step 1: create the worktree at HEAD.
	addCmd := exec.Command("git", "worktree", "add", "--detach", childPath, "HEAD") //nolint:gosec
	addCmd.Dir = parentPath
	out, err := addCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w\n%s", err, out)
	}

	// Step 2: capture all working-tree changes (staged + unstaged).
	// --diff-filter=ACDMRT excludes unmerged / ignored paths.
	diffCmd := exec.Command("git", "diff", "HEAD") //nolint:gosec
	diffCmd.Dir = parentPath
	diff, err := diffCmd.Output()
	if err != nil {
		// git diff exits non-zero only on errors, not when there are changes.
		return fmt.Errorf("git diff HEAD: %w", err)
	}
	if len(strings.TrimSpace(string(diff))) == 0 {
		// Nothing to apply — worktree is already an exact copy of HEAD.
		return nil
	}

	// Step 3: apply the diff in the child worktree.
	applyCmd := exec.Command("git", "apply", "--3way", "--whitespace=nowarn", "-") //nolint:gosec
	applyCmd.Dir = childPath
	applyCmd.Stdin = strings.NewReader(string(diff))
	applyOut, err := applyCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply working-tree changes to fork: %w\n%s", err, applyOut)
	}
	return nil
}
