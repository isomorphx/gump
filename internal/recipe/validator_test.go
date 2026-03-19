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
		if strings.Contains(e.Message, "must have") && strings.Contains(e.Message, "agent") {
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
		if strings.Contains(e.Message, "cannot have both") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs: %v", errs)
	}
}

func TestValidate_RetryMaxAttempts(t *testing.T) {
	r := &Recipe{
		Name: "x",
		Steps: []Step{{
			Name: "s", Agent: "a", Prompt: "p",
			Retry: &RetryPolicy{MaxAttempts: 0, Strategy: []StrategyEntry{{Type: "same", Count: 1}}},
		}},
	}
	errs := Validate(r)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "max_attempts must be >= 1") {
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
			{Name: "plan", Agent: "opus", Output: "plan", Validate: []Validator{{Type: "schema", Arg: "plan"}}},
			{Name: "impl", Foreach: "plan", Steps: []Step{
				{Name: "code", Agent: "haiku", Prompt: "p", Validate: []Validator{{Type: "compile"}}},
			}},
		},
	}
	errs := Validate(r)
	if len(errs) != 0 {
		t.Errorf("unexpected errs: %v", errs)
	}
}
