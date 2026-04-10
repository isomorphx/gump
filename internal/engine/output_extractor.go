package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/plan"
)

func outDirPath(worktreeDir string) string {
	// WHY: build brand-aware paths so gump mode never creates legacy state dirs.
	return filepath.Join(worktreeDir, brand.StateDir(), "out")
}

// PrepareOutputDir creates and empties .gump/out/ in the worktree so each step writes into a clean dir.
func PrepareOutputDir(worktreeDir string) error {
	dir := outDirPath(worktreeDir)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// ExtractPlanOutput reads and parses .gump/out/plan.json. No fallback to stdout.
func ExtractPlanOutput(worktreeDir string) ([]plan.Task, string, error) {
	p := filepath.Join(outDirPath(worktreeDir), "plan.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("plan step did not produce %s/out/plan.json", brand.StateDir())
		}
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("plan step did not produce valid JSON output")
	}
	tasks, err := plan.ParsePlanOutput(data)
	if err != nil {
		return nil, "", err
	}
	return tasks, string(data), nil
}

// ExtractArtifactOutput reads .gump/out/artifact.txt.
func ExtractArtifactOutput(worktreeDir string) (string, error) {
	p := filepath.Join(outDirPath(worktreeDir), "artifact.txt")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("artifact step did not produce %s/out/artifact.txt", brand.StateDir())
		}
		return "", err
	}
	return string(data), nil
}
