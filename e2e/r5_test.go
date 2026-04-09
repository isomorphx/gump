package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func manifestHasStepSubstring(t *testing.T, repoDir, runID, sub string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoDir, ".gump", "runs", runID, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), sub)
}

// assertStepStartedAgentForStepContaining fails unless a step_started ledger line for a step path containing sub records wantAgent (stub agent_launched may omit agent/cli; step_started does not).
func assertStepStartedAgentForStepContaining(t *testing.T, repoDir, runID, sub, wantAgent string) {
	t.Helper()
	path := filepath.Join(repoDir, ".gump", "runs", runID, "manifest.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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
		if !strings.Contains(step, sub) {
			continue
		}
		agent, _ := ev["agent"].(string)
		if agent == wantAgent {
			return
		}
	}
	t.Fatalf("no step_started with agent %q for step containing %q", wantAgent, sub)
}

func TestE2E_R5_01_GateValidatorPass(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/check.yaml", `name: check
steps:
  - name: analyze
    type: validate
    get:
      prompt: "check"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-01.yaml", `name: r5-01
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, validate: validators/check]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":true,\"comments\":\"Looks good\"}"},"files":{"main.go":"package main\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-01", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["impl.gate.check.pass"] != "true" || st["impl.gate.check.comments"] != "Looks good" {
		t.Fatalf("gate state: %+v", map[string]string{"pass": st["impl.gate.check.pass"], "comments": st["impl.gate.check.comments"]})
	}
	_ = stderr
}

func TestE2E_R5_02_GateValidatorFail(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/check.yaml", `name: check
steps:
  - name: analyze
    type: validate
    get:
      prompt: "check"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-02.yaml", `name: r5-02
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, validate: validators/check]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":false,\"comments\":\"Architecture violation\"}"},"files":{"main.go":"package main\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-02", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected fail stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	st := readRunState(t, dir, runID)
	if st["impl.gate.check.pass"] != "false" || st["impl.gate.check.comments"] != "Architecture violation" {
		t.Fatalf("gate state: %+v", st)
	}
}

func assertSomeFileUnderGumpContainsAll(t *testing.T, repoDir string, parts []string) {
	t.Helper()
	found := false
	_ = filepath.Walk(filepath.Join(repoDir, ".gump"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil || len(b) > 2<<20 {
			return nil
		}
		s := string(b)
		for _, p := range parts {
			if !strings.Contains(s, p) {
				return nil
			}
		}
		found = true
		return filepath.SkipAll
	})
	if !found {
		t.Fatalf("expected some file under .gump to contain all substrings: %v", parts)
	}
}

func TestE2E_R5_03_GateValidatorWithUsesOpus(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "SPEC_R5_03_SPEC_MARK")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/arch-review.yaml", `name: arch-review
steps:
  - name: analyze
    type: validate
    get:
      prompt: "d {diff} s {spec} a {agent}"
    run:
      agent: "{agent}"
`)
	writeFile(t, dir, ".gump/workflows/r5-03.yaml", `name: r5-03
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate:
      - compile
      - validate: validators/arch-review
        diff: "{diff}"
        spec: "{spec}"
        agent: claude-opus
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":true,\"comments\":\"ok\"}"},"files":{"main.go":"package main\n// DIFF_R5_03_MARK\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-03", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	assertStepStartedAgentForStepContaining(t, dir, runID, "gate/review", "claude-opus")
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) < 1 || agents[0] != "claude-sonnet" {
		t.Fatalf("parent step should stay sonnet, got %v", agents)
	}
	// WHY: proves gate with: values reached the nested GET prompt (not only run.agent).
	assertSomeFileUnderGumpContainsAll(t, dir, []string{"SPEC_R5_03_SPEC_MARK", "DIFF_R5_03_MARK", "claude-opus"})
	_ = stderr
}

func TestE2E_R5_04_GateValidatorCommentsInRetryPrompt(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/arch-review.yaml", `name: arch-review
steps:
  - name: analyze
    type: validate
    get:
      prompt: "review {diff}"
    run:
      agent: claude-opus
`)
	writeFile(t, dir, ".gump/workflows/r5-04.yaml", `name: r5-04
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
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
          Deviations: {gate.review.comments}
          Fix only these.
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":false,\"comments\":\"Missing error handling in auth module\"}"},"files":{"main.go":"package main\nfunc main(){}\n","main_test.go":"package main\nimport \"testing\"\nfunc TestM(t *testing.T){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-04", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit after validator keeps failing stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	st := readRunState(t, dir, runID)
	if st["impl.gate.review.comments"] != "Missing error handling in auth module" {
		t.Fatalf("gate review comments in state: %q", st["impl.gate.review.comments"])
	}
	// WHY: the gate validator run rewrites the same worktree CLAUDE.md after the code retry, so we assert ledger + run dir text instead of final CLAUDE.md only.
	var rt2 map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "retry_triggered" {
			continue
		}
		if s, _ := ev["step"].(string); s != "impl" {
			continue
		}
		if a, _ := ev["attempt"].(float64); int(a) == 2 {
			rt2 = ev
			break
		}
	}
	if rt2 == nil {
		t.Fatal("missing retry_triggered for impl attempt 2")
	}
	ov, _ := rt2["overrides"].(map[string]interface{})
	if ov == nil || ov["prompt"] != "overridden" {
		t.Fatalf("expected prompt override in ledger: %v", ov)
	}
	needle := "Deviations: Missing error handling in auth module"
	found := strings.Contains(stderr, needle)
	_ = filepath.Walk(filepath.Join(dir, ".gump"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil || len(b) > 2<<20 {
			return nil
		}
		if strings.Contains(string(b), needle) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if !found {
		t.Fatalf("expected resolved retry prompt text %q under .gump or stderr", needle)
	}
}

func TestE2E_R5_05_RetryValidatorMatch(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/assess-distance.yaml", `name: assess-distance
steps:
  - name: judge
    type: validate
    get:
      prompt: "assess"
    run:
      agent: claude-haiku
`)
	writeFile(t, dir, ".gump/workflows/r5-05.yaml", `name: r5-05
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - validate: validators/assess-distance
        with:
          diff: "{diff}"
          error: "{error}"
          agent: claude-haiku
        agent: claude-opus
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"judge":"{\"pass\":true,\"comments\":\"escalate\"}"},"by_attempt":{"1":{"files":{"main.go":"package main\n\nfunc Broken( { }\n"}},"2":{"files":{"main.go":"package main\nfunc main(){}\n"}},"3":{"files":{"main.go":"package main\nfunc main(){}\n"}},"4":{"files":{"main.go":"package main\nfunc main(){}\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-05", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) < 2 || agents[len(agents)-1] != "claude-opus" {
		t.Fatalf("want escalation to opus, got %v", agents)
	}
	_ = stderr
}

func TestE2E_R5_06_RetryValidatorNoMatch(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/assess-distance.yaml", `name: assess-distance
steps:
  - name: judge
    type: validate
    get:
      prompt: "assess"
    run:
      agent: claude-haiku
`)
	writeFile(t, dir, ".gump/workflows/r5-06.yaml", `name: r5-06
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - validate: validators/assess-distance
        with:
          diff: "{diff}"
        agent: claude-opus
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"judge":"{\"pass\":false,\"comments\":\"ok\"}"},"by_attempt":{"1":{"files":{"main.go":"package main\n\nfunc Broken( { }\n"}},"2":{"files":{"main.go":"package main\n\nfunc main(){}\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-06", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	for _, a := range agents {
		if a == "claude-opus" {
			t.Fatalf("did not want opus escalation, got %v", agents)
		}
	}
	_ = stderr
}

func TestE2E_R5_07_WorkflowAsStepStateMerge(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "deploy x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/workflows/setup-env.yaml", `name: setup-env
steps:
  - name: deploy
    type: validate
    get:
      prompt: "d"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-07.yaml", `name: r5-07
steps:
  - name: setup
    workflow: workflows/setup-env
    with:
      target: staging
  - name: impl
    type: code
    prompt: "Deploy target: {setup.state.deploy.comments}"
    agent: claude-sonnet
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"deploy":"{\"pass\":true,\"comments\":\"staging-ready\"}"},"files":{"main.go":"package main\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-07", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["setup.state.deploy.comments"] != "staging-ready" {
		t.Fatalf("expected merged validate comments, got %q", st["setup.state.deploy.comments"])
	}
	_ = stderr
}

func TestE2E_R5_08_WorkflowInGet(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "auth spec")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/research.yaml", `name: research
steps:
  - name: find
    type: validate
    get:
      prompt: "r {query}"
    run:
      agent: claude-haiku
`)
	writeFile(t, dir, ".gump/workflows/r5-08.yaml", "name: r5-08\nsteps:\n  - name: impl\n    type: code\n    get:\n      prompt: \"Based on research: {research.output} Implement {spec}\"\n      workflow: validators/research\n      query: \"best practices for authentication\"\n      agent: claude-haiku\n    run:\n      agent: claude-sonnet\n    gate: [compile]\n")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"find":"{\"pass\":true,\"comments\":\"research-result-text\"}"},"files":{"main.go":"package main\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-08", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["research.output"] != "true" {
		t.Fatalf("expected research.output from validate step, got %q", st["research.output"])
	}
	_ = stderr
}

func TestE2E_R5_09_StandaloneValidatorWithSet(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "ignored")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/arch-review.yaml", `name: arch-review
steps:
  - name: audit
    type: validate
    get:
      prompt: "d {diff} s {spec} a {agent}"
    run:
      agent: "{agent}"
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"audit":"{\"pass\":true,\"comments\":\"ok\"}"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "validators/arch-review", "--set", `diff=some diff`, "--set", `spec=some spec`, "--set", "agent=claude-opus", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["audit.output"] != "true" {
		t.Fatalf("expected audit validate output, got %q", st["audit.output"])
	}
	_ = stderr
}

func TestE2E_R5_10_DryRunSubworkflowWarning(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/arch-review.yaml", `name: arch-review
steps:
  - name: a
    type: validate
    get:
      prompt: "{diff}{spec}{agent}"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-10.yaml", `name: r5-10
steps:
  - name: review
    workflow: validators/arch-review
`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-10", "--dry-run"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "may not be available") || !strings.Contains(stderr, "diff") {
		t.Fatalf("expected dry-run warning for diff, stderr=%s", stderr)
	}
}

func TestE2E_R5_11_AllGatesEvaluated(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/check.yaml", `name: check
steps:
  - name: analyze
    type: validate
    get:
      prompt: "c"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-11.yaml", `name: r5-11
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, validate: validators/check, test]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":false,\"comments\":\"no\"}"},"files":{"main.go":"package main\nfunc main(){}\n","main_test.go":"package main\nimport \"testing\"\nfunc TestM(t *testing.T){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-11", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected fail stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	st := readRunState(t, dir, runID)
	if st["impl.gate.compile"] != "true" {
		t.Fatalf("compile should pass: %q", st["impl.gate.compile"])
	}
	if st["impl.gate.test"] != "true" {
		t.Fatalf("test should still run and pass: %q", st["impl.gate.test"])
	}
	if st["impl.gate.check.pass"] != "false" {
		t.Fatalf("validator should fail: %+v", st["impl.gate.check.pass"])
	}
}

func TestE2E_R5_12_LedgerQualifiedNestedStep(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/arch-review.yaml", `name: arch-review
steps:
  - name: analyze
    type: validate
    get:
      prompt: "x"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-12.yaml", `name: r5-12
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [validate: validators/arch-review]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":true,\"comments\":\"ok\"}"},"files":{"main.go":"package main\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-12", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	sub := "impl/gate/review/analyze"
	if !manifestHasStepSubstring(t, dir, runID, sub) {
		t.Fatalf("manifest missing qualified step %q", sub)
	}
}

func TestE2E_R5_13_CumulativeCostWithGateValidator(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/extra.yaml", `name: extra
steps:
  - name: v
    type: validate
    get:
      prompt: "v"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/r5-13.yaml", `name: r5-13
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, validate: validators/extra]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"cost_usd_by_step":{"impl":0.10,"v":0.05},"review_by_step":{"v":"{\"pass\":true,\"comments\":\"ok\"}"},"files":{"main.go":"package main\nfunc main(){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r5-13", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	cost, err := strconv.ParseFloat(strings.TrimSpace(st["run.cost"]), 64)
	if err != nil || cost < 0.14 || cost > 0.16 {
		t.Fatalf("expected ~0.15 run.cost, got %v (%q) err=%v", cost, st["run.cost"], err)
	}
	_ = stderr
}

func TestSmoke_R5_01_ArchReviewInGate(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows", "validators"), 0755)
	writeFile(t, dir, ".gump/workflows/validators/arch-review.yaml", `name: arch-review
steps:
  - name: analyze
    type: validate
    get:
      prompt: "x"
    run:
      agent: claude-sonnet
`)
	writeFile(t, dir, ".gump/workflows/smoke-r5-01.yaml", `name: smoke-r5-01
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test, validate: validators/arch-review]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"review_by_step":{"analyze":"{\"pass\":true,\"comments\":\"arch ok\"}"},"files":{"main.go":"package main\nfunc main(){}\n","main_test.go":"package main\nimport \"testing\"\nfunc TestM(t *testing.T){}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "smoke-r5-01", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["impl.gate.review.pass"] != "true" || st["impl.gate.review.comments"] != "arch ok" {
		t.Fatalf("gate state: %+v", map[string]string{"pass": st["impl.gate.review.pass"], "comments": st["impl.gate.review.comments"]})
	}
	_ = stderr
}

func TestSmoke_R5_02_AssessDistanceInRetry(t *testing.T) {
	TestE2E_R5_05_RetryValidatorMatch(t)
}

func TestSmoke_R5_03_StandaloneValidatorWithSet(t *testing.T) {
	TestE2E_R5_09_StandaloneValidatorWithSet(t)
}
