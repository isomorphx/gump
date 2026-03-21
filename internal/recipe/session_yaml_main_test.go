package recipe

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParse_SessionString verifies session: reuse (string → self reuse).
func TestParse_SessionString(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: s1
    agent: claude
    prompt: p
    session: reuse
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Session.Mode != "reuse" || r.Steps[0].Session.Target != "" {
		t.Errorf("session: reuse -> Mode=%q Target=%q", r.Steps[0].Session.Mode, r.Steps[0].Session.Target)
	}
}

// TestParse_SessionBlockMap verifies session with reuse: spec-build (mapping block).
func TestParse_SessionBlockMap(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: s1
    agent: claude
    prompt: p
    session:
      reuse: spec-build
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Session.Mode != "reuse-targeted" || r.Steps[0].Session.Target != "spec-build" {
		t.Errorf("session block reuse: spec-build -> Mode=%q Target=%q", r.Steps[0].Session.Mode, r.Steps[0].Session.Target)
	}
}

// TestParse_SessionFlowMap verifies session: { reuse: spec-refine } (mapping flow).
func TestParse_SessionFlowMap(t *testing.T) {
	yaml := []byte(`
name: test
steps:
  - name: s1
    agent: claude
    prompt: p
    session: { reuse: spec-refine }
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Session.Mode != "reuse-targeted" || r.Steps[0].Session.Target != "spec-refine" {
		t.Errorf("session flow reuse: spec-refine -> Mode=%q Target=%q", r.Steps[0].Session.Mode, r.Steps[0].Session.Target)
	}
}

// TestSessionYAML_InvalidOneLine documente que "session: reuse: step" sur une ligne est invalide en YAML (mapping values not allowed).
func TestSessionYAML_InvalidOneLine(t *testing.T) {
	yamlBytes := []byte(`
steps:
  - name: s1
    session: reuse: spec-build
`)
	var raw struct {
		Steps []struct {
			Session interface{} `yaml:"session"`
		} `yaml:"steps"`
	}
	err := yaml.Unmarshal(yamlBytes, &raw)
	if err == nil {
		t.Fatal("expected YAML error for session: reuse: spec-build on one line")
	}
	if !strings.Contains(err.Error(), "mapping values are not allowed") {
		t.Logf("yaml error: %v", err)
	}
}

// TestParse_AdversarialSessionForms verifies that a recipe with the three valid session forms parses without error.
func TestParse_AdversarialSessionForms(t *testing.T) {
	// Content equivalent to internal/builtin/recipes/adversarial.yaml (three forms: reuse, block reuse:, flow possible)
	yaml := []byte(`
name: adversarial
description: Test session forms.
steps:
  - name: spec-build
    agent: claude-sonnet
    prompt: build
    gate: []
  - name: spec-refine
    agent: claude-sonnet
    prompt: refine
    gate: []
  - name: step-self-reuse
    agent: claude-sonnet
    prompt: self
    session: reuse
    gate: []
  - name: step-reuse-build
    agent: claude-sonnet
    prompt: use build session
    session:
      reuse: spec-build
    gate: []
  - name: step-reuse-refine
    agent: claude-sonnet
    prompt: use refine session
    session:
      reuse: spec-refine
    gate: []
`)
	r, err := Parse(yaml, "")
	if err != nil {
		t.Fatalf("adversarial-style recipe must parse: %v", err)
	}
	if len(r.Steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(r.Steps))
	}
	// step 2: session: reuse
	if r.Steps[2].Session.Mode != "reuse" || r.Steps[2].Session.Target != "" {
		t.Errorf("step 2 session: got Mode=%q Target=%q", r.Steps[2].Session.Mode, r.Steps[2].Session.Target)
	}
	// step 3: session: { reuse: spec-build }
	if r.Steps[3].Session.Mode != "reuse-targeted" || r.Steps[3].Session.Target != "spec-build" {
		t.Errorf("step 3 session: got Mode=%q Target=%q", r.Steps[3].Session.Mode, r.Steps[3].Session.Target)
	}
	// step 4: session: reuse: spec-refine (block)
	if r.Steps[4].Session.Mode != "reuse-targeted" || r.Steps[4].Session.Target != "spec-refine" {
		t.Errorf("step 4 session: got Mode=%q Target=%q", r.Steps[4].Session.Mode, r.Steps[4].Session.Target)
	}
}
