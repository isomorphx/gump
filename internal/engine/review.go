package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ParseReviewJSON reads .pudding/out/review.json with strict typing for gate and retry semantics.
func ParseReviewJSON(worktreeDir string) (pass bool, comment string, raw string, err error) {
	p := filepath.Join(outDirPath(worktreeDir), "review.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", "", fmt.Errorf("review.json not found in .pudding/out/")
		}
		return false, "", "", err
	}
	raw = string(data)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return false, "", raw, fmt.Errorf("invalid review.json: %w", err)
	}
	pv, ok := m["pass"]
	if !ok {
		return false, "", raw, fmt.Errorf("invalid review.json: 'pass' must be a boolean")
	}
	pass, ok = pv.(bool)
	if !ok {
		return false, "", raw, fmt.Errorf("invalid review.json: 'pass' must be a boolean")
	}
	cv, ok := m["comment"]
	if !ok {
		return false, "", raw, fmt.Errorf("invalid review.json: 'comment' must be a string")
	}
	comment, ok = cv.(string)
	if !ok {
		return false, "", raw, fmt.Errorf("invalid review.json: 'comment' must be a string")
	}
	return pass, comment, raw, nil
}
