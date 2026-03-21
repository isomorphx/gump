package recipe

import (
	"strings"
	"testing"
)

func TestValidate_NameRequired(t *testing.T) {
	r := &Recipe{Name: "", Steps: []Step{{Name: "s", Agent: "a", Prompt: "p"}}}
	errs := Validate(r)
	if len(errs) == 0 {
		t.Fatal("expected errors")
	}
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "name is required") && e.Path == "recipe" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs: %v", errs)
	}
}

func TestValidate_AtLeastOneStep(t *testing.T) {
	r := &Recipe{Name: "x", Steps: nil}
	errs := Validate(r)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "at least one step") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs: %v", errs)
	}
}

func TestValidate_StepMustHaveAgentStepsOrValidate(t *testing.T) {
	r := &Recipe{Name: "x", Steps: []Step{{Name: "s", Agent: "", Prompt: "p"}}}
	errs := Validate(r)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "has no agent") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs: %v", errs)
	}
}

func TestValidate_CannotHaveBothAgentAndSteps(t *testing.T) {
	r := &Recipe{
		Name: "x",
		Steps: []Step{{
			Name: "f", Agent: "should-not-be-set",
			Steps: []Step{{Name: "s", Agent: "a", Prompt: "p"}},
		}},
	}
	errs := Validate(r)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "has both 'agent' and 'steps'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs: %v", errs)
	}
}

func TestValidate_RestartFromWithoutRetry(t *testing.T) {
	r := &Recipe{
		Name: "x",
		Steps: []Step{{
			Name:   "s",
			Agent:  "a",
			Prompt: "p",
			Gate:   []Validator{{Type: "compile"}},
			OnFailure: &OnFailure{
				Retry:       0,
				RestartFrom: "t0",
				Strategy:    []StrategyEntry{{Type: "same", Count: 1}},
			},
		}},
	}
	errs := Validate(r)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "restart_from without retry limit") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs: %v", errs)
	}
}

func TestValidate_ValidRecipePasses(t *testing.T) {
	r := &Recipe{
		Name: "tdd",
		Steps: []Step{
			{Name: "plan", Agent: "opus", Output: "plan", Gate: []Validator{{Type: "schema", Arg: "plan"}}},
			{Name: "impl", Foreach: "plan", Steps: []Step{
				{Name: "code", Agent: "haiku", Prompt: "p", Gate: []Validator{{Type: "compile"}}},
			}},
		},
	}
	errs := Validate(r)
	if len(errs) != 0 {
		t.Errorf("unexpected errs: %v", errs)
	}
}

func TestValidate_SessionReuseTargetDifferentAgentIsError(t *testing.T) {
	r := &Recipe{
		Name: "x",
		Steps: []Step{
			{Name: "a", Agent: "claude", Prompt: "p"},
			{
				Name:   "b",
				Agent:  "codex",
				Prompt: "p",
				Session: SessionConfig{
					Mode:   "reuse-targeted",
					Target: "a",
				},
			},
		},
	}
	errs := Validate(r)
	combined := ""
	for _, e := range errs {
		combined += e.Error() + "\n"
	}
	if !strings.Contains(combined, "has session: reuse: a") || !strings.Contains(combined, "session reuse requires the same agent") {
		t.Fatalf("expected session reuse mismatch error, got: %s", combined)
	}
}
