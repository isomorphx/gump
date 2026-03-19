package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/pudding/internal/cook"
	"github.com/isomorphx/pudding/internal/sandbox"
	"github.com/spf13/cobra"
)

var gcKeepLast int

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Remove old cooks and their worktrees",
	Long:  "Keeps the N most recent cooks (by updated_at). For each removed cook: remove worktree and branch if present, then delete the cook directory. Never removes cooks with status 'running'.",
	RunE:  runGC,
}

func init() {
	gcCmd.Flags().IntVar(&gcKeepLast, "keep-last", 5, "Number of most recent cooks to keep")
	rootCmd.AddCommand(gcCmd)
}


func runGC(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()
	if cwd == "" {
		return fmt.Errorf("cannot determine working directory")
	}
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("pudding gc must be run inside a git repository")
	}
	cooksDir := filepath.Join(repoRoot, ".pudding", "cooks")
	entries, err := cook.ListCooks(cooksDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("Cleaned 0 cooks. Kept 0 most recent.")
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
	var toRemove []cook.CookEntry
	if keep < len(entries) {
		toRemove = entries[keep:]
	}
	cleaned := 0
	for _, e := range toRemove {
		if e.Status == "running" {
			fmt.Fprintf(os.Stderr, "Skipping cook %s (still running)\n", e.ID)
			continue
		}
		wtPath := cook.WorktreePath(repoRoot, e.ID)
		if _, err := os.Stat(wtPath); err == nil {
			if err := sandbox.RemoveWorktree(repoRoot, wtPath, "pudding/cook-"+e.ID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v\n", e.ID, err)
			}
		}
		dir := cook.CookDir(repoRoot, e.ID)
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove cook dir %s: %v\n", e.ID, err)
		} else {
			cleaned++
		}
	}
	kept := len(toKeep)
	fmt.Printf("Cleaned %d cooks. Kept %d most recent.\n", cleaned, kept)
	return nil
}
