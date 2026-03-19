package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/pudding/internal/cook"
	"github.com/isomorphx/pudding/internal/sandbox"
	"github.com/spf13/cobra"
)

var applyCookID string

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Merge the last completed cook into the current branch",
	Long:  "Resolves the most recent cook with status 'pass' (or --cook <uuid>), verifies worktree exists and working dir is clean, then runs git merge and teardown.",
	RunE:  runApply,
}

func init() {
	applyCmd.Flags().StringVar(&applyCookID, "cook", "", "Cook UUID to apply (default: latest pass)")
	rootCmd.AddCommand(applyCmd)
}


func runApply(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()
	if cwd == "" {
		return fmt.Errorf("cannot determine working directory")
	}
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("pudding apply must be run inside a git repository")
	}
	cooksDir := filepath.Join(repoRoot, ".pudding", "cooks")

	cookID := applyCookID
	if cookID == "" {
		cookID, err = cook.FindLatestPassingCook(cooksDir)
		if err != nil {
			return err
		}
		if cookID == "" {
			return fmt.Errorf("no completed cook found to apply")
		}
	}

	c, err := cook.LoadCookFromDir(repoRoot, cookID)
	if err != nil {
		return err
	}
	if c.Status != "pass" {
		return fmt.Errorf("cook %s has status %s — only completed cooks can be applied", cookID, c.Status)
	}
	if !cook.WorktreeExists(repoRoot, cookID) {
		return fmt.Errorf("worktree for cook %s has been cleaned up — cannot apply", cookID)
	}
	return c.Apply()
}
