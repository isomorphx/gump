package e2e

import (
	"strings"
	"testing"

	_ "github.com/isomorphx/gump/internal/builtin"
	"github.com/isomorphx/gump/internal/workflow"
)

func mustParse(t *testing.T, yaml []byte) (*workflow.Workflow, []workflow.Warning) {
	t.Helper()
	wf, warns, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return wf, warns
}

func mustValidate(t *testing.T, wf *workflow.Workflow) {
	t.Helper()
	if errs := workflow.Validate(wf); len(errs) > 0 {
		t.Fatalf("validate: %v", errs)
	}
}

func validationStrings(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}

// E2E-R1-01 : forme plate simple
func TestE2E_R1_01_SimpleFlat(t *testing.T) {
	yaml := []byte(`name: simple
steps:
  - name: execute
    type: code
    prompt: "{spec}"
    agent: claude-opus
    guard: { max_turns: 80 }
    gate: [compile]
`)
	wf, warns := mustParse(t, yaml)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	mustValidate(t, wf)
	if wf.Name != "simple" {
		t.Fatalf("Name: %q", wf.Name)
	}
	s0 := wf.Steps[0]
	if s0.Type != "code" || s0.Prompt != "{spec}" || s0.Agent != "claude-opus" {
		t.Fatalf("step fields: %+v", s0)
	}
	if s0.Guard.MaxTurns != 80 {
		t.Fatalf("Guard.MaxTurns: %d", s0.Guard.MaxTurns)
	}
	if len(s0.Gate) != 1 || s0.Gate[0].Type != "compile" {
		t.Fatalf("Gate: %+v", s0.Gate)
	}
	workflow.ApplyDefaults(wf)
	if s0.Worktree != "read-write" {
		t.Fatalf("Worktree: %q", s0.Worktree)
	}
	if s0.Session.Mode != "new" {
		t.Fatalf("Session.Mode: %q", s0.Session.Mode)
	}
}

// E2E-R1-02 : get/run structuré
func TestE2E_R1_02_StructuredGetRun(t *testing.T) {
	yaml := []byte(`name: structured
steps:
  - name: converge
    gate: [compile]
  - name: impl
    type: code
    get:
      prompt: |
        Implement: {task.description}
        Files: {task.files}
      context:
        - file: docs/architecture.md
        - bash: git log --oneline -10
      worktree: read-write
      session:
        from: converge
    run:
      agent: claude-sonnet
      guard:
        max_turns: 60
        max_budget: 3.00
      hitl: before_gate
    gate:
      - compile
      - test
      - validate: validators/arch-review
        diff: "{diff}"
        spec: "{spec}"
        agent: claude-opus
    retry:
      - attempt: 2
        prompt: |
          Fix: {gate.review.comments}
      - attempt: 4
        agent: claude-opus
        session: new
        worktree: reset
      - exit: 6
`)
	wf, warns := mustParse(t, yaml)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	mustValidate(t, wf)
	step := wf.Steps[1]
	if !strings.Contains(step.Prompt, "Implement: {task.description}") {
		t.Fatalf("prompt: %q", step.Prompt)
	}
	if len(step.Context) != 2 || step.Context[0].Type != "file" || step.Context[1].Type != "bash" {
		t.Fatalf("Context: %+v", step.Context)
	}
	if step.Session.Mode != "from" || step.Session.Target != "converge" {
		t.Fatalf("Session: %+v", step.Session)
	}
	if step.Agent != "claude-sonnet" || step.Guard.MaxTurns != 60 || step.Guard.MaxBudget != 3.0 {
		t.Fatalf("run fields: %+v / %+v", step, step.Guard)
	}
	if step.HITL != "before_gate" {
		t.Fatalf("HITL: %q", step.HITL)
	}
	if len(step.Gate) != 3 {
		t.Fatalf("len gate %d", len(step.Gate))
	}
	g2 := step.Gate[2]
	if g2.Type != "validate" || g2.Arg != "validators/arch-review" || len(g2.With) != 3 {
		t.Fatalf("gate[2]: %+v", g2)
	}
	if len(step.Retry) != 3 {
		t.Fatalf("retry len %d", len(step.Retry))
	}
	if step.Retry[0].Attempt != 2 || !strings.Contains(step.Retry[0].Prompt, "Fix:") {
		t.Fatalf("retry[0]: %+v", step.Retry[0])
	}
	if step.Retry[1].Attempt != 4 || step.Retry[1].Agent != "claude-opus" || step.Retry[1].Session != "new" || step.Retry[1].Worktree != "reset" {
		t.Fatalf("retry[1]: %+v", step.Retry[1])
	}
	if step.Retry[2].Exit != 6 {
		t.Fatalf("retry[2]: %+v", step.Retry[2])
	}
}

// E2E-R1-03 : split + each
func TestE2E_R1_03_SplitEach(t *testing.T) {
	yaml := []byte(`name: split-each
steps:
  - name: decompose
    type: split
    get:
      prompt: "Decompose {spec} into tasks."
    run:
      agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        get:
          prompt: "Implement: {task.description}"
        run:
          agent: claude-sonnet
          guard: { max_turns: 60 }
        gate: [compile, test]
        retry:
          - exit: 3
      - name: smoke
        type: code
        get:
          prompt: "Run smoke tests."
          session:
            from: impl
        run:
          agent: claude-sonnet
        gate: [compile, test]
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	s0 := wf.Steps[0]
	if s0.Type != "split" || len(s0.Each) != 2 {
		t.Fatalf("split: %+v", s0)
	}
	if s0.Each[0].Name != "impl" || s0.Each[0].Type != "code" {
		t.Fatalf("each[0]: %+v", s0.Each[0])
	}
	sm := s0.Each[1]
	if sm.Session.Mode != "from" || sm.Session.Target != "impl" {
		t.Fatalf("each[1] session: %+v", sm.Session)
	}
	workflow.ApplyDefaults(wf)
	if s0.Worktree != "read-only" {
		t.Fatalf("split worktree: %q", s0.Worktree)
	}
}

// E2E-R1-04 : split + parallel
func TestE2E_R1_04_SplitParallel(t *testing.T) {
	yaml := []byte(`name: parallel-split
steps:
  - name: decompose
    type: split
    parallel: true
    get:
      prompt: "Decompose {spec}."
    run:
      agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        prompt: "{task.description}"
        agent: claude-sonnet
        gate: [compile]
        retry:
          - exit: 3
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	if !wf.Steps[0].Parallel {
		t.Fatal("expected parallel")
	}
}

// E2E-R1-05 : agent pass
func TestE2E_R1_05_AgentPass(t *testing.T) {
	yaml := []byte(`name: pass-test
steps:
  - name: check-size
    type: code
    agent: pass
    gate:
      - bash: "test $(wc -l < main.go) -lt 500"
    retry:
      - exit: 2
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	if wf.Steps[0].Agent != "pass" {
		t.Fatalf("agent: %q", wf.Steps[0].Agent)
	}
}

// E2E-R1-06 : gate-only
func TestE2E_R1_06_GateOnly(t *testing.T) {
	yaml := []byte(`name: quality-check
steps:
  - name: quality
    gate: [compile, lint, test]
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	s := wf.Steps[0]
	if s.Type != "" || s.Agent != "" || len(s.Gate) != 3 {
		t.Fatalf("step: %+v", s)
	}
}

// E2E-R1-07 : bornes globales
func TestE2E_R1_07_GlobalBounds(t *testing.T) {
	yaml := []byte(`name: bounded
max_budget: 20.00
max_timeout: 30m
max_tokens: 500000
steps:
  - name: exec
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	if wf.MaxBudget != 20.0 || wf.MaxTimeout != "30m" || wf.MaxTokens != 500000 {
		t.Fatalf("bounds: %v %q %d", wf.MaxBudget, wf.MaxTimeout, wf.MaxTokens)
	}
}

// E2E-R1-08 : retry validate condition
func TestE2E_R1_08_RetryValidate(t *testing.T) {
	yaml := []byte(`name: retry-validate
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - validate: validators/assess-distance
        with:
          diff: "{diff}"
          error: "{error}"
          agent: claude-haiku
        agent: claude-opus
      - not: gate.test
        agent: claude-opus
      - exit: 6
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	r := wf.Steps[0].Retry
	if r[0].Validate != "validators/assess-distance" || len(r[0].With) != 3 || r[0].Agent != "claude-opus" {
		t.Fatalf("retry[0]: %+v", r[0])
	}
	if r[1].Not != "gate.test" || r[1].Agent != "claude-opus" {
		t.Fatalf("retry[1]: %+v", r[1])
	}
	if r[2].Exit != 6 {
		t.Fatalf("retry[2]: %+v", r[2])
	}
}

// E2E-R1-09 : guards étendus + alias timeout
func TestE2E_R1_09_GuardsExtended(t *testing.T) {
	yaml := []byte(`name: guards-extended
steps:
  - name: exec
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    guard:
      max_turns: 60
      max_budget: 3.00
      max_tokens: 100000
      max_time: 10m
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	g := wf.Steps[0].Guard
	if g.MaxTokens != 100000 || g.MaxTime != "10m" {
		t.Fatalf("guard: %+v", g)
	}
	yaml2 := []byte(strings.Replace(string(yaml), "max_time: 10m", "timeout: 10m", 1))
	wf2, _ := mustParse(t, yaml2)
	mustValidate(t, wf2)
	if wf2.Steps[0].Guard.MaxTime != "10m" {
		t.Fatalf("timeout alias: %+v", wf2.Steps[0].Guard)
	}
	yaml3 := []byte(`name: guards-both
steps:
  - name: exec
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    guard:
      max_time: 5m
      timeout: 10m
`)
	_, _, err := workflow.Parse(yaml3, "")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout/max_time conflict, got %v", err)
	}
}

// E2E-R1-10 : dépréciations v0.0.3
func TestE2E_R1_10_DeprecatedV003(t *testing.T) {
	yaml := []byte(`name: deprecated
inputs:
  diff: { required: true }
review:
  - compile
description: "A workflow"
steps:
  - name: impl
    output: diff
    agent: claude-sonnet
    prompt: "{spec}"
    on_failure:
      retry: 3
    strategy: [same, same]
    restart_from: tests
    foreach: decompose
    recipe: sub-workflow
`)
	wf, warns, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(warns) < 9 {
		t.Fatalf("expected many warnings, got %d: %v", len(warns), warns)
	}
	s := wf.Steps[0]
	if len(s.Retry) != 0 || len(s.Gate) != 0 {
		t.Fatalf("legacy keys should not populate retry/gate: retry=%d gate=%d", len(s.Retry), len(s.Gate))
	}
	errs := workflow.Validate(wf)
	combined := validationStrings(errs)
	if !strings.Contains(combined, "type") {
		t.Fatalf("expected type validation error, got: %s", combined)
	}
}

// E2E-R1-11 : erreurs validation multiples
func TestE2E_R1_11_ValidationErrors(t *testing.T) {
	yaml := []byte(`name: errors
steps:
  - name: ""
    type: code
    agent: claude-sonnet
  - name: impl
    type: split
    agent: claude-opus
    prompt: "decompose"
  - name: impl
    type: code
    agent: claude-sonnet
    prompt: "{spec}"
    retry:
      - attempt: 5
      - attempt: 3
  - name: bad-retry
    type: code
    agent: claude-sonnet
    prompt: "{spec}"
    retry:
      - attempt: 2
`)
	wf, _, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := workflow.Validate(wf)
	combined := validationStrings(errs)
	for _, frag := range []string{"name", "each", "duplicate", "attempt", "exit"} {
		if !strings.Contains(combined, frag) {
			t.Errorf("expected %q in errors: %s", frag, combined)
		}
	}
}

// E2E-R1-12 : doublon bloc/plat
func TestE2E_R1_12_DupBlockFlat(t *testing.T) {
	yaml := []byte(`name: doublon
steps:
  - name: impl
    type: code
    prompt: "inline prompt"
    get:
      prompt: "bloc prompt"
    agent: claude-sonnet
`)
	_, _, err := workflow.Parse(yaml, "")
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

// E2E-R1-13 : HITL
func TestE2E_R1_13_HITL(t *testing.T) {
	yaml := []byte(`name: hitl-test
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    hitl: after_gate
    gate: [compile, test]
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	if wf.Steps[0].HITL != "after_gate" {
		t.Fatalf("HITL: %q", wf.Steps[0].HITL)
	}
	yaml2 := []byte(`name: hitl-true
steps:
  - name: s
    type: code
    agent: a
    prompt: "p"
    hitl: true
`)
	wf2, _ := mustParse(t, yaml2)
	mustValidate(t, wf2)
	if wf2.Steps[0].HITL != "before_gate" {
		t.Fatalf("hitl true: %q", wf2.Steps[0].HITL)
	}
	yaml3 := []byte(`name: hitl-bad
steps:
  - name: s
    type: code
    agent: a
    prompt: "p"
    hitl: invalid
`)
	wf3, _ := mustParse(t, yaml3)
	errs := workflow.Validate(wf3)
	if len(errs) == 0 {
		t.Fatal("expected validation error for invalid hitl")
	}
}

// E2E-R1-14 : groupe parallèle
func TestE2E_R1_14_ParallelGroup(t *testing.T) {
	yaml := []byte(`name: parallel-group
steps:
  - name: reviews
    parallel: true
    steps:
      - name: arch-review
        type: validate
        prompt: "Review architecture"
        agent: claude-opus
      - name: security-review
        type: validate
        prompt: "Review security"
        agent: claude-sonnet
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	g := wf.Steps[0]
	if !g.Parallel || len(g.Steps) != 2 {
		t.Fatalf("group: %+v", g)
	}
	workflow.ApplyDefaults(wf)
	for _, ch := range g.Steps {
		if ch.Worktree != "read-only" {
			t.Fatalf("child worktree: %+v", ch)
		}
	}
}

// E2E-R1-15 : workflow call
func TestE2E_R1_15_WorkflowCall(t *testing.T) {
	yaml := []byte(`name: with-sub
steps:
  - name: setup
    workflow: workflows/setup-env
    with:
      project: "{spec.project}"
      target: staging
`)
	wf, _ := mustParse(t, yaml)
	mustValidate(t, wf)
	s := wf.Steps[0]
	if s.Workflow != "workflows/setup-env" || len(s.With) != 2 {
		t.Fatalf("step: %+v", s)
	}
}

// Smoke-R1-02 (spec §7) : dry-run CLI — repo minimal avec gump.toml, même entrée que les autres smokes e2e (spec positionnel + --workflow).
func TestSmoke_R1_02_DryRunCLI(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "gump.toml", "# minimal project config (Smoke-R1-02)\n")
	writeFile(t, dir, "spec.md", "x")
	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"freeform", "execute", "claude-opus", "Dry Run"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
}

// Smoke-R1-01 : built-ins sans warning
func TestSmoke_R1_01_BuiltinsNoWarnings(t *testing.T) {
	names := []string{
		"tdd.yaml",
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
			t.Fatalf("missing builtin %s", k)
		}
		wf, warns, err := workflow.Parse(raw, "")
		if err != nil {
			t.Fatalf("parse %s: %v", k, err)
		}
		if len(warns) != 0 {
			t.Fatalf("builtin %s: expected 0 warnings, got %v", k, warns)
		}
		if errs := workflow.Validate(wf); len(errs) != 0 {
			t.Fatalf("validate %s: %v", k, errs)
		}
	}
	vraw := workflow.BuiltinValidators["arch-review.yaml"]
	if len(vraw) == 0 {
		t.Fatal("missing builtin arch-review.yaml")
	}
	vwf, vwarns, verr := workflow.Parse(vraw, "")
	if verr != nil {
		t.Fatalf("parse arch-review: %v", verr)
	}
	if len(vwarns) != 0 {
		t.Fatalf("arch-review warnings: %v", vwarns)
	}
	if errs := workflow.Validate(vwf); len(errs) != 0 {
		t.Fatalf("validate arch-review: %v", errs)
	}
}

// Smoke-R1-03 : workflow v0.0.3 → warnings + erreur validation type
func TestSmoke_R1_03_LegacyWorkflowWarnings(t *testing.T) {
	yaml := []byte(`name: legacy-ish
inputs:
  x: {}
steps:
  - name: s
    output: diff
    agent: claude
    prompt: "hi"
    on_failure: { retry: 1 }
`)
	wf, warns, err := workflow.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(warns) == 0 {
		t.Fatal("expected deprecation warnings")
	}
	errs := workflow.Validate(wf)
	if len(errs) == 0 {
		t.Fatal("expected validation errors (missing type)")
	}
}
