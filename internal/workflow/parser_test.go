package workflow

import (
	"strings"
	"testing"
)

func TestParseWorkflowCallNoType(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: setup
    workflow: workflows/setup-env
    with:
      k: v
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Steps[0].Workflow != "workflows/setup-env" || len(wf.Steps[0].With) != 1 {
		t.Fatalf("%+v", wf.Steps[0])
	}
}

func TestParseParallelGroupNoType(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: g
    parallel: true
    steps:
      - name: a
        type: code
        agent: x
        prompt: p
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if !wf.Steps[0].Parallel || len(wf.Steps[0].Steps) != 1 {
		t.Fatalf("%+v", wf.Steps[0])
	}
}

func TestParseGuardTimeoutConflict(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    guard:
      max_time: 1m
      timeout: 2m
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("got %v", err)
	}
}

// --- Gate polymorphism (parseGate via full Parse)

func TestParseGateStringForms(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    gate: [compile, "lint:strict", test]
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	g := wf.Steps[0].Gate
	if len(g) != 3 {
		t.Fatalf("len=%d %+v", len(g), g)
	}
	if g[0].Type != "compile" || g[0].Arg != "" {
		t.Errorf("g0: %+v", g[0])
	}
	if g[1].Type != "lint" || g[1].Arg != "strict" {
		t.Errorf("g1: %+v", g[1])
	}
	if g[2].Type != "test" || g[2].Arg != "" {
		t.Errorf("g2: %+v", g[2])
	}
}

func TestParseGateMapBashTouchedCoverage(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    gate:
      - bash: "go test ./..."
      - untouched: vendor/
      - coverage: "80"
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	g := wf.Steps[0].Gate
	if len(g) != 3 {
		t.Fatalf("len=%d %+v", len(g), g)
	}
	if g[0].Type != "bash" || !strings.Contains(g[0].Arg, "go test") {
		t.Errorf("g0: %+v", g[0])
	}
	if g[1].Type != "untouched" || g[1].Arg != "vendor/" {
		t.Errorf("g1: %+v", g[1])
	}
	if g[2].Type != "coverage" || g[2].Arg != "80" {
		t.Errorf("g2: %+v", g[2])
	}
}

func TestParseGateValidateMapWithExtras(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    gate:
      - validate: validators/review
        diff: "{diff}"
        spec: "{spec}"
        agent: claude-opus
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	ge := wf.Steps[0].Gate[0]
	if ge.Type != "validate" || ge.Arg != "validators/review" {
		t.Fatalf("entry: %+v", ge)
	}
	if len(ge.With) != 3 || ge.With["diff"] != "{diff}" || ge.With["agent"] != "claude-opus" {
		t.Fatalf("With: %+v", ge.With)
	}
}

func TestParseGateEmptyStringEntry(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    gate: ["", compile]
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
}

func TestParseGateUnknownMapKey(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    gate:
      - unknown_gate: x
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("got %v", err)
	}
}

func TestParseGateValidateMissingPath(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    gate:
      - validate: ""
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "validate") {
		t.Fatalf("got %v", err)
	}
}

// --- Retry

func TestParseRetryExactlyOneCondition(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    retry:
      - attempt: 1
        exit: 2
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "exactly one condition") {
		t.Fatalf("got %v", err)
	}
}

func TestParseRetryUnknownKey(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    retry:
      - attempt: 1
        unknown: x
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("got %v", err)
	}
}

func TestParseRetryValidateWithAndStepAgentOverride(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    retry:
      - validate: validators/check
        with:
          diff: "{diff}"
          err: "{error}"
        agent: claude-opus
      - exit: 5
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	r := wf.Steps[0].Retry
	if len(r) != 2 {
		t.Fatalf("len=%d", len(r))
	}
	if r[0].Validate != "validators/check" || len(r[0].With) != 2 || r[0].Agent != "claude-opus" {
		t.Fatalf("r0: %+v", r[0])
	}
	if r[1].Exit != 5 || r[1].Agent != "" {
		t.Fatalf("r1: %+v", r[1])
	}
}

// --- Session

func TestParseSessionStringNew(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    session: new
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Steps[0].Session.Mode != "new" || wf.Steps[0].Session.Target != "" {
		t.Fatalf("%+v", wf.Steps[0].Session)
	}
}

func TestParseSessionFromMap(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: anchor
    gate: [compile]
  - name: s
    type: code
    agent: a
    prompt: p
    session:
      from: anchor
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	sc := wf.Steps[1].Session
	if sc.Mode != "from" || sc.Target != "anchor" {
		t.Fatalf("%+v", sc)
	}
}

func TestParseSessionAbsentDefaultsNew(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Steps[0].Session.Mode != "new" {
		t.Fatalf("%+v", wf.Steps[0].Session)
	}
}

func TestParseSessionInvalidString(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    session: garbage
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("got %v", err)
	}
}

// --- Context (polymorphic list)

func TestParseContextFileAndBash(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    context:
      - file: README.md
      - bash: git status -sb
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	c := wf.Steps[0].Context
	if len(c) != 2 || c[0].Type != "file" || c[1].Type != "bash" {
		t.Fatalf("%+v", c)
	}
}

func TestParseContextInvalidEntry(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    type: code
    agent: a
    prompt: p
    context:
      - oops: nope
`)
	_, _, err := Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "file") {
		t.Fatalf("got %v", err)
	}
}

// --- Legacy no type (parser tolerant)

func TestParseLegacyAgentPromptNoType(t *testing.T) {
	yaml := []byte(`name: w
steps:
  - name: s
    agent: claude-sonnet
    prompt: "hi"
`)
	wf, _, err := Parse(yaml, "")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Steps[0].Type != "" {
		t.Fatalf("expected empty type, got %q", wf.Steps[0].Type)
	}
	if errs := Validate(wf); len(errs) == 0 {
		t.Fatal("expected validation error for missing type")
	}
}
