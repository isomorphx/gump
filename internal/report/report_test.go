package report

import "testing"

func TestNormalizeStepPattern(t *testing.T) {
	if g := normalizeStepPattern("build/task-1/impl", "task-1"); g != "build/*/impl" {
		t.Errorf("got %q", g)
	}
	if g := normalizeStepPattern("quality", ""); g != "quality" {
		t.Errorf("got %q", g)
	}
}
