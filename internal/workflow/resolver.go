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

// Resolve loads a workflow by name using project → user → built-in cascade (v0.0.4 paths).
func Resolve(name string, projectRoot string) (*ResolvedWorkflow, error) {
	var searched []string

	primaryDir := brand.StateDir()
	primarySubdir := "workflows"
	if brand.Lower() != "gump" {
		primarySubdir = "recipes"
	}

	if projectRoot != "" {
		p := filepath.Join(projectRoot, primaryDir, primarySubdir, name+".yaml")
		searched = append(searched, p)
		if raw, err := os.ReadFile(p); err == nil {
			return &ResolvedWorkflow{Name: name, Source: "project", Path: p, Raw: raw}, nil
		}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, primaryDir, primarySubdir, name+".yaml")
		searched = append(searched, p)
		if raw, err := os.ReadFile(p); err == nil {
			return &ResolvedWorkflow{Name: name, Source: "user", Path: p, Raw: raw}, nil
		}
	}

	if brand.Lower() == "gump" {
		if projectRoot != "" {
			p := filepath.Join(projectRoot, ".pudding/recipes", name+".yaml")
			searched = append(searched, p)
			if raw, err := os.ReadFile(p); err == nil {
				fmt.Fprintf(os.Stderr, "warning: workflow %q not found in .gump/workflows/, falling back to legacy .pudding/recipes/ (compat)\n", name)
				return &ResolvedWorkflow{Name: name, Source: "project", Path: p, Raw: raw}, nil
			}
		}
		if home != "" {
			p := filepath.Join(home, ".pudding/recipes", name+".yaml")
			searched = append(searched, p)
			if raw, err := os.ReadFile(p); err == nil {
				fmt.Fprintf(os.Stderr, "warning: workflow %q not found in .gump/workflows/, falling back to legacy .pudding/recipes/ (compat)\n", name)
				return &ResolvedWorkflow{Name: name, Source: "user", Path: p, Raw: raw}, nil
			}
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
