package workflow

import (
	"strings"
	"testing"
)

func TestValidateDuplicateRootStepNames(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{Name: "a", Type: "code", Agent: "x", Prompt: "p"},
			{Name: "a", Type: "code", Agent: "x", Prompt: "p"},
		},
	}
	errs := Validate(wf)
	if len(errs) == 0 {
		t.Fatal("expected duplicate error")
	}
	combined := joinErrs(errs)
	if !strings.Contains(combined, "duplicate") {
		t.Fatalf("errs: %s", combined)
	}
}

func TestValidateDuplicateNamesInEachScope(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{
				Name: "splitter", Type: "split", Agent: "a", Prompt: "p",
				Each: []Step{
					{Name: "x", Type: "code", Agent: "a", Prompt: "p"},
					{Name: "x", Type: "code", Agent: "a", Prompt: "p"},
				},
			},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "duplicate") {
		t.Fatalf("expected duplicate in each scope: %s", combined)
	}
}

func TestValidateSplitRequiresEach(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{Name: "s", Type: "split", Agent: "a", Prompt: "p", Gate: []GateEntry{{Type: "schema"}}},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "each") {
		t.Fatalf("expected each error: %s", combined)
	}
}

func TestValidateEachOnlyOnSplit(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{
				Name: "c", Type: "code", Agent: "a", Prompt: "p",
				Each: []Step{{Name: "n", Type: "code", Agent: "a", Prompt: "p"}},
			},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "each") {
		t.Fatalf("expected each-only-on-split: %s", combined)
	}
}

func TestValidateGateOnlyRejectsPrompt(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{Name: "g", Gate: []GateEntry{{Type: "compile"}}, Prompt: "nope"},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "gate-only") && !strings.Contains(combined, "prompt") {
		t.Fatalf("expected gate-only prompt error: %s", combined)
	}
}

func TestValidatePromptRequiresAgent(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{Name: "s", Type: "code", Prompt: "p"},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "agent") {
		t.Fatalf("expected agent required: %s", combined)
	}
}

func TestValidateWorkflowCallRejectsAgent(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{Name: "w", Workflow: "other", Agent: "x"},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "workflow") || !strings.Contains(combined, "agent") {
		t.Fatalf("expected workflow+agent clash: %s", combined)
	}
}

func TestValidateSessionFromMissingTarget(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{Name: "s", Type: "code", Agent: "a", Prompt: "p", Session: SessionConfig{Mode: "from", Target: "nope"}},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "session") || !strings.Contains(combined, "nope") {
		t.Fatalf("expected session target error: %s", combined)
	}
}

func TestValidateRetryAttemptOrder(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{
				Name: "s", Type: "code", Agent: "a", Prompt: "p",
				Retry: []RetryEntry{
					{Attempt: 5, Prompt: "a"},
					{Attempt: 3, Prompt: "b"},
					{Exit: 9},
				},
			},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "ascending") {
		t.Fatalf("expected ascending order error: %s", combined)
	}
}

func TestValidateRetryRequiresExit(t *testing.T) {
	wf := &Workflow{
		Name: "w",
		Steps: []Step{
			{
				Name: "s", Type: "code", Agent: "a", Prompt: "p",
				Retry: []RetryEntry{{Attempt: 2, Prompt: "x"}},
			},
		},
	}
	errs := Validate(wf)
	combined := joinErrs(errs)
	if !strings.Contains(combined, "exit") {
		t.Fatalf("expected exit required: %s", combined)
	}
}

func joinErrs(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
