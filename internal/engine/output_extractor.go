package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/pudding/internal/plan"
)

// Outputs live under .pudding/out/ so they are gitignored and not snapshotted; the engine reads them before snapshot and stores content in the State Bag.
const outDir = ".pudding/out"
const planFile = ".pudding/out/plan.json"
const artifactFile = ".pudding/out/artifact.txt"
const reviewFile = ".pudding/out/review.json"

// ReviewOutput is the agent contract for output: review steps (stored in State Bag as raw JSON).
type ReviewOutput struct {
	Pass    bool
	Comment string
	Raw     string
}

// ExtractReviewOutput reads and parses .pudding/out/review.json so review steps feed the State Bag and gate retries.
func ExtractReviewOutput(worktreeDir string) (ReviewOutput, error) {
	p := filepath.Join(worktreeDir, reviewFile)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ReviewOutput{}, fmt.Errorf("review step did not produce .pudding/out/review.json")
		}
		return ReviewOutput{}, err
	}
	var v struct {
		Pass    bool   `json:"pass"`
		Comment string `json:"comment"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return ReviewOutput{}, fmt.Errorf("review step produced invalid review.json: %w", err)
	}
	return ReviewOutput{Pass: v.Pass, Comment: v.Comment, Raw: string(data)}, nil
}

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
