package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/run"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/spf13/cobra"
)

var gcKeepLast int

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Remove old runs and their worktrees",
	Long:  "Keeps the N most recent runs (by updated_at). For each removed run: remove worktree and branch if present, then delete the run directory. Never removes runs with status 'running'.",
	RunE:  runGC,
}

func init() {
	gcCmd.Flags().IntVar(&gcKeepLast, "keep-last", 5, "Number of most recent runs to keep")
	rootCmd.AddCommand(gcCmd)
}


func runGC(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()
	if cwd == "" {
		return fmt.Errorf("cannot determine working directory")
	}
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("gump gc must be run inside a git repository")
	}
	cooksDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())
	entries, err := run.ListRuns(cooksDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("Cleaned 0 runs. Kept 0 most recent.")
		return nil
	}
	keep := gcKeepLast
	if keep < 0 {
		keep = 0
	}
	toKeep := entries
	if len(entries) > keep {
		toKeep = entries[:keep]
	}
	var toRemove []run.RunEntry
	if keep < len(entries) {
		toRemove = entries[keep:]
	}
	cleaned := 0
	for _, e := range toRemove {
		if e.Status == "running" {
			fmt.Fprintf(os.Stderr, "Skipping run %s (still running)\n", e.ID)
			continue
		}
		wtPath := run.WorktreePath(repoRoot, e.ID)
		if _, err := os.Stat(wtPath); err == nil {
			if err := sandbox.RemoveWorktree(repoRoot, wtPath, brand.WorktreeBranchPrefix()+e.ID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v\n", e.ID, err)
			}
		}
		dir := run.RunPath(repoRoot, e.ID)
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove run dir %s: %v\n", e.ID, err)
		} else {
			cleaned++
		}
	}
	kept := len(toKeep)
	fmt.Printf("Cleaned %d runs. Kept %d most recent.\n", cleaned, kept)
	return nil
}
