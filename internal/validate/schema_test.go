package validate

import (
	"testing"

	"github.com/isomorphx/gump/internal/state"
)

func TestRunSchemaValidator_NoOutput(t *testing.T) {
	sb := state.New()
	r := RunSchemaValidator("decompose", sb)
	if r.Pass {
		t.Error("expected fail when no output")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}

func TestRunSchemaValidator_ValidPlan(t *testing.T) {
	sb := state.New()
	sb.SetStepOutput("decompose", `[{"name":"t1","description":"d1","files":["a.go"]}]`, "", nil, "")
	r := RunSchemaValidator("decompose", sb)
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
}

func TestRunSchemaValidator_InvalidJSON(t *testing.T) {
	sb := state.New()
	sb.SetStepOutput("decompose", "not json", "", nil, "")
	r := RunSchemaValidator("decompose", sb)
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}

func TestRunSchemaValidatorWithArg_UnknownSchema(t *testing.T) {
	r := RunSchemaValidatorWithArg("x", state.New(), "other")
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}
