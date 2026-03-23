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
    on_failure:
      retry: 2
      strategy: [same, escalate: claude-sonnet]
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 1 || r.Steps[0].OnFailure == nil {
		t.Fatal("expected one step with on_failure")
	}
	if len(r.Steps[0].OnFailure.Strategy) != 2 {
		t.Fatalf("strategy len: got %d", len(r.Steps[0].OnFailure.Strategy))
	}
	if r.Steps[0].OnFailure.Strategy[0].Type != "same" || r.Steps[0].OnFailure.Strategy[0].Count != 1 {
		t.Errorf("first: got %+v", r.Steps[0].OnFailure.Strategy[0])
	}
	if r.Steps[0].OnFailure.Strategy[1].Type != "escalate" || r.Steps[0].OnFailure.Strategy[1].Agent != "claude-sonnet" {
		t.Errorf("second: got %+v", r.Steps[0].OnFailure.Strategy[1])
	}
}

func TestParse_StrategyMapForm(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
    on_failure:
      retry: 5
      strategy: [same: 3, escalate: claude-sonnet]
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].OnFailure == nil {
		t.Fatal("expected on_failure")
	}
	// WHY: v4 expands "same: 3" at runtime into 3 individual strategy slots.
	exp := r.Steps[0].ExpandedOnFailureStrategy()
	if len(exp) != 4 {
		t.Fatalf("expected 4 expanded strategy entries, got %d", len(exp))
	}
	for i := 0; i < 3; i++ {
		if exp[i].Type != "same" {
			t.Errorf("strategy[%d]: expected same, got %+v", i, exp[i])
		}
	}
	if exp[3].Type != "escalate" || exp[3].Agent != "claude-sonnet" {
		t.Errorf("escalate: got %+v", exp[3])
	}
}

func TestParse_ValidateForms(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: do
    agent: claude
    prompt: p
    gate: [compile, touched: "*_test.*"]
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps[0].Gate) != 2 {
		t.Fatalf("gate len: %d", len(r.Steps[0].Gate))
	}
	if r.Steps[0].Gate[0].Type != "compile" {
		t.Errorf("first validator: %+v", r.Steps[0].Gate[0])
	}
	if r.Steps[0].Gate[1].Type != "touched" || r.Steps[0].Gate[1].Arg != "*_test.*" {
		t.Errorf("second: %+v", r.Steps[0].Gate[1])
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
    gate: [compile, 42]
`)
	_, err := Parse(yaml, "")
	if err == nil {
		t.Fatal("expected error for malformed gate")
	}
	if !strings.Contains(err.Error(), "expected string or map") {
		t.Errorf("error should mention expected form: %v", err)
	}
	if !strings.Contains(err.Error(), "gate") {
		t.Errorf("error should mention gate path: %v", err)
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

func TestParse_WorkflowInputsAndGuard(t *testing.T) {
	yaml := []byte(`
name: parent
steps:
  - name: sub
    workflow: child
    with:
      msg: hello
  - name: work
    agent: codex
    prompt: test
    guard:
      max_turns: 2
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Workflow != "child" {
		t.Fatalf("workflow not parsed: %+v", r.Steps[0])
	}
	if r.Steps[0].With["msg"] != "hello" {
		t.Fatalf("with msg not parsed")
	}
	if r.Steps[1].Guard.MaxTurns != 2 {
		t.Fatalf("guard not parsed")
	}
}
