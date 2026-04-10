package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
)

// ResolvedWorkflow records where a workflow was loaded from for provenance and error messages.
type ResolvedWorkflow struct {
	Name   string
	Source string
	Path   string
	Raw    []byte
}

// BuiltinWorkflows is populated from embedded YAML by internal/builtin.
var BuiltinWorkflows = map[string][]byte{}

// BuiltinValidators is embedded validator workflows (keys are filenames, e.g. arch-review.yaml).
var BuiltinValidators = map[string][]byte{}

// Resolve loads a workflow or validator path using project → user → built-in cascade (v0.0.4 only; no legacy paths).
func Resolve(name string, projectRoot string) (*ResolvedWorkflow, error) {
	name = strings.TrimSpace(name)
	var searched []string

	// Gate validate: paths use the validators/ prefix so files live under .gump/validators/, not under workflows/.
	if strings.HasPrefix(name, "validators/") {
		rel := strings.Trim(strings.TrimPrefix(name, "validators/"), "/")
		if rel == "" || strings.Contains(rel, "..") {
			return nil, fmt.Errorf("invalid validator reference %q", name)
		}
		rel = filepath.ToSlash(rel)
		vPath := filepath.Join("validators", filepath.FromSlash(rel)+".yaml")

		if projectRoot != "" {
			p := filepath.Join(projectRoot, brand.StateDir(), vPath)
			searched = append(searched, p)
			if raw, err := os.ReadFile(p); err == nil {
				return &ResolvedWorkflow{Name: name, Source: "project", Path: p, Raw: raw}, nil
			}
		}
		home, _ := os.UserHomeDir()
		if home != "" {
			p := filepath.Join(home, brand.StateDir(), vPath)
			searched = append(searched, p)
			if raw, err := os.ReadFile(p); err == nil {
				return &ResolvedWorkflow{Name: name, Source: "user", Path: p, Raw: raw}, nil
			}
		}
		key := filepath.Base(vPath)
		if raw, ok := BuiltinValidators[key]; ok {
			return &ResolvedWorkflow{Name: name, Source: "built-in", Path: "", Raw: raw}, nil
		}
		searched = append(searched, "built-in/validators")
		return nil, fmt.Errorf("workflow %q not found. Searched: %v", name, searched)
	}

	primaryDir := brand.StateDir()

	if projectRoot != "" {
		p := filepath.Join(projectRoot, primaryDir, "workflows", name+".yaml")
		searched = append(searched, p)
		if raw, err := os.ReadFile(p); err == nil {
			return &ResolvedWorkflow{Name: name, Source: "project", Path: p, Raw: raw}, nil
		}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, primaryDir, "workflows", name+".yaml")
		searched = append(searched, p)
		if raw, err := os.ReadFile(p); err == nil {
			return &ResolvedWorkflow{Name: name, Source: "user", Path: p, Raw: raw}, nil
		}
	}

	if raw, ok := BuiltinWorkflows[name+".yaml"]; ok {
		return &ResolvedWorkflow{Name: name, Source: "built-in", Path: "", Raw: raw}, nil
	}
	searched = append(searched, "built-in")

	return nil, fmt.Errorf("workflow %q not found. Searched: %v", name, searched)
}

// ListBuiltinNames returns embedded workflow names without extension.
func ListBuiltinNames() []string {
	var names []string
	for k := range BuiltinWorkflows {
		names = append(names, strings.TrimSuffix(k, ".yaml"))
	}
	return names
}
