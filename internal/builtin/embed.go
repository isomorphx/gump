// Package builtin registers embedded workflows and validators so CLI resolution can fall back when nothing exists on disk under .gump/.
package builtin

import (
	"embed"
	"io/fs"

	"github.com/isomorphx/gump/internal/workflow"
)

//go:embed workflows/*.yaml
var workflowsFS embed.FS

//go:embed validators/*.yaml
var validatorsFS embed.FS

func init() {
	workflow.BuiltinWorkflows = mustLoadYAMLDir(workflowsFS, "workflows")
	workflow.BuiltinValidators = mustLoadYAMLDir(validatorsFS, "validators")
}

func mustLoadYAMLDir(root embed.FS, sub string) map[string][]byte {
	out := make(map[string][]byte)
	dir, err := fs.Sub(root, sub)
	if err != nil {
		panic("builtin " + sub + ": " + err.Error())
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		panic("builtin " + sub + ": " + err.Error())
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		data, err := fs.ReadFile(dir, n)
		if err != nil {
			panic("builtin " + sub + "/" + n + ": " + err.Error())
		}
		out[n] = data
	}
	return out
}
