package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
)

// ParseValidateJSON reads `.gump/out/validate.json` (R3 §4.6): `{ "pass": bool, "comments": string }`.
// Returns the pass bit, output strings "true"/"false" for the state bag, comments, raw JSON, and an error if missing or invalid.
func ParseValidateJSON(worktreeDir string) (pass bool, outStr string, comments string, raw string, err error) {
	p := filepath.Join(outDirPath(worktreeDir), "validate.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return false, "", "", "", fmt.Errorf("validate.json not found in %s/out/: %w", brand.StateDir(), err)
	}
	raw = string(data)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return false, "", "", raw, fmt.Errorf("invalid validate.json: %w", err)
	}
	pv, ok := m["pass"]
	if !ok {
		return false, "", "", raw, fmt.Errorf("invalid validate.json: 'pass' must be a boolean")
	}
	pass, ok = pv.(bool)
	if !ok {
		return false, "", "", raw, fmt.Errorf("invalid validate.json: 'pass' must be a boolean")
	}
	if pass {
		outStr = "true"
	} else {
		outStr = "false"
	}
	var cv interface{}
	var hasComment bool
	if cv, hasComment = m["comments"]; !hasComment {
		cv, hasComment = m["comment"]
	}
	if hasComment {
		s, ok := cv.(string)
		if !ok {
			return false, "", "", raw, fmt.Errorf("invalid validate.json: comments must be a string")
		}
		comments = s
	} else if !pass {
		return false, outStr, "", raw, fmt.Errorf("invalid validate.json: 'comments' required when pass is false")
	}
	return pass, outStr, comments, raw, nil
}
