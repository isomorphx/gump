package state

import (
	"testing"
)

// WHY: one integration-style check that all resolver categories compose without shadowing each other.
func TestResolveContext_fullStack(t *testing.T) {
	st := New()
	st.Set("plan.output", `[]`)
	st.Set("impl.cost", "0.01")
	st.Set("impl.output", "old")
	st.RotatePrev("impl")
	st.Set("impl.output", "new")
	ctx := &ResolveContext{
		State: st, StepPath: "impl", Spec: "SPEC", Attempt: 2,
		Error: "e", Diff: "d",
		Task:  &TaskVars{Name: "t", Description: "d", Files: "f"},
		GateResults: map[string]string{"g": "true"},
		GateMeta:    map[string]map[string]string{"v": {"pass": "true", "comments": "c"}},
		Extra:       map[string]string{"custom": "extra"},
	}
	checks := map[string]string{
		"spec": "SPEC", "attempt": "2", "error": "e", "diff": "d", "output": "new",
		"task.name": "t", "prev.output": "old",
		"gate.g": "true", "gate.v.pass": "true", "gate.v.comments": "c",
		"plan.output": `[]`, "impl.cost": "0.01", "custom": "extra",
	}
	for k, want := range checks {
		if got := ctx.Resolve(k); got != want {
			t.Fatalf("%s: got %q want %q", k, got, want)
		}
	}
}

func TestResolveContext_flatStateKeyForSubworkflowInputs(t *testing.T) {
	st := New()
	st.Set("agent", "claude-opus")
	st.Set("query", "q")
	ctx := &ResolveContext{State: st, StepPath: "analyze", Spec: "S"}
	if ctx.Resolve("agent") != "claude-opus" {
		t.Fatalf("agent: %q", ctx.Resolve("agent"))
	}
	if ctx.Resolve("query") != "q" {
		t.Fatalf("query: %q", ctx.Resolve("query"))
	}
}

func TestResolveContext_extraOverridesFlatState(t *testing.T) {
	st := New()
	st.Set("agent", "from-state")
	ctx := &ResolveContext{State: st, StepPath: "x", Extra: map[string]string{"agent": "from-extra"}}
	if ctx.Resolve("agent") != "from-extra" {
		t.Fatalf("got %q", ctx.Resolve("agent"))
	}
}

func TestResolveContext_eachScopeBlocksTopLevelSiblingName(t *testing.T) {
	st := New()
	st.Set("converge.agent", "LEAK")
	st.Set("decompose/auth/converge.agent", "INNER")
	ctx := &ResolveContext{State: st, StepPath: "decompose/auth/smoke"}
	if got := ctx.Resolve("converge.agent"); got != "INNER" {
		t.Fatalf("scoped converge.agent: got %q", got)
	}
	st2 := New()
	st2.Set("converge.agent", "LEAK")
	ctx2 := &ResolveContext{State: st2, StepPath: "decompose/auth/smoke"}
	if got := ctx2.Resolve("converge.agent"); got != "" {
		t.Fatalf("missing task-local converge must not fall back to top-level: got %q", got)
	}
}

func TestResolveContext_eachScopeStillSeesAncestorSplitOutput(t *testing.T) {
	st := New()
	st.Set("decompose.output", `PLAN`)
	ctx := &ResolveContext{State: st, StepPath: "decompose/auth/impl"}
	if got := ctx.Resolve("decompose.output"); got != `PLAN` {
		t.Fatalf("decompose.output: got %q", got)
	}
}
