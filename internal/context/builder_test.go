package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isomorphx/pudding/internal/config"
	"github.com/isomorphx/pudding/internal/recipe"
)

func TestBuild_WritesCLAUDE(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	err := Build("diff", "Hello world prompt", nil, dir, cfg, nil, nil, "CLAUDE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Pudding Agent Context") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "Do NOT run") {
		t.Error("missing git rules")
	}
	if !strings.Contains(content, "## Your task") {
		t.Error("missing Your task section")
	}
	if !strings.Contains(content, "Hello world prompt") {
		t.Error("missing prompt")
	}
}

func TestBuild_PlanContainsMarker(t *testing.T) {
	dir := t.TempDir()
	err := Build("plan", "Plan the work", nil, dir, nil, nil, nil, "CLAUDE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(data), "[PUDDING:plan]") {
		t.Error("plan section must contain marker for stub")
	}
}

func TestBuild_BlastRadius(t *testing.T) {
	dir := t.TempDir()
	err := Build("diff", "Implement", nil, dir, nil, []string{"pkg/a.go", "pkg/b.go"}, nil, "CLAUDE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	content := string(data)
	if !strings.Contains(content, "Blast Radius") {
		t.Error("missing blast radius section")
	}
	if !strings.Contains(content, "pkg/a.go") || !strings.Contains(content, "pkg/b.go") {
		t.Error("missing files in blast radius")
	}
}

func TestBuild_Conventions(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, ".pudding")
	if err := os.MkdirAll(convDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "conventions.md"), []byte("Use Go 1.22."), 0644); err != nil {
		t.Fatal(err)
	}
	err := Build("diff", "Do it", nil, dir, nil, nil, nil, "CLAUDE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(data), "Project Conventions") || !strings.Contains(string(data), "Use Go 1.22") {
		t.Error("conventions section missing or wrong content")
	}
}

func TestBuild_ValidationCommands(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{CompileCmd: "go build ./...", TestCmd: "go test ./..."}
	err := Build("diff", "Do it", nil, dir, cfg, nil, nil, "CLAUDE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	content := string(data)
	if !strings.Contains(content, "Validation Commands") {
		t.Error("missing validation section")
	}
	if !strings.Contains(content, "go build") || !strings.Contains(content, "go test") {
		t.Error("missing compile/test commands")
	}
}

func TestBuild_ContextFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ctx.txt", "Context content here")
	sources := []recipe.ContextSource{{File: "ctx.txt"}}
	err := Build("diff", "Do it", sources, dir, nil, nil, nil, "CLAUDE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(data), "### ctx.txt") || !strings.Contains(string(data), "Context content here") {
		t.Error("file context missing")
	}
	if !strings.Contains(string(data), "## Context Files") {
		t.Error("expected ## Context Files section")
	}
}

func TestBuild_ContextFileNotFound(t *testing.T) {
	dir := t.TempDir()
	sources := []recipe.ContextSource{{File: "missing.md"}}
	err := Build("diff", "Do it", sources, dir, nil, nil, nil, "CLAUDE.md", nil)
	if err == nil {
		t.Fatal("expected error for missing context file")
	}
	if !strings.Contains(err.Error(), "context file not found") || !strings.Contains(err.Error(), "missing.md") {
		t.Errorf("unexpected error: %v", err)
	}
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
