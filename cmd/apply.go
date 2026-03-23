package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/cook"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/spf13/cobra"
)

var (
	applyRunID        string
	applyCookIDLegacy string
)

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Merge the last completed run into the current branch",
	Long:  "Resolves the most recent run with status 'pass' (or --run <uuid>), verifies worktree exists and working dir is clean, then runs git merge and teardown.",
	RunE:  runApply,
}

func init() {
	applyCmd.Flags().StringVar(&applyRunID, "run", "", "Run UUID to apply (default: latest pass)")
	applyCmd.Flags().StringVar(&applyCookIDLegacy, "cook", "", "Deprecated alias for --run")
	_ = applyCmd.Flags().MarkDeprecated("cook", "use --run instead")
	rootCmd.AddCommand(applyCmd)
}


func runApply(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()
	if cwd == "" {
		return fmt.Errorf("cannot determine working directory")
	}
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("gump apply must be executed inside a git repository")
	}
	cooksDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())

	cookID := applyRunID
	if cookID == "" && applyCookIDLegacy != "" {
		fmt.Fprintln(os.Stderr, "warning: --cook is deprecated, use --run instead")
		cookID = applyCookIDLegacy
	}
	if cookID == "" {
		cookID, err = cook.FindLatestPassingCook(cooksDir)
		if err != nil {
			return err
		}
		if cookID == "" {
			return fmt.Errorf("no completed run found to apply")
		}
	}

	c, err := cook.LoadCookFromDir(repoRoot, cookID)
	if err != nil {
		return err
	}
	if c.Status != "pass" {
		return fmt.Errorf("run %s has status %s — only completed runs can be applied", cookID, c.Status)
	}
	if !cook.WorktreeExists(repoRoot, cookID) {
		return fmt.Errorf("worktree for run %s has been cleaned up — cannot apply", cookID)
	}
	return c.Apply()
}
