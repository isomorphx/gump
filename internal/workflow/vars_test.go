package workflow

import "testing"

func TestValidatorGateNameFromPath(t *testing.T) {
	if g := ValidatorGateNameFromPath("validators/arch-review"); g != "review" {
		t.Fatalf("arch-review: got %q", g)
	}
	if g := ValidatorGateNameFromPath("validators/check"); g != "check" {
		t.Fatalf("check: got %q", g)
	}
}

func TestWorkflowRefLastSegment(t *testing.T) {
	if g := WorkflowRefLastSegment("workflows/setup-env"); g != "setup-env" {
		t.Fatalf("got %q", g)
	}
}
