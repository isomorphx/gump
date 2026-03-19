package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_Builtin(t *testing.T) {
	// Unit test runs without main, so builtin init may not have run; seed built-in for this test.
	old := BuiltinRecipes
	BuiltinRecipes = map[string][]byte{"tdd.yaml": []byte("name: tdd\ndescription: test\nsteps: []\nreview: []\n")}
	defer func() { BuiltinRecipes = old }()
	resolved, err := Resolve("tdd", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != "built-in" || resolved.Name != "tdd" {
		t.Errorf("got %+v", resolved)
	}
	if len(resolved.Raw) == 0 {
		t.Error("raw empty")
	}
}

func TestResolve_NotFound(t *testing.T) {
	_, err := Resolve("doesnotexist", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %v", err)
	}
}

func TestResolve_ProjectOverrides(t *testing.T) {
	dir := t.TempDir()
	recipesDir := filepath.Join(dir, ".pudding", "recipes")
	_ = os.MkdirAll(recipesDir, 0755)
	_ = os.WriteFile(filepath.Join(recipesDir, "tdd.yaml"), []byte("name: tdd\ndescription: custom\nsteps: []\nreview: []\n"), 0644)
	resolved, err := Resolve("tdd", dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != "project" {
		t.Errorf("got source %q", resolved.Source)
	}
	if !strings.Contains(string(resolved.Raw), "custom") {
		t.Errorf("raw %s", resolved.Raw)
	}
}
