package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitRepoRoot returns the absolute path of the git repository root containing dir.
// Required so we create .gump and worktrees at the repo root, not in a random subdir.
// Uses -C so git runs in dir regardless of the process CWD (reliable under go test and subprocesses).
func GitRepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// HasUncommittedChanges reports whether the working tree in dir has uncommitted changes.
// Enforced before run/apply so we never mix local edits with gump branches.
func HasUncommittedChanges(dir string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return true, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ".gump/") {
			continue
		}
		return true, nil
	}
	return false, nil
}

// CurrentBranch returns the current branch name in dir (e.g. "main").
func CurrentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadCommit returns the full hash of HEAD in dir. Frozen at worktree creation for FinalDiff.
func HeadCommit(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateWorktree adds a new worktree at worktreeDir and creates branch branchName from HEAD.
// The dev repo stays untouched; all agent work happens in the new worktree.
func CreateWorktree(repoRoot, worktreeDir, branchName string) error {
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return err
	}
	cmd := exec.Command("git", "worktree", "add", worktreeDir, "-b", branchName)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, out)
	}
	return nil
}

// CreateWorktreeAtCommit creates a new worktree at worktreeDir checked out at fromCommit, so parallel branches start from the same tree state.
func CreateWorktreeAtCommit(repoRoot, worktreeDir, branchName, fromCommit string) error {
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return err
	}
	br := exec.Command("git", "branch", branchName, fromCommit)
	br.Dir = repoRoot
	if out, err := br.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch: %w: %s", err, out)
	}
	cmd := exec.Command("git", "worktree", "add", worktreeDir, branchName)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = exec.Command("git", "branch", "-D", branchName).Run()
		return fmt.Errorf("git worktree add: %w: %s", err, out)
	}
	return nil
}

// RemoveWorktree removes the worktree and deletes its branch so the main repo stays clean.
// Run dir (.gump/runs/<uuid>/) is left intact for ledger and artifacts.
func RemoveWorktree(repoRoot, worktreeDir, branchName string) error {
	rm := exec.Command("git", "worktree", "remove", "--force", worktreeDir)
	rm.Dir = repoRoot
	if out, err := rm.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, out)
	}
	br := exec.Command("git", "branch", "-D", branchName)
	br.Dir = repoRoot
	if out, err := br.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch -D: %w: %s", err, out)
	}
	return nil
}

// ResetTo resets the worktree to the given commit and removes untracked files so retry/replay start clean.
func ResetTo(worktreeDir, commitHash string) error {
	cmd := exec.Command("git", "reset", "--hard", commitHash)
	cmd.Dir = worktreeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %w: %s", err, out)
	}
	clean := exec.Command("git", "clean", "-fd")
	clean.Dir = worktreeDir
	if out, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("git clean: %w: %s", err, out)
	}
	return nil
}
