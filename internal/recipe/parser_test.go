package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_StrategyStringForm(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
    retry:
      max_attempts: 2
      strategy: [same, escalate: claude-sonnet]
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 1 || r.Steps[0].Retry == nil {
		t.Fatal("expected one step with retry")
	}
	if len(r.Steps[0].Retry.Strategy) != 2 {
		t.Fatalf("strategy len: got %d", len(r.Steps[0].Retry.Strategy))
	}
	if r.Steps[0].Retry.Strategy[0].Type != "same" || r.Steps[0].Retry.Strategy[0].Count != 1 {
		t.Errorf("first: got %+v", r.Steps[0].Retry.Strategy[0])
	}
	if r.Steps[0].Retry.Strategy[1].Type != "escalate" || r.Steps[0].Retry.Strategy[1].Agent != "claude-sonnet" {
		t.Errorf("second: got %+v", r.Steps[0].Retry.Strategy[1])
	}
}

func TestParse_StrategyMapForm(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
    retry:
      max_attempts: 5
      strategy: [same: 3, escalate: claude-sonnet]
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Retry.Strategy[0].Type != "same" || r.Steps[0].Retry.Strategy[0].Count != 3 {
		t.Errorf("same:3: got %+v", r.Steps[0].Retry.Strategy[0])
	}
}

func TestParse_ValidateForms(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
    validate: [compile, touched: "*_test.*"]
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps[0].Validate) != 2 {
		t.Fatalf("validate len: %d", len(r.Steps[0].Validate))
	}
	if r.Steps[0].Validate[0].Type != "compile" {
		t.Errorf("first validator: %+v", r.Steps[0].Validate[0])
	}
	if r.Steps[0].Validate[1].Type != "touched" || r.Steps[0].Validate[1].Arg != "*_test.*" {
		t.Errorf("second: %+v", r.Steps[0].Validate[1])
	}
}

func TestParse_SessionDefault(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Session.Mode != "fresh" {
		t.Errorf("default session when absent: got %q, want fresh", r.Steps[0].Session.Mode)
	}
}

func TestParse_ValidateErrorPropagated(t *testing.T) {
	// Malformed validate entry (number instead of string/map) must produce a clear error.
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
    validate: [compile, 42]
`)
	_, err := Parse(yaml, "")
	if err == nil {
		t.Fatal("expected error for malformed validate")
	}
	if !strings.Contains(err.Error(), "expected string or map") {
		t.Errorf("error should mention expected form: %v", err)
	}
	if !strings.Contains(err.Error(), "validate") {
		t.Errorf("error should mention validate path: %v", err)
	}
}

// TestParse_PromptFile ensures prompt: file: <path> loads content from recipe dir so long prompts stay out of YAML.
func TestParse_PromptFile(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "test-prompt.md")
	if err := os.WriteFile(promptPath, []byte("Create {task.name}"), 0644); err != nil {
		t.Fatal(err)
	}
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt:
      file: test-prompt.md
`)
	r, err := Parse(yaml, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Prompt != "Create {task.name}" {
		t.Errorf("step.Prompt = %q, want %q", r.Steps[0].Prompt, "Create {task.name}")
	}
}

func TestParse_PromptFileNotFound(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt:
      file: missing.md
`)
	_, err := Parse(yaml, dir)
	if err == nil {
		t.Fatal("expected error for missing prompt file")
	}
	if !strings.Contains(err.Error(), "prompt file not found") || !strings.Contains(err.Error(), "missing.md") {
		t.Errorf("error should mention prompt file not found and path: %v", err)
	}
}
