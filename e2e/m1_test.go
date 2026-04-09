package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/isomorphx/gump/internal/builtin"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/workflow"
)

func gateHasType(g []workflow.GateEntry, typ string) bool {
	for _, v := range g {
		if v.Type == typ {
			return true
		}
	}
	return false
}

func TestM1_1_ParserV4_TDD(t *testing.T) {
	raw := workflow.BuiltinWorkflows["tdd.yaml"]
	if len(raw) == 0 {
		t.Fatal("missing builtin workflow tdd.yaml")
	}
	r, _, err := workflow.Parse(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := workflow.Validate(r); len(errs) != 0 {
		t.Fatalf("validate errs: %v", errs)
	}

	if r.Name != "tdd" {
		t.Fatalf("Workflow.Name: got %q", r.Name)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("len(Workflow.Steps): got %d", len(r.Steps))
	}
	s0 := r.Steps[0]
	if s0.Name != "build" || s0.Type != "split" || s0.Agent != "claude-opus" || !gateHasType(s0.Gate, "schema") || len(s0.Each) != 2 {
		t.Fatalf("steps[0] split: got %+v", s0)
	}
	if r.Steps[1].Name != "quality" || len(r.Steps[1].Gate) == 0 {
		t.Fatalf("steps[1] quality gate: %+v", r.Steps[1])
	}
}

func TestM1_2_ParserV4_SplitParallelEach(t *testing.T) {
	yaml := []byte(`name: parallel-demo
steps:
  - name: fan
    type: split
    parallel: true
    agent: claude-opus
    prompt: "Plan tasks for {spec}"
    gate: [schema]
    each:
      - name: a
        type: code
        agent: claude-opus
        prompt: "do a"
      - name: b
        type: code
        agent: claude-opus
        prompt: "do b"
`)
	r, _, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := workflow.Validate(r); len(errs) != 0 {
		t.Fatalf("validate errs: %v", errs)
	}
	fan := r.Steps[0]
	if fan.Name != "fan" || !fan.Parallel || len(fan.Each) != 2 {
		t.Fatalf("fan step: %+v", fan)
	}
}

func TestM1_3_RejectV3ValidateAtStepLevel(t *testing.T) {
	yaml := []byte(`name: bad
steps:
  - name: s
    agent: claude
    validate: [compile]
    retry:
      max_attempts: 2
      strategy: ["same"]
`)
	_, _, err := workflow.Parse(yaml, "")
	if err == nil {
		t.Fatal("expected parse error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "validate:") || !strings.Contains(msg, "replaced by") || !strings.Contains(msg, "gate:") {
		t.Fatalf("unexpected error message: %s", msg)
	}
}

func TestM1_4_RejectStepAmbiguousAgentAndSteps(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: invalid
    type: code
    agent: claude
    prompt: "x"
    steps:
      - name: sub
        type: code
        agent: claude
        prompt: "x"
`)
	r, _, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := workflow.Validate(r)
	all := make([]string, 0, len(errs))
	for _, e := range errs {
		all = append(all, e.Error())
	}
	combined := strings.Join(all, "\n")
	if !strings.Contains(combined, "has both 'agent' and 'steps'") {
		t.Fatalf("combined errs missing has both: %s", combined)
	}
	if !strings.Contains(combined, "Hint: split into two steps") {
		t.Fatalf("combined errs missing hint: %s", combined)
	}
}

func TestM1_5_RetryRequiresExitCondition(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: impl
    type: code
    agent: claude
    prompt: "x"
    retry:
      - attempt: 2
        agent: other
`)
	r, _, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := workflow.Validate(r)
	combined := ""
	for _, e := range errs {
		combined += e.Error() + "\n"
	}
	if !strings.Contains(combined, "exit") {
		t.Fatalf("expected exit-related validation, got: %s", combined)
	}
}

func TestM1_6_RetryDuplicateConditionsRejected(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: impl
    type: code
    agent: claude
    prompt: "x"
    retry:
      - attempt: 1
        exit: 3
        not: foo
`)
	_, _, err := workflow.Parse(yaml, "")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "exactly one condition") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestM1_7_TemplateTaskVars(t *testing.T) {
	ctx := &state.ResolveContext{
		Task: &state.TaskVars{Description: "Add auth", Files: "auth.go"},
	}
	out := template.Resolve("Implement {task.description} in {task.files}", ctx)
	if out != "Implement Add auth in auth.go" {
		t.Fatalf("got %q", out)
	}
}

func TestM1_8_TemplateItemVarsRemoved(t *testing.T) {
	out := template.Resolve("x {item.description} y", &state.ResolveContext{})
	if out != "x  y" {
		t.Fatalf("got %q", out)
	}
}

func TestM1_9_DryRunFormatV4(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "Implement a hello world function")
	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "tdd", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"type=split", "gate=[schema]", "each:", "claude-opus"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
}

func TestM1_10_DryRunStateBagResolutions(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/statebag.yaml", `name: statebag
steps:
  - name: impl
    type: code
    agent: claude-opus
    prompt: "build"
  - name: next
    type: code
    agent: claude-opus
    prompt: "Use {impl.output}"
`)
	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "statebag", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "State Bag Resolutions:") {
		t.Fatalf("stdout missing State Bag resolutions: %s", stdout)
	}
	if !strings.Contains(stdout, "impl.output") || !strings.Contains(stdout, "→") {
		t.Fatalf("stdout missing impl.output / arrow: %s", stdout)
	}
}

func TestM1_11_ParserMaxBudget(t *testing.T) {
	yaml := []byte(`name: x
max_budget: 5.00
steps:
  - name: s
    type: code
    agent: a
    prompt: "x"
    guard:
      max_budget: 2.00
`)
	r, _, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := workflow.Validate(r); len(errs) != 0 {
		t.Fatalf("validate errs: %v", errs)
	}
	if r.MaxBudget != 5.0 {
		t.Fatalf("Workflow.MaxBudget: got %v", r.MaxBudget)
	}
	if r.Steps[0].Guard.MaxBudget != 2.0 {
		t.Fatalf("Step.Guard.MaxBudget: got %v", r.Steps[0].Guard.MaxBudget)
	}
}

func TestM1_12_ParserHitl(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: s
    type: code
    agent: a
    prompt: "p"
    hitl: true
`)
	r, _, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(r.Steps) == 0 || r.Steps[0].HITL != "before_gate" {
		t.Fatalf("Step.HITL: expected before_gate, got %+v", r.Steps)
	}
}

func TestM1_13_BuiltinWorkflowsParseAndValidate(t *testing.T) {
	names := []string{
		"tdd.yaml",
		"simple.yaml",
		"cheap2sota.yaml",
		"parallel-tasks.yaml",
		"implement-spec.yaml",
		"bugfix.yaml",
		"refactor.yaml",
		"freeform.yaml",
	}
	for _, k := range names {
		raw := workflow.BuiltinWorkflows[k]
		if len(raw) == 0 {
			t.Fatalf("missing builtin workflow %s", k)
		}
		r, warns, err := workflow.Parse(raw, "")
		if err != nil {
			t.Fatalf("parse %s: %v", k, err)
		}
		if len(warns) != 0 {
			t.Fatalf("builtin %s: expected 0 warnings, got %v", k, warns)
		}
		if errs := workflow.Validate(r); len(errs) != 0 {
			t.Fatalf("validate %s: %v", k, errs)
		}
	}
}
