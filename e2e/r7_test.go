package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/isomorphx/gump/internal/builtin"
	"github.com/isomorphx/gump/internal/workflow"
)

func TestE2E_R7_01_BuiltinsParseValidate(t *testing.T) {
	for _, k := range []string{
		"tdd.yaml", "cheap2sota.yaml", "parallel-tasks.yaml",
		"implement-spec.yaml", "bugfix.yaml", "refactor.yaml", "freeform.yaml",
	} {
		raw := workflow.BuiltinWorkflows[k]
		if len(raw) == 0 {
			t.Fatalf("missing workflow %s", k)
		}
		wf, warns, err := workflow.Parse(raw, "")
		if err != nil {
			t.Fatalf("parse %s: %v", k, err)
		}
		if len(warns) != 0 {
			t.Fatalf("%s: unexpected warnings %v", k, warns)
		}
		if errs := workflow.Validate(wf); len(errs) != 0 {
			t.Fatalf("validate %s: %v", k, errs)
		}
	}
	raw := workflow.BuiltinValidators["arch-review.yaml"]
	if len(raw) == 0 {
		t.Fatal("missing builtin validators/arch-review")
	}
	wf, warns, err := workflow.Parse(raw, "")
	if err != nil {
		t.Fatalf("parse arch-review: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("arch-review warnings: %v", warns)
	}
	if errs := workflow.Validate(wf); len(errs) != 0 {
		t.Fatalf("validate arch-review: %v", errs)
	}
}

func TestE2E_R7_02_FreeformFullRun(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "do something")
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	if runID == "" {
		t.Fatal("no run id")
	}
	st := readRunState(t, dir, runID)
	if st["execute.status"] != "pass" || st["execute.output"] == "" || st["execute.agent"] != "claude-opus" {
		t.Fatalf("state execute: status=%q output empty=%v agent=%q", st["execute.status"], st["execute.output"] == "", st["execute.agent"])
	}
	man := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	for _, sub := range []string{"run_started", "step_started", "agent_launched", "agent_completed", "gate_started", "gate_passed", "step_completed", "run_completed"} {
		if !strings.Contains(man, sub) {
			t.Errorf("ledger missing %q", sub)
		}
	}
}

func TestE2E_R7_03_TDD_SplitEachRedGreen(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "feature")
	writeFile(t, dir, ".gump-test-plan.json", `[
  {"name":"t1","description":"d1","files":["t1.go","t1_test.go"]},
  {"name":"t2","description":"d2","files":["t2.go","t2_test.go"]}
]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_task_step": {
    "t1": {
      "red": {"files": {
        "t1_test.go": "package main\nimport \"testing\"\nfunc TestT1(t *testing.T) { if T1() != 1 { t.Fatal() } }\n",
        "t1.go": "package main\n\nfunc T1() int { return 0 }\n"
      }},
      "green": {"files": {
        "t1.go": "package main\n\nfunc T1() int { return 1 }\n"
      }}
    },
    "t2": {
      "red": {"files": {
        "t2_test.go": "package main\nimport \"testing\"\nfunc TestT2(t *testing.T) { if T2() != 2 { t.Fatal() } }\n",
        "t2.go": "package main\n\nfunc T2() int { return 0 }\n"
      }},
      "green": {"files": {
        "t2.go": "package main\n\nfunc T2() int { return 2 }\n"
      }}
    }
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "tdd", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	st := readRunState(t, dir, runID)
	for _, p := range []string{
		"decompose/t1/red.output", "decompose/t1/green.output",
		"decompose/t2/red.output", "decompose/t2/green.output",
	} {
		if st[p] == "" {
			t.Fatalf("missing state %q", p)
		}
	}
	if st["quality.status"] != "pass" {
		t.Fatalf("quality gate: want pass, got %q", st["quality.status"])
	}
	_ = stderr
	_ = stdout
}

func TestE2E_R7_04_TDD_GreenRetryPrompt(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"z","description":"dz","files":["z.go","z_test.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_step": {
    "red": {"files": {
      "z_test.go": "package main\nimport \"testing\"\nfunc TestZ(t *testing.T) { if Z()!=1 { t.Fatal() } }\n",
      "z.go": "package main\n\nfunc Z() int { return 0 }\n"
    }}
  },
  "by_attempt": {
    "2": {"files": {
      "z_test.go": "package main\nimport \"testing\"\nfunc TestZ(t *testing.T) { if Z()!=1 { t.Fatal() } }\n",
      "z.go": "package main\n\nfunc Z() int { return 0 }\n"
    }},
    "3": {"files": {
      "z_test.go": "package main\nimport \"testing\"\nfunc TestZ(t *testing.T) { if Z()!=1 { t.Fatal() } }\n",
      "z.go": "package main\n\nfunc Z() int { return 1 }\n"
    }}
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "tdd", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/z/green.attempt"] != "2" {
		t.Fatalf("expected green retry attempt 2, got %q", st["decompose/z/green.attempt"])
	}
}

func TestE2E_R7_05_Cheap2SotaEscalation(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"x","description":"ix","files":["x.go"]}]`)
	// cheap2sota.yaml: impl attempts 1–2 qwen, 3 haiku, 4 claude-sonnet; stub skips counter for root `decompose` plan so by_attempt keys match workflow attempts.
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_attempt": {
    "1": {"files": {"x.go": "package main\n\nfunc X() { !!! }\n"}},
    "2": {"files": {"x.go": "package main\n\nfunc X() { !!! }\n"}},
    "3": {"files": {"x.go": "package main\n\nfunc X() { !!! }\n"}},
    "4": {"files": {"x.go": "package main\n\nfunc X() int { return 1 }\n"}}
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "cheap2sota", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/x/impl.status"] != "pass" {
		t.Fatalf("expected impl pass, got status %q", st["decompose/x/impl.status"])
	}
	if st["decompose/x/impl.agent"] != "claude-sonnet" {
		t.Fatalf("expected final agent claude-sonnet, got %q", st["decompose/x/impl.agent"])
	}
	if st["decompose/x/impl.attempt"] != "4" {
		t.Fatalf("expected success on workflow attempt 4 (yaml: claude-sonnet), got attempt %q", st["decompose/x/impl.attempt"])
	}
	_ = stderr
}

func TestE2E_R7_06_ParallelTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-plan.json", `[
  {"name":"a","description":"impl a","files":["a.go","a_test.go"]},
  {"name":"b","description":"impl b","files":["b.go","b_test.go"]}
]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_task_step": {
    "a": {"impl": {"files": {
      "a.go": "package main\n\nfunc A() int { return 1 }\n",
      "a_test.go": "package main\nimport \"testing\"\nfunc TestA(t *testing.T) { if A()!=1 { t.Fatal() } }\n"
    }}},
    "b": {"impl": {"files": {
      "b.go": "package main\n\nfunc B() int { return 2 }\n",
      "b_test.go": "package main\nimport \"testing\"\nfunc TestB(t *testing.T) { if B()!=2 { t.Fatal() } }\n"
    }}}
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parallel-tasks", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/a/impl.output"] == "" || st["decompose/b/impl.output"] == "" {
		t.Fatalf("expected both impl outputs: a=%q b=%q", st["decompose/a/impl.output"], st["decompose/b/impl.output"])
	}
	if st["quality.status"] != "pass" {
		t.Fatalf("quality: %q", st["quality.status"])
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var tA, tB time.Time
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "step_started" {
			continue
		}
		step, _ := ev["step"].(string)
		ts, _ := ev["ts"].(string)
		tm, err := time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			continue
		}
		switch step {
		case "decompose/a/impl":
			tA = tm
		case "decompose/b/impl":
			tB = tm
		}
	}
	if tA.IsZero() || tB.IsZero() {
		t.Fatal("missing step_started for parallel impl steps")
	}
	d := tA.Sub(tB)
	if d < 0 {
		d = -d
	}
	if d > 5*time.Second {
		t.Fatalf("impl starts not parallel enough: %v vs %v", tA, tB)
	}
	_ = stderr
	_ = stdout
}

func TestE2E_R7_07_ImplementSpecReviewRetry(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "spec body")
	writeFile(t, dir, "docs/architecture.md", "# arch\n")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"only","description":"task one","files":["main.go","main_test.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "review_by_attempt": {
    "1": "{\"pass\":false,\"comments\":\"missing error handling\"}",
    "2": "{\"pass\":true,\"comments\":\"ok\"}"
  },
  "by_step": {
    "converge": {"files": {
      "main.go": "package main\n\nfunc main() {}\n",
      "main_test.go": "package main\nimport \"testing\"\nfunc TestM(t *testing.T) {}\n"
    }},
    "smoke": {"files": {"main.go": "package main\n\nfunc main() {}\n"}}
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "implement-spec", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	gumpDir := filepath.Join(dir, ".gump")
	if !worktreeTreeContains(t, gumpDir, "Deviations remain: missing error handling") {
		t.Fatal("retry converge prompt should appear under .gump (runs/worktrees/context)")
	}
	prevComments := readPrevField(t, dir, runID, "decompose/only/converge", "gate.review.comments")
	if prevComments != "missing error handling" {
		t.Fatalf("prev gate.review.comments: want %q got %q", "missing error handling", prevComments)
	}
	st := readRunState(t, dir, runID)
	if st["decompose/only/converge.status"] != "pass" {
		t.Fatalf("converge status: %q", st["decompose/only/converge.status"])
	}
	if st["decompose/only/smoke.agent"] != "claude-sonnet" {
		t.Fatalf("smoke should reuse converge agent, got %q", st["decompose/only/smoke.agent"])
	}
	_ = stderr
}

func worktreeTreeContains(t *testing.T, root, sub string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found {
			return nil
		}
		if info.Size() > 2<<20 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), sub) {
			found = true
		}
		return nil
	})
	return found
}

func TestE2E_R7_08_ImplementSpecOpusAttempt4(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, "docs/architecture.md", "# a\n")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"z","description":"z","files":["z.go","z_test.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "review_by_attempt": {
    "1": "{\"pass\":false,\"comments\":\"r1\"}",
    "2": "{\"pass\":false,\"comments\":\"r2\"}",
    "3": "{\"pass\":false,\"comments\":\"r3\"}",
    "4": "{\"pass\":true,\"comments\":\"ok\"}"
  },
  "by_step": {
    "converge": {"files": {"z.go": "package main\n\nfunc Z() int { return 1 }\n","z_test.go": "package main\nimport \"testing\"\nfunc TestZ(t *testing.T) { if Z()!=1 { t.Fatal() } }\n"}},
    "smoke": {"files": {"z.go": "package main\n\nfunc Z() int { return 1 }\n"}}
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "implement-spec", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/z/converge.agent"] != "claude-opus" {
		t.Fatalf("attempt 4 should escalate to claude-opus, got %q", st["decompose/z/converge.agent"])
	}
	if st["decompose/z/smoke.agent"] != "claude-opus" {
		t.Fatalf("smoke should use resolved converge.agent (opus), got %q", st["decompose/z/smoke.agent"])
	}
	_ = stderr
	_ = stdout
}

func TestE2E_R7_09_BugfixReproducePatch(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "bug.md", "bug")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"bug","description":"repro","files":["fix.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_step": {
    "reproduce": {"files": {"fix_test.go": "package main\nimport \"testing\"\nfunc TestBug(t *testing.T) { t.Fatal(\"fail\") }\n"}},
    "patch": {"files": {"fix.go": "package main\n\nfunc Fix() bool { return true }\n","fix_test.go": "package main\nimport \"testing\"\nfunc TestBug(t *testing.T) { if !Fix() { t.Fatal() } }\n"}}
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "bug.md", "--workflow", "bugfix", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	_ = stdout
	_ = stderr
}

func TestE2E_R7_10_RefactorTwoTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "refactor")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"p","description":"dp","files":["p.go"]},{"name":"q","description":"dq","files":["q.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{"by_step":{"apply":{"files":{"p.go":"package main\n\nfunc P() {}\n","q.go":"package main\n\nfunc Q() {}\n"}}}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "refactor", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	_ = stdout
	_ = stderr
}

func TestE2E_R7_11_ArchReviewStandaloneBuiltin(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "ignored")
	writeFile(t, dir, ".gump-test-scenario.json", `{"review_by_step":{"review":"{\"pass\":true,\"comments\":\"Approved\"}"}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "validators/arch-review", "--set", `diff=some diff`, "--set", `spec=some spec`, "--set", "agent=claude-opus", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractRunID(stdout)
	st := readRunState(t, dir, runID)
	if st["review.output"] != "true" {
		t.Fatalf("review.output=%q", st["review.output"])
	}
}

func TestE2E_R7_12_ArchReviewPromptEscaping(t *testing.T) {
	raw := workflow.BuiltinValidators["arch-review.yaml"]
	s := string(raw)
	if !strings.Contains(s, `{"pass": true/false, "comments": "your review"}`) {
		t.Fatalf("expected literal JSON example in prompt, got substring check failed (len=%d)", len(s))
	}
}

func TestE2E_R7_13_DryRunImplementSpec(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "implement-spec", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	for _, w := range []string{"type=split", "1.1 converge", "1.2 smoke", "validate:validators/arch-review", "attempt:2→prompt", "attempt:4→opus+reset", "Budget:", "$20.00"} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout missing %q\n%s", w, stdout)
		}
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("expected no warnings, stderr=%s", stderr)
	}
}

func TestE2E_R7_14_WorkflowProjectOverridesBuiltin(t *testing.T) {
	dir := setupGoRepo(t)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/freeform.yaml", `name: freeform
max_budget: 1.00
steps:
  - name: execute
    type: code
    get:
      prompt: "{spec}"
    run:
      agent: claude-opus
      guard:
        max_turns: 80
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "$1.00") || strings.Contains(stdout, "$5.00") {
		t.Fatalf("expected project max_budget 1.00, stdout=%s", stdout)
	}
}

func TestE2E_R7_15_ValidatorProjectOverridesBuiltin(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "s")
	writeFile(t, dir, "docs/architecture.md", "# a\n")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"only","description":"x","files":["m.go"]}]`)
	os.MkdirAll(filepath.Join(dir, ".gump", "validators"), 0755)
	writeFile(t, dir, ".gump/validators/arch-review.yaml", `name: arch-review
steps:
  - name: review
    type: validate
    get:
      prompt: "R7_15_CUSTOM_MARKER {diff} {spec}"
    run:
      agent: "{agent}"
`)
	writeFile(t, dir, ".gump-test-scenario.json", `{"review_by_step":{"review":"{\"pass\":true,\"comments\":\"ok\"}"},"by_step":{"converge":{"files":{"m.go":"package main\n","m_test.go":"package main\nimport \"testing\"\nfunc TestM(t *testing.T){}\n"}},"smoke":{"files":{"m.go":"package main\n"}}}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "implement-spec", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	assertSomeFileUnderGumpContainsAll(t, dir, []string{"R7_15_CUSTOM_MARKER"})
	_ = stdout
}
