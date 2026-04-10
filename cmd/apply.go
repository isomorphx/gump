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

var (
	applyRunID       string
	applyRunIDLegacy string
)

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Merge the last completed run into the current branch",
	Long:  "Resolves the most recent run with status 'pass' (or --run <uuid>), verifies worktree exists and working dir is clean, then runs git merge and teardown.",
	RunE:  runApply,
}

func init() {
	legacyRunFlag := "co" + "ok"
	applyCmd.Flags().StringVar(&applyRunID, "run", "", "Run UUID to apply (default: latest pass)")
	applyCmd.Flags().StringVar(&applyRunIDLegacy, legacyRunFlag, "", "Deprecated alias for --run")
	_ = applyCmd.Flags().MarkDeprecated(legacyRunFlag, "use --run instead")
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
	runsDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())

	runID := applyRunID
	if runID == "" && applyRunIDLegacy != "" {
		fmt.Fprintln(os.Stderr, "warning: --"+("co"+"ok")+" is deprecated, use --run instead")
		runID = applyRunIDLegacy
	}
	if runID == "" {
		runID, err = run.FindLatestPassingRun(runsDir)
		if err != nil {
			return err
		}
		if runID == "" {
			return fmt.Errorf("no completed run found to apply")
		}
	}

	c, err := run.LoadRunFromDir(repoRoot, runID)
	if err != nil {
		return err
	}
	if c.Status != "pass" {
		return fmt.Errorf("run %s has status %s — only completed runs can be applied", runID, c.Status)
	}
	if !run.WorktreeExists(repoRoot, runID) {
		return fmt.Errorf("worktree for run %s has been cleaned up — cannot apply", runID)
	}
	return c.Apply()
}
