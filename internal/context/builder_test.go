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
	err := Build("diff", "Hello world prompt", nil, dir, cfg, nil, nil, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Pudding — Agent Instructions") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "code step") {
		t.Error("expected diff mode wording")
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

func TestBuild_PlanContainsPlanJSON(t *testing.T) {
	dir := t.TempDir()
	err := Build("plan", "", nil, dir, nil, nil, map[string]string{"spec": "SPEC_BODY"}, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(data)
	if !strings.Contains(s, ".pudding/out/plan.json") {
		t.Error("plan must reference plan.json")
	}
	if !strings.Contains(s, "SPEC_BODY") {
		t.Error("spec must appear in plan template")
	}
	if !strings.Contains(s, defaultPlanTaskPrompt[:40]) {
		t.Error("empty plan prompt should use default task text")
	}
}

func TestBuild_BlastRadius(t *testing.T) {
	dir := t.TempDir()
	err := Build("diff", "Implement", nil, dir, nil, []string{"pkg/a.go", "pkg/b.go"}, nil, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	content := string(data)
	if !strings.Contains(content, "## Blast radius") {
		t.Error("missing blast radius section")
	}
	if !strings.Contains(content, "- pkg/a.go") || !strings.Contains(content, "- pkg/b.go") {
		t.Error("missing files in blast radius")
	}
}

func TestBuild_ConventionsDiffOnly(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, ".pudding")
	if err := os.MkdirAll(convDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "conventions.md"), []byte("Use Go 1.22."), 0644); err != nil {
		t.Fatal(err)
	}
	err := Build("diff", "Do it", nil, dir, nil, nil, nil, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(data), "## Project conventions") || !strings.Contains(string(data), "Use Go 1.22") {
		t.Error("diff mode should inject conventions")
	}
}

func TestBuild_PlanOmitsConventions(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, ".pudding")
	if err := os.MkdirAll(convDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "conventions.md"), []byte("Use Go 1.22."), 0644); err != nil {
		t.Fatal(err)
	}
	err := Build("plan", "x", nil, dir, nil, nil, map[string]string{"spec": "S"}, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if strings.Contains(string(data), "Project conventions") {
		t.Error("plan mode must not include conventions block")
	}
}

func TestBuild_ContextFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ctx.txt", "Context content here")
	sources := []recipe.ContextSource{{File: "ctx.txt"}}
	err := Build("diff", "Do it", sources, dir, nil, nil, nil, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(data)
	if !strings.Contains(s, "### ctx.txt") || !strings.Contains(s, "Context content here") {
		t.Error("file context missing")
	}
	if !strings.Contains(s, "## Additional context") {
		t.Error("expected Additional context section")
	}
}

func TestBuild_ContextFileNotFound(t *testing.T) {
	dir := t.TempDir()
	sources := []recipe.ContextSource{{File: "missing.md"}}
	err := Build("diff", "Do it", sources, dir, nil, nil, nil, "CLAUDE.md", nil, nil)
	if err == nil {
		t.Fatal("expected error for missing context file")
	}
	if !strings.Contains(err.Error(), "context file not found") || !strings.Contains(err.Error(), "missing.md") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildAgentContext_ReviewMode(t *testing.T) {
	s := BuildAgentContext(ContextParams{OutputMode: "review", Prompt: "Check code"})
	if !strings.Contains(s, "review step") || !strings.Contains(s, "review.json") {
		t.Errorf("review template: %s", s)
	}
}

func TestBuild_ArtifactContainsArtifactTxt(t *testing.T) {
	dir := t.TempDir()
	err := Build("artifact", "Summarize the spec", nil, dir, nil, nil, map[string]string{"spec": "SPEC"}, "CLAUDE.md", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(data)
	if !strings.Contains(s, "artifact step") || !strings.Contains(s, ".pudding/out/artifact.txt") {
		t.Errorf("artifact template: %s", s)
	}
	if !strings.Contains(s, "Summarize the spec") {
		t.Error("task prompt should appear in artifact template")
	}
}

func TestBuildAgentContext_SessionReusePrepends(t *testing.T) {
	s := BuildAgentContext(ContextParams{OutputMode: "diff", Prompt: "x", SessionReuse: true})
	if !strings.HasPrefix(strings.TrimSpace(s), "## Context transition") {
		t.Errorf("expected transition first: %q", s[:min(80, len(s))])
	}
}

func TestTruncateLines_Short(t *testing.T) {
	in := "a\nb\nc\nd\n"
	if TruncateLines(in, 100) != in {
		t.Error("short string unchanged")
	}
}

func TestTruncateLines_Long(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, strings.Repeat("x", 50))
	}
	in := strings.Join(lines, "\n")
	out := TruncateLines(in, 200)
	if !strings.Contains(out, "[... truncated") {
		t.Error("expected truncation marker")
	}
}

// When the naive head/tail windows overlap (e.g. first line has no newline within the head window),
// TruncateLines must still cut only on line boundaries (greedy path), not mid-line.
func TestTruncateLines_OverlapKeepsLineBoundaries(t *testing.T) {
	longLine := strings.Repeat("a", 4000)
	in := longLine + "\nTAIL_LINE\n"
	out := TruncateLines(in, 200)
	if !strings.Contains(out, "[... truncated") {
		t.Fatalf("expected marker, got len=%d prefix=%q", len(out), out[:min(80, len(out))])
	}
	if !strings.Contains(out, "TAIL_LINE") {
		t.Error("tail line should be preserved")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
