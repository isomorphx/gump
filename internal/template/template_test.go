package template

import (
	"testing"

	"github.com/isomorphx/pudding/internal/statebag"
)

func TestResolve_Spec(t *testing.T) {
	vars := map[string]string{"spec": "Build a REST API"}
	out := Resolve("Do this: {spec}", vars, nil, "")
	if out != "Do this: Build a REST API" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_TaskVars(t *testing.T) {
	vars := map[string]string{
		"task.name":        "add-auth",
		"task.description": "Add JWT auth",
		"task.files":       "pkg/auth.go, pkg/auth_test.go",
	}
	out := Resolve("Task {task.name}: {task.description}. Files: {task.files}", vars, nil, "")
	if out != "Task add-auth: Add JWT auth. Files: pkg/auth.go, pkg/auth_test.go" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_UnknownLeftAsIs(t *testing.T) {
	vars := map[string]string{"spec": "x"}
	out := Resolve("{spec} and {unknown}", vars, nil, "")
	if out != "x and {unknown}" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_NilVars(t *testing.T) {
	out := Resolve("{spec}", nil, nil, "")
	if out != "{spec}" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_StepsRefWhenNilStateBag(t *testing.T) {
	out := Resolve("Prior: {steps.analyze.output}", nil, nil, "")
	if out != "Prior: " {
		t.Errorf("steps ref with nil stateBag should be empty: got %q", out)
	}
}

func TestResolve_StepsRefFromStateBag(t *testing.T) {
	sb := statebag.New()
	sb.Set("analyze", "stub artifact output for analyze", "")
	vars := map[string]string{}
	out := Resolve("Use: {steps.analyze.output}", vars, sb, "code")
	if out != "Use: stub artifact output for analyze" {
		t.Errorf("got %q", out)
	}
}
