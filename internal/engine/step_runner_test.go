package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/workflow"
)

func TestMakeRetryEvalForStep_DefaultMatchesNewRetryEvaluator(t *testing.T) {
	t.Cleanup(func() { retryEvalFactory = nil })
	step := &workflow.Step{
		Agent: " claude-sonnet ",
		Retry: []workflow.RetryEntry{{Attempt: 2, Prompt: "fix"}, {Exit: 5}},
	}
	a := makeRetryEvalForStep(step, "impl")
	b := NewRetryEvaluator(step.Retry, "impl", "claude-sonnet")
	ctx := &state.ResolveContext{State: state.New(), StepPath: "impl", Attempt: 2}
	da, errA := a.Evaluate(2, nil, nil, ctx)
	if errA != nil {
		t.Fatal(errA)
	}
	db, errB := b.Evaluate(2, nil, nil, ctx)
	if errB != nil {
		t.Fatal(errB)
	}
	if da.Action != db.Action || da.Prompt != db.Prompt || da.MatchedEntry != db.MatchedEntry {
		t.Fatalf("mismatch %+v vs %+v", da, db)
	}
}

func TestEvaluateRetryAttempt_HookOverridesDecision(t *testing.T) {
	t.Cleanup(func() { retryEvaluateHook = nil })
	eval := NewRetryEvaluator([]workflow.RetryEntry{{Exit: 3}}, "s", "sonnet")
	retryEvaluateHook = func(real retryEval, attempt int, gateStates map[string]gatePassState, validator ValidatorInvoker, resolveCtx *state.ResolveContext) (*RetryDecision, error) {
		if attempt == 2 {
			return &RetryDecision{Action: "retry", Agent: "claude-opus", Session: "new"}, nil
		}
		return real.Evaluate(attempt, gateStates, validator, resolveCtx)
	}
	ctx := &state.ResolveContext{State: state.New(), StepPath: "s"}
	d, err := evaluateRetryAttempt(eval, 2, nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Agent != "claude-opus" || d.Session != "new" || d.Action != "retry" {
		t.Fatalf("got %+v", d)
	}
}

func TestEmitRetryTriggered_LedgerContainsOverridesFromDecision(t *testing.T) {
	dir := t.TempDir()
	l, err := ledger.New(dir, "test-run")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	d := &RetryDecision{Action: "retry", Agent: "claude-opus", Session: "new", Worktree: "reset"}
	ov := ledgerOverridesFromDecision(d)
	if ov == nil {
		t.Fatal("ledgerOverridesFromDecision returned nil")
	}
	if err := l.Emit(ledger.RetryTriggered{Step: "impl", Attempt: 2, Overrides: ov}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(bytesTrimLine(raw), &payload); err != nil {
		t.Fatal(err)
	}
	o, ok := payload["overrides"].(map[string]interface{})
	if !ok {
		t.Fatalf("overrides missing or wrong type: %v", payload["overrides"])
	}
	if o["agent"] != "claude-opus" || o["session"] != "new" || o["worktree"] != "reset" {
		t.Fatalf("overrides: %v", o)
	}

	d2 := &RetryDecision{Action: "retry"}
	ov2 := ledgerOverridesFromDecision(d2)
	if err := l.Emit(ledger.RetryTriggered{Step: "impl", Attempt: 3, Overrides: ov2}); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(filepath.Join(dir, "manifest.ndjson"))
	lines := strings.Split(strings.TrimSpace(string(raw2)), "\n")
	if len(lines) < 2 {
		t.Fatalf("want 2 lines, got %q", string(raw2))
	}
	var payload2 map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &payload2); err != nil {
		t.Fatal(err)
	}
	o2, ok := payload2["overrides"].(map[string]interface{})
	if !ok || len(o2) != 0 {
		t.Fatalf("want empty overrides object, got %v", payload2["overrides"])
	}
}

func bytesTrimLine(b []byte) []byte {
	s := strings.TrimSpace(string(b))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return []byte(s)
}
