package template

import (
	"testing"

	"github.com/isomorphx/gump/internal/statebag"
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
	if out != "x and " {
		t.Errorf("got %q", out)
	}
}

func TestResolve_NilVars(t *testing.T) {
	out := Resolve("{spec}", nil, nil, "")
	if out != "" {
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
	sb.Set("analyze", "stub artifact output for analyze", "", nil, "")
	vars := map[string]string{}
	out := Resolve("Use: {steps.analyze.output}", vars, sb, "code")
	if out != "Use: stub artifact output for analyze" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_RunAndStepMetricsRefs(t *testing.T) {
	sb := statebag.New()
	// WHY: verify that the template engine can interpolate stringified metrics.
	sb.UpdateStepAgentMetrics("first", 123, 0.05, 2, 1000, 500, 7, 9)
	sb.SetRunMetric("cost", "0.05")

	out := Resolve("C1={steps.first.tokens_in} C2={steps.first.cost} R={run.cost}", nil, sb, "code")
	if out != "C1=1000 C2=0.05 R=0.05" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_NestedWorkflowStateBagRef(t *testing.T) {
	sb := statebag.New()
	sb.Set("call-sub.steps.echo", "hello", "", nil, "")
	out := Resolve("X={steps.call-sub.steps.echo.output}", nil, sb, "")
	if out != "X=hello" {
		t.Fatalf("got %q", out)
	}
}

func TestResolve_EscapedDoubleBracesRemainLiteral(t *testing.T) {
	out := Resolve("json: {{example}} and {spec}", map[string]string{"spec": "ok"}, nil, "")
	if out != "json: {example} and ok" {
		t.Fatalf("got %q", out)
	}
}

func TestResolve_UnresolvedVariablesAreCleaned(t *testing.T) {
	out := Resolve("a\n{error}\nb {missing}\n{steps.future.output}\nc", nil, nil, "")
	if out != "a\nb \nc" {
		t.Fatalf("got %q", out)
	}
}

func TestResolve_RemovesOnlyPlaceholderLinesThatBecomeEmpty(t *testing.T) {
	out := Resolve("start\n\n{empty}\nkeep\n", map[string]string{"empty": ""}, nil, "")
	if out != "start\n\nkeep\n" {
		t.Fatalf("got %q", out)
	}
}
