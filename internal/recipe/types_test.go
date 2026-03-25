package recipe

import "testing"

func TestStep_MaxAttempts_flatRetryTotal(t *testing.T) {
	s := Step{OnFailure: &OnFailure{Retry: 2, Strategy: []StrategyEntry{{Type: "same", Count: 1}}}}
	if s.MaxAttempts() != 2 {
		t.Fatalf("flat retry:2 means 2 total attempts, got %d", s.MaxAttempts())
	}
}

func TestStep_MaxAttempts_conditionalPlusOne(t *testing.T) {
	s := Step{OnFailure: &OnFailure{
		GateFail:  &FailureAction{Retry: 2},
		GuardFail: &FailureAction{Retry: 1},
	}}
	if !s.OnFailure.IsConditionalForm() {
		t.Fatal("expected conditional form")
	}
	// max(2,1)+1 = 3
	if s.MaxAttempts() != 3 {
		t.Fatalf("MaxAttempts = max branch retries + 1, got %d", s.MaxAttempts())
	}
}

func TestStep_MaxAttempts_conditionalAllZero(t *testing.T) {
	s := Step{OnFailure: &OnFailure{
		GateFail:  &FailureAction{Retry: 0},
		GuardFail: &FailureAction{Retry: 0},
	}}
	if s.MaxAttempts() != 1 {
		t.Fatalf("expected 1 when all branch retries are 0, got %d", s.MaxAttempts())
	}
}

func TestStep_ShouldRunWithRetryLoop_conditional(t *testing.T) {
	s := Step{OnFailure: &OnFailure{
		GateFail: &FailureAction{Retry: 1},
	}}
	if !s.ShouldRunWithRetryLoop() {
		t.Fatal("expected retry loop when gate_fail.retry > 0")
	}
}

func TestStep_ShouldRunWithRetryLoop_restartFromOnly(t *testing.T) {
	s := Step{OnFailure: &OnFailure{RestartFrom: "prep"}}
	if !s.RestartFromWithoutStrategy() {
		t.Fatal("fixture: restart_from only")
	}
	if !s.ShouldRunWithRetryLoop() {
		t.Fatal("expected retry loop for restart_from-only policy")
	}
}
