package sandbox

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/diff"
)

// Snapshot stages all changes in worktreeDir, commits with a structured message, and returns the diff contract.
// If there are no changes, returns an empty DiffContract (no error) so steps that touch nothing are valid.
// Structured commit messages let us correlate commits to steps for replay and reporting.
func Snapshot(worktreeDir, stepName, taskName string, attempt int) (*diff.DiffContract, error) {
	baseCommit, err := HeadCommit(worktreeDir)
	if err != nil {
		return nil, err
	}
	add := exec.Command("git", "add", "-A")
	add.Dir = worktreeDir
	if out, err := add.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git add -A: %w: %s", err, out)
	}
	// Check for staged changes so we can return empty contract instead of failing.
	quiet := exec.Command("git", "diff", "--cached", "--quiet")
	quiet.Dir = worktreeDir
	if err := quiet.Run(); err == nil {
		return &diff.DiffContract{BaseCommit: baseCommit, HeadCommit: baseCommit, Patch: "", FilesChanged: nil}, nil
	}
	patchCmd := exec.Command("git", "diff", "--cached")
	patchCmd.Dir = worktreeDir
	patchOut, err := patchCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}
	nameCmd := exec.Command("git", "diff", "--cached", "--name-only")
	nameCmd.Dir = worktreeDir
	nameOut, err := nameCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached --name-only: %w", err)
	}
	var filesChanged []string
	for _, line := range strings.Split(strings.TrimSpace(string(nameOut)), "\n") {
		if line != "" {
			filesChanged = append(filesChanged, line)
		}
	}
	task := taskName
	if task == "" {
		task = "-"
	}
	msg := fmt.Sprintf("[%s] step:%s task:%s attempt:%d", brand.Lower(), stepName, task, attempt)
	commit := exec.Command("git", "commit", "-m", msg)
	commit.Dir = worktreeDir
	if out, err := commit.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git commit: %w: %s", err, out)
	}
	headCommit, err := HeadCommit(worktreeDir)
	if err != nil {
		return nil, err
	}
	return &diff.DiffContract{
		BaseCommit:   baseCommit,
		HeadCommit:   headCommit,
		Patch:        string(patchOut),
		FilesChanged: filesChanged,
	}, nil
}

// FinalDiff returns the total diff between initialCommit and current HEAD in worktreeDir.
// Uses the frozen initial commit (not merge-base) so branch evolution during the cook does not change the diff.
func FinalDiff(worktreeDir, initialCommit string) (*diff.DiffContract, error) {
	patchCmd := exec.Command("git", "diff", initialCommit, "HEAD")
	patchCmd.Dir = worktreeDir
	patchOut, err := patchCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	nameCmd := exec.Command("git", "diff", initialCommit, "HEAD", "--name-only")
	nameCmd.Dir = worktreeDir
	nameOut, err := nameCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	headCommit, err := HeadCommit(worktreeDir)
	if err != nil {
		return nil, err
	}
	var filesChanged []string
	for _, line := range strings.Split(strings.TrimSpace(string(nameOut)), "\n") {
		if line != "" {
			filesChanged = append(filesChanged, line)
		}
	}
	return &diff.DiffContract{
		BaseCommit:   initialCommit,
		HeadCommit:   headCommit,
		Patch:        string(patchOut),
		FilesChanged: filesChanged,
	}, nil
}

// ParallelMergeExcludes are paths excluded from parallel worktree diff so merge only considers repo code
// (not <brand> state dir or provider context files).
var ParallelMergeExcludes = []string{brand.StateDir(), "AGENTS.md", "CLAUDE.md", "GEMINI.md", "QWEN.md"}

// FinalDiffExclude returns diff between initialCommit and HEAD excluding the given paths so parallel merge only considers repo files.
func FinalDiffExclude(worktreeDir, initialCommit string, excludePaths []string) (*diff.DiffContract, error) {
	args := []string{"diff", initialCommit, "HEAD", "--", "."}
	for _, p := range excludePaths {
		args = append(args, ":(exclude)"+p)
	}
	patchCmd := exec.Command("git", args...)
	patchCmd.Dir = worktreeDir
	patchOut, err := patchCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	nameArgs := []string{"diff", initialCommit, "HEAD", "--name-only", "--", "."}
	for _, p := range excludePaths {
		nameArgs = append(nameArgs, ":(exclude)"+p)
	}
	nameCmd := exec.Command("git", nameArgs...)
	nameCmd.Dir = worktreeDir
	nameOut, err := nameCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	headCommit, err := HeadCommit(worktreeDir)
	if err != nil {
		return nil, err
	}
	var filesChanged []string
	for _, line := range strings.Split(strings.TrimSpace(string(nameOut)), "\n") {
		if line != "" {
			filesChanged = append(filesChanged, line)
		}
	}
	return &diff.DiffContract{
		BaseCommit:   initialCommit,
		HeadCommit:   headCommit,
		Patch:        string(patchOut),
		FilesChanged: filesChanged,
	}, nil
}

// ApplyPatch applies a unified diff patch in worktreeDir; used when merging parallel worktrees into main.
func ApplyPatch(worktreeDir string, patch []byte) error {
	if len(patch) == 0 {
		return nil
	}
	cmd := exec.Command("git", "apply", "--whitespace=fix")
	cmd.Dir = worktreeDir
	cmd.Stdin = bytes.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git apply: %w: %s", err, out)
	}
	return nil
}
