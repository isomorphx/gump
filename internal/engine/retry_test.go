package engine

import (
	"strings"
	"testing"

	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/workflow"
)

func TestRetryEvaluator_MaxAttempt(t *testing.T) {
	re := NewRetryEvaluator([]workflow.RetryEntry{{Exit: 5}, {Exit: 3}}, "s", "a")
	if re.MaxAttempt() != 5 {
		t.Fatalf("MaxAttempt: %d", re.MaxAttempt())
	}
}

func TestRetryEvaluator_ExitCap(t *testing.T) {
	re := NewRetryEvaluator([]workflow.RetryEntry{{Attempt: 2, Agent: "x"}, {Exit: 3}}, "s", "base")
	ctx := state.New()
	rc := &state.ResolveContext{State: ctx, StepPath: "s", GateResults: map[string]string{}}
	d, err := re.Evaluate(4, nil, nil, rc)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "fatal" {
		t.Fatalf("want fatal, got %+v", d)
	}
}

func TestRetryEvaluator_AccumulateAttemptOverrides(t *testing.T) {
	// WHY: later matching entries override the same keyword (E2E-R4-02 / §4.1 amendé).
	re := NewRetryEvaluator([]workflow.RetryEntry{
		{Attempt: 2, Agent: "sonnet-thinking"},
		{Attempt: 4, Agent: "opus"},
		{Exit: 6},
	}, "s", "sonnet")
	ctx := state.New()
	rc := &state.ResolveContext{State: ctx, StepPath: "s"}
	d, err := re.Evaluate(4, nil, nil, rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(d.Agent) != "opus" {
		t.Fatalf("agent: %q", d.Agent)
	}
}

func TestRetryEvaluator_NotGate(t *testing.T) {
	re := NewRetryEvaluator([]workflow.RetryEntry{
		{Not: "gate.test", Agent: "opus"},
		{Exit: 4},
	}, "s", "sonnet")
	ctx := state.New()
	rc := &state.ResolveContext{State: ctx, StepPath: "s"}
	gs := map[string]gatePassState{"test": {Known: true, Pass: false}}
	d, err := re.Evaluate(2, gs, nil, rc)
	if err != nil {
		t.Fatal(err)
	}
	if d.Agent != "opus" {
		t.Fatalf("agent: %q", d.Agent)
	}
	gs2 := map[string]gatePassState{"test": {Known: true, Pass: true}}
	d2, err := re.Evaluate(3, gs2, nil, rc)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Agent != "opus" {
		t.Fatalf("sticky agent: %q", d2.Agent)
	}
}

func TestRetryEvaluator_ValidateNilInvoker(t *testing.T) {
	re := NewRetryEvaluator([]workflow.RetryEntry{
		{Validate: "validators/x", Agent: "opus"},
		{Exit: 3},
	}, "s", "sonnet")
	ctx := state.New()
	rc := &state.ResolveContext{State: ctx, StepPath: "s"}
	_, err := re.Evaluate(2, nil, nil, rc)
	if err == nil || !strings.Contains(err.Error(), "workflow validators") {
		t.Fatalf("err: %v", err)
	}
}
