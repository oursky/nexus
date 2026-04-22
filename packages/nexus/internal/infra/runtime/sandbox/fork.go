package sandbox

import (
	"fmt"
	"os/exec"
	"strings"
)

// ForkWorktree creates a new git worktree at childPath from the repository
// rooted at parentPath. When ref is non-empty the worktree checks out that
// branch/ref; otherwise it is detached at HEAD.
//
// In both cases the parent's uncommitted working-tree changes (staged +
// unstaged) are applied on top via a 3-way patch so the fork is a faithful
// replica of in-progress state.
//
// Implementation:
//  1. Create worktree at ref (or detached HEAD when ref is empty).
//  2. Generate a unified diff of parent's working-tree changes.
//  3. Apply the diff in the child worktree (3-way merge, ignore whitespace).
//
// Untracked files are intentionally excluded (git diff only covers tracked
// files), matching the behaviour developers expect for a "fork" operation.
func ForkWorktree(parentPath, childPath, ref string) error {
	// Step 1: create the worktree at the target ref or detached HEAD.
	var addCmd *exec.Cmd
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "HEAD" {
		addCmd = exec.Command("git", "worktree", "add", "--detach", childPath, "HEAD") //nolint:gosec
	} else {
		addCmd = exec.Command("git", "worktree", "add", childPath, ref) //nolint:gosec
	}
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
