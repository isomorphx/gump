package builtin

import (
	"embed"
	"io/fs"

	"github.com/isomorphx/gump/internal/workflow"
)

//go:embed workflows/*.yaml
var workflowsFS embed.FS

func init() {
	workflow.BuiltinWorkflows = mustLoadWorkflows()
}

func mustLoadWorkflows() map[string][]byte {
	out := make(map[string][]byte)
	dir, err := fs.Sub(workflowsFS, "workflows")
	if err != nil {
		panic("builtin workflows: " + err.Error())
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		panic("builtin workflows: " + err.Error())
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := fs.ReadFile(dir, name)
		if err != nil {
			panic("builtin workflows: " + name + ": " + err.Error())
		}
		out[name] = data
	}
	return out
}
