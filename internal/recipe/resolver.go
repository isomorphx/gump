package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedRecipe carries source and path so the UI and future ledger can show provenance.
type ResolvedRecipe struct {
	Name   string
	Source string // "built-in", "project", or "user"
	Path   string
	Raw    []byte
}

// Resolve finds the first recipe by name so project and user can override built-ins without forking the binary.
func Resolve(name string, projectRoot string) (*ResolvedRecipe, error) {
	var searched []string

	// 1. Project: <repo-root>/.pudding/recipes/<name>.yaml
	if projectRoot != "" {
		p := filepath.Join(projectRoot, ".pudding", "recipes", name+".yaml")
		searched = append(searched, p)
		raw, err := os.ReadFile(p)
		if err == nil {
			return &ResolvedRecipe{Name: name, Source: "project", Path: p, Raw: raw}, nil
		}
	}

	// 2. User: ~/.pudding/recipes/<name>.yaml
	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, ".pudding", "recipes", name+".yaml")
		searched = append(searched, p)
		raw, err := os.ReadFile(p)
		if err == nil {
			return &ResolvedRecipe{Name: name, Source: "user", Path: p, Raw: raw}, nil
		}
	}

	// 3. Built-in
	raw, ok := BuiltinRecipes[name+".yaml"]
	if ok {
		return &ResolvedRecipe{Name: name, Source: "built-in", Path: "", Raw: raw}, nil
	}
	searched = append(searched, "built-in")

	return nil, fmt.Errorf("recipe %q not found. Searched: %v", name, searched)
}

// BuiltinRecipes is filled by internal/builtin via embed; resolver uses it so builtin stays decoupled from resolver.
var BuiltinRecipes = map[string][]byte{}

// ListBuiltinNames returns recipe names that are embedded (for cookbook list and doctor).
func ListBuiltinNames() []string {
	var names []string
	for k := range BuiltinRecipes {
		names = append(names, strings.TrimSuffix(k, ".yaml"))
	}
	return names
}
