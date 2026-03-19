package builtin

import (
	"embed"
	"io/fs"

	"github.com/isomorphx/pudding/internal/recipe"
)

// recipesFS embeds built-in YAML so users can run "pudding cook ... --recipe tdd" with zero setup.
//go:embed recipes/*.yaml
var recipesFS embed.FS

func init() {
	recipe.BuiltinRecipes = mustLoadRecipes()
}

func mustLoadRecipes() map[string][]byte {
	out := make(map[string][]byte)
	dir, err := fs.Sub(recipesFS, "recipes")
	if err != nil {
		panic("builtin recipes: " + err.Error())
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		panic("builtin recipes: " + err.Error())
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := fs.ReadFile(dir, name)
		if err != nil {
			panic("builtin recipes: " + name + ": " + err.Error())
		}
		out[name] = data
	}
	return out
}
