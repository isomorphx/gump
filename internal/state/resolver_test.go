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
