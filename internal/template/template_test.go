package template

import (
	"testing"

	"github.com/isomorphx/gump/internal/state"
)

func TestResolve_Spec(t *testing.T) {
	ctx := &state.ResolveContext{Spec: "Build a REST API"}
	out := Resolve("Do this: {spec}", ctx)
	if out != "Do this: Build a REST API" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_TaskVars(t *testing.T) {
	ctx := &state.ResolveContext{
		Task: &state.TaskVars{
			Name: "add-auth", Description: "Add JWT auth",
			Files: "pkg/auth.go, pkg/auth_test.go",
		},
	}
	out := Resolve("Task {task.name}: {task.description}. Files: {task.files}", ctx)
	if out != "Task add-auth: Add JWT auth. Files: pkg/auth.go, pkg/auth_test.go" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_UnknownLeftAsIs(t *testing.T) {
	ctx := &state.ResolveContext{Spec: "x"}
	out := Resolve("{spec} and {unknown}", ctx)
	if out != "x and " {
		t.Errorf("got %q", out)
	}
}

func TestResolve_NilCtx(t *testing.T) {
	out := Resolve("{spec}", nil)
	if out != "" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_QualifiedStepOutput(t *testing.T) {
	st := state.New()
	st.SetStepOutput("analyze", "stub artifact output for analyze", "", nil, "")
	ctx := &state.ResolveContext{State: st, StepPath: "code"}
	out := Resolve("Use: {analyze.output}", ctx)
	if out != "Use: stub artifact output for analyze" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_StepMetricsAndRunDeprecated(t *testing.T) {
	st := state.New()
	st.UpdateStepAgentMetrics("first", 123, 0.05, 2, 1000, 500, 7, 9)
	st.SetRunMetric("cost", "0.05")
	ctx := &state.ResolveContext{State: st, StepPath: "first"}
	out := Resolve("C1={first.tokens_in} C2={first.cost} R={run.cost}", ctx)
	if out != "C1=1000 C2=0.05 R=" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_NestedPathInState(t *testing.T) {
	st := state.New()
	st.SetStepOutput("call-sub/steps/echo", "hello", "", nil, "")
	ctx := &state.ResolveContext{State: st, StepPath: "root"}
	out := Resolve("X={call-sub/steps/echo.output}", ctx)
	if out != "X=hello" {
		t.Fatalf("got %q", out)
	}
}

func TestResolve_EscapedDoubleBracesRemainLiteral(t *testing.T) {
	ctx := &state.ResolveContext{Spec: "ok"}
	out := Resolve("json: {{example}} and {spec}", ctx)
	if out != "json: {example} and ok" {
		t.Fatalf("got %q", out)
	}
}

func TestResolve_UnresolvedVariablesAreCleaned(t *testing.T) {
	out := Resolve("a\n{error}\nb {missing}\n{steps.future.output}\nc", &state.ResolveContext{})
	if out != "a\nb \nc" {
		t.Fatalf("got %q", out)
	}
}

func TestResolve_RemovesOnlyPlaceholderLinesThatBecomeEmpty(t *testing.T) {
	ctx := &state.ResolveContext{Extra: map[string]string{"empty": ""}}
	out := Resolve("start\n\n{empty}\nkeep\n", ctx)
	if out != "start\n\nkeep\n" {
		t.Fatalf("got %q", out)
	}
}
