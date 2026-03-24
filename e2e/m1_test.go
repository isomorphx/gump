package e2e

import (
	"io"
	"os"
	"strings"
	"testing"

	_ "github.com/isomorphx/gump/internal/builtin"
	"github.com/isomorphx/gump/internal/recipe"
	"github.com/isomorphx/gump/internal/template"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = orig
	out, _ := io.ReadAll(r)
	_ = r.Close()
	return string(out)
}

func gateHasType(g []recipe.Validator, typ string) bool {
	for _, v := range g {
		if v.Type == typ {
			return true
		}
	}
	return false
}

func TestM1_1_ParserV4_TDD(t *testing.T) {
	raw := recipe.BuiltinRecipes["tdd.yaml"]
	if len(raw) == 0 {
		t.Fatal("missing builtin recipe tdd.yaml")
	}
	r, err := recipe.Parse(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := recipe.Validate(r); len(errs) != 0 {
		t.Fatalf("validate errs: %v", errs)
	}

	if r.Name != "tdd" {
		t.Fatalf("Recipe.Name: got %q", r.Name)
	}
	if r.MaxBudget != 5.0 {
		t.Fatalf("Recipe.MaxBudget: got %v", r.MaxBudget)
	}
	if len(r.Steps) != 3 {
		t.Fatalf("len(Recipe.Steps): got %d", len(r.Steps))
	}
	if r.Steps[0].Name != "decompose" || r.Steps[0].Output != "plan" || r.Steps[0].Agent != "claude-opus" {
		t.Fatalf("steps[0]: got name=%q output=%q agent=%q", r.Steps[0].Name, r.Steps[0].Output, r.Steps[0].Agent)
	}
	if !gateHasType(r.Steps[0].Gate, "schema") {
		t.Fatalf("steps[0].Gate missing schema: %+v", r.Steps[0].Gate)
	}
	if r.Steps[1].Name != "build" || r.Steps[1].Foreach != "decompose" {
		t.Fatalf("steps[1]: got name=%q foreach=%q", r.Steps[1].Name, r.Steps[1].Foreach)
	}
	if r.Steps[1].Steps[0].Name != "tests" {
		t.Fatalf("steps[1].Steps[0].Name: got %q", r.Steps[1].Steps[0].Name)
	}
	if r.Steps[1].Steps[0].OnFailure == nil || r.Steps[1].Steps[0].OnFailure.Retry != 2 {
		t.Fatalf("steps[1].Steps[0].OnFailure.Retry: got %v", r.Steps[1].Steps[0].OnFailure)
	}
	if r.Steps[1].Steps[1].OnFailure == nil || r.Steps[1].Steps[1].OnFailure.RestartFrom != "tests" {
		t.Fatalf("steps[1].Steps[1].OnFailure.RestartFrom: got %q", r.Steps[1].Steps[1].OnFailure.RestartFrom)
	}
	if r.Steps[2].Name != "quality" || !gateHasType(r.Steps[2].Gate, "compile") || !gateHasType(r.Steps[2].Gate, "lint") || !gateHasType(r.Steps[2].Gate, "test") {
		t.Fatalf("steps[2] gate mismatch: %+v", r.Steps[2].Gate)
	}
	if r.Steps[2].Agent != "" {
		t.Fatalf("steps[2].Agent: expected empty gate step, got %q", r.Steps[2].Agent)
	}
}

func TestM1_2_ParserV4_AdversarialReview(t *testing.T) {
	raw := recipe.BuiltinRecipes["adversarial-review.yaml"]
	if len(raw) == 0 {
		t.Fatal("missing builtin recipe adversarial-review.yaml")
	}
	r, err := recipe.Parse(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := recipe.Validate(r); len(errs) != 0 {
		t.Fatalf("validate errs: %v", errs)
	}

	if len(r.Steps) < 2 {
		t.Fatalf("unexpected steps length: %d", len(r.Steps))
	}
	// build step is steps[1]
	build := r.Steps[1]
	if build.Steps[1].Parallel != true {
		t.Fatalf("build.Steps[1].Parallel: got %v", build.Steps[1].Parallel)
	}
	if build.Steps[1].Steps[0].Output != "review" || build.Steps[1].Steps[1].Output != "review" {
		t.Fatalf("review step outputs: %q / %q", build.Steps[1].Steps[0].Output, build.Steps[1].Steps[1].Output)
	}
	if build.Steps[2].Output != "artifact" {
		t.Fatalf("arbiter output: got %q", build.Steps[2].Output)
	}
	if build.OnFailure == nil {
		t.Fatal("build.OnFailure is nil")
	}
	if build.OnFailure.RestartFrom != "impl" || build.OnFailure.Retry != 3 {
		t.Fatalf("build.OnFailure: restart_from=%q retry=%d", build.OnFailure.RestartFrom, build.OnFailure.Retry)
	}
}

func TestM1_3_RejectV3Format(t *testing.T) {
	yaml := []byte(`name: bad
steps:
  - name: s
    agent: claude
    validate: [compile]
    retry:
      max_attempts: 2
      strategy: ["same"]
`)
	_, err := recipe.Parse(yaml, "")
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
    agent: claude
    steps:
      - name: sub
        agent: claude
        prompt: "x"
`)
	r, err := recipe.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := recipe.Validate(r)
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

func TestM1_5_CycleDetectionRestartFrom(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: impl
    agent: claude
    prompt: "x"
    on_failure:
      retry: 1
      strategy: ["same"]
      restart_from: tests
  - name: tests
    agent: claude
    prompt: "y"
    on_failure:
      retry: 1
      strategy: ["same"]
      restart_from: impl
`)
	r, err := recipe.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := recipe.Validate(r)
	combined := ""
	for _, e := range errs {
		combined += e.Error() + "\n"
	}
	if !strings.Contains(combined, "cycle detected in restart_from") {
		t.Fatalf("expected cycle error, got: %s", combined)
	}
}

func TestM1_6_RestartFromWithoutRetry(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: impl
    agent: claude
    prompt: "x"
    on_failure:
      restart_from: tests
  - name: tests
    agent: claude
    prompt: "y"
`)
	r, err := recipe.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := recipe.Validate(r)
	combined := ""
	for _, e := range errs {
		combined += e.Error() + "\n"
	}
	if !strings.Contains(combined, "restart_from without retry limit") {
		t.Fatalf("expected restart_from without retry limit, got: %s", combined)
	}
}

func TestM1_7_TemplateItemDotUnderscore(t *testing.T) {
	out := template.Resolve("Implement {item.description} in {item.files}", map[string]string{
		"item.description": "Add auth",
		"item.files":       "auth.go",
	}, nil, "")
	if out != "Implement Add auth in auth.go" {
		t.Fatalf("got %q", out)
	}
}

func TestM1_8_TemplateTaskVarsWarningDeprecated(t *testing.T) {
	stderr := captureStderr(t, func() {
		out := template.Resolve("Implement {task.description}", map[string]string{
			"item.description": "Add auth",
		}, nil, "")
		if out != "Implement Add auth" {
			t.Fatalf("got %q", out)
		}
	})
	if !strings.Contains(stderr, "deprecated") {
		t.Fatalf("expected deprecated warning, stderr=%q", stderr)
	}
}

func TestM1_9_DryRunFormatV4(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "Implement a hello world function")
	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "tdd", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"Budget:", "$5.00", "gate=[schema]", "on_failure:", "restart_from=tests"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
	if strings.Contains(stdout, "validate:") || strings.Contains(stdout, "retry:") {
		t.Errorf("stdout must not contain validate/retry: %s", stdout)
	}
}

func TestM1_10_DryRunStateBagResolutions(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "adversarial-review", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "State Bag Resolutions:") {
		t.Fatalf("stdout missing State Bag resolutions: %s", stdout)
	}
	if !strings.Contains(stdout, "steps.impl.output") || !strings.Contains(stdout, "→") {
		t.Fatalf("stdout missing steps.impl.output / arrow: %s", stdout)
	}
}

func TestM1_11_ParserMaxBudget(t *testing.T) {
	yaml := []byte(`name: x
max_budget: 5.00
steps:
  - name: s
    agent: a
    max_budget: 2.00
`)
	r, err := recipe.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := recipe.Validate(r); len(errs) != 0 {
		t.Fatalf("validate errs: %v", errs)
	}
	if r.MaxBudget != 5.0 {
		t.Fatalf("Recipe.MaxBudget: got %v", r.MaxBudget)
	}
	if r.Steps[0].MaxBudget != 2.0 {
		t.Fatalf("Step.MaxBudget: got %v", r.Steps[0].MaxBudget)
	}
}

func TestM1_12_ParserHitl(t *testing.T) {
	yaml := []byte(`name: x
steps:
  - name: s
    agent: a
    hitl: true
`)
	r, err := recipe.Parse(yaml, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(r.Steps) == 0 || !r.Steps[0].HITL {
		t.Fatalf("Step.HITL: expected true, got %+v", r.Steps)
	}
}

func TestM1_13_BuiltinRecipesParseAndValidate(t *testing.T) {
	names := []string{
		"tdd.yaml",
		"cheap2sota.yaml",
		"parallel-tasks.yaml",
		"adversarial-review.yaml",
		"bugfix.yaml",
		"refactor.yaml",
		"freeform.yaml",
	}
	for _, k := range names {
		raw := recipe.BuiltinRecipes[k]
		if len(raw) == 0 {
			t.Fatalf("missing builtin recipe %s", k)
		}
		r, err := recipe.Parse(raw, "")
		if err != nil {
			t.Fatalf("parse %s: %v", k, err)
		}
		if errs := recipe.Validate(r); len(errs) != 0 {
			t.Fatalf("validate %s: %v", k, errs)
		}
	}
}
