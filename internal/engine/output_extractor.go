package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/pudding/internal/plan"
)

// Outputs live under .pudding/out/ so they are gitignored and not snapshotted; the engine reads them before snapshot and stores content in the State Bag.
const outDir = ".pudding/out"
const planFile = ".pudding/out/plan.json"
const artifactFile = ".pudding/out/artifact.txt"

// PrepareOutputDir creates and empties .pudding/out/ in the worktree so each step writes into a clean dir.
func PrepareOutputDir(worktreeDir string) error {
	dir := filepath.Join(worktreeDir, outDir)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// ExtractPlanOutput reads and parses .pudding/out/plan.json. No fallback to stdout.
func ExtractPlanOutput(worktreeDir string) ([]plan.Task, string, error) {
	p := filepath.Join(worktreeDir, planFile)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("plan step did not produce .pudding/out/plan.json")
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

// ExtractArtifactOutput reads .pudding/out/artifact.txt.
func ExtractArtifactOutput(worktreeDir string) (string, error) {
	p := filepath.Join(worktreeDir, artifactFile)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("artifact step did not produce .pudding/out/artifact.txt")
		}
		return "", err
	}
	return string(data), nil
}
