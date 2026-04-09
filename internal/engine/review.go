package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
)

// ParseReviewJSON reads validate.json (v0.0.4) or legacy review.json for gate and retry semantics.
func ParseReviewJSON(worktreeDir string) (pass bool, comment string, raw string, err error) {
	out := outDirPath(worktreeDir)
	try := func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(out, name))
	}
	var data []byte
	var pathUsed string
	if b, err := try("validate.json"); err == nil {
		data, pathUsed = b, "validate.json"
	} else if b, err := try("review.json"); err == nil {
		data, pathUsed = b, "review.json"
	} else {
		return false, "", "", fmt.Errorf("validate.json or review.json not found in %s/out/", brand.StateDir())
	}
	raw = string(data)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return false, "", raw, fmt.Errorf("invalid %s: %w", pathUsed, err)
	}
	pv, ok := m["pass"]
	if !ok {
		return false, "", raw, fmt.Errorf("invalid %s: 'pass' must be a boolean", pathUsed)
	}
	pass, ok = pv.(bool)
	if !ok {
		return false, "", raw, fmt.Errorf("invalid %s: 'pass' must be a boolean", pathUsed)
	}
	cv, ok := m["comment"]
	if !ok {
		cv, ok = m["comments"]
	}
	if !ok {
		return false, "", raw, fmt.Errorf("invalid %s: need comment or comments string", pathUsed)
	}
	comment, ok = cv.(string)
	if !ok {
		return false, "", raw, fmt.Errorf("invalid %s: comment/comments must be a string", pathUsed)
	}
	return pass, comment, raw, nil
}
