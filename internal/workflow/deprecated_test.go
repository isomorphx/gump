package workflow

import (
	"strings"
	"testing"
)

func TestDetectDeprecatedRoot(t *testing.T) {
	m := map[string]interface{}{
		"inputs":      map[string]interface{}{},
		"review":      []interface{}{},
		"description": "x",
	}
	w := detectDeprecated(m)
	if len(w) != 3 {
		t.Fatalf("got %d warnings: %v", len(w), w)
	}
	joined := joinWarns(w)
	for _, frag := range []string{"inputs", "review", "description", "v0.0.4"} {
		if !strings.Contains(joined, frag) {
			t.Errorf("expected %q in warnings: %s", frag, joined)
		}
	}
}

func TestDetectDeprecatedStep(t *testing.T) {
	m := map[string]interface{}{
		"output":       "diff",
		"on_failure":   map[string]interface{}{},
		"strategy":     []interface{}{},
		"restart_from": "s",
		"replan":       true,
		"foreach":      "x",
		"recipe":       "y",
	}
	w := detectDeprecatedStep(m)
	if len(w) != 7 {
		t.Fatalf("got %d warnings: %v", len(w), w)
	}
	joined := joinWarns(w)
	for _, frag := range []string{"output", "on_failure", "strategy", "restart_from", "foreach", "recipe"} {
		if !strings.Contains(joined, frag) {
			t.Errorf("expected %q in warnings: %s", frag, joined)
		}
	}
}

func TestDetectDeprecatedRootPartial(t *testing.T) {
	w := detectDeprecated(map[string]interface{}{"inputs": true})
	if len(w) != 1 || !strings.Contains(w[0].Message, "inputs") {
		t.Fatalf("%v", w)
	}
}

func TestDetectDeprecatedStepPartial(t *testing.T) {
	w := detectDeprecatedStep(map[string]interface{}{"output": "x"})
	if len(w) != 1 || !strings.Contains(w[0].Message, "output") {
		t.Fatalf("%v", w)
	}
}

func joinWarns(w []Warning) string {
	var b strings.Builder
	for _, x := range w {
		b.WriteString(x.Message)
		b.WriteByte('\n')
	}
	return b.String()
}
