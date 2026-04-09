package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/workflow"
)

func readRunState(t *testing.T, repoDir, runID string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoDir, ".gump", "runs", runID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	m := make(map[string]string)
	for k, v := range raw {
		if k == state.PrevSnapshotJSONKey {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			m[k] = s
		}
	}
	return m
}

func readPrevField(t *testing.T, repoDir, runID, stepPath, field string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoDir, ".gump", "runs", runID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	pk, ok := raw[state.PrevSnapshotJSONKey]
	if !ok || len(pk) == 0 {
		return ""
	}
	var prev map[string]map[string]string
	if json.Unmarshal(pk, &prev) != nil {
		return ""
	}
	if prev == nil {
		return ""
	}
	row := prev[stepPath]
	if row == nil {
		return ""
	}
	return row[field]
}

func manifestEventTypes(t *testing.T, repoDir, runID string) string {
	t.Helper()
	p := filepath.Join(repoDir, ".gump", "runs", runID, "manifest.ndjson")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if typ, _ := m["type"].(string); typ != "" {
			types = append(types, typ)
		}
	}
	return strings.Join(types, " ")
}

func TestE2E_R3_01_SimpleCodeStepLedger(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "add func Add")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-freeform.yaml", `name: r3-freeform
steps:
  - name: execute
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")

	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-freeform", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	runID := extractCookID(stdout)
	if runID == "" {
		t.Fatal("no run id in output")
	}
	joined := manifestEventTypes(t, dir, runID)
	for _, want := range []string{"run_started", "step_started", "agent_launched", "agent_completed", "gate_started", "gate_passed", "step_completed", "run_completed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("manifest missing event %q: %s", want, joined)
		}
	}
	st := readRunState(t, dir, runID)
	if st["execute.output"] == "" {
		t.Error("expected non-empty execute.output in state")
	}
	if st["execute.gate.compile"] != "true" {
		t.Errorf("execute.gate.compile: want true, got %q", st["execute.gate.compile"])
	}
	stepTypeOK := false
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson")))), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "step_started" && ev["step_type"] == "code" {
			stepTypeOK = true
			break
		}
	}
	if !stepTypeOK {
		t.Error("manifest: expected step_started with step_type=code")
	}
}

func TestE2E_R3_04_AgentPassSkipsAgent(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-pass.yaml", `name: r3-pass
steps:
  - name: check
    type: code
    agent: pass
    gate: [bash: "test -f main.go"]
`)
	gitCommitAll(t, dir, "wf")

	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-pass", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	b, err := os.ReadFile(filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "agent_launched") || strings.Contains(s, "agent_completed") {
		t.Fatalf("agent should be skipped: %s", s)
	}
}

func TestE2E_R3_02_RetryGateThenPass(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "fix Add")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-retry.yaml", `name: r3-retry
steps:
  - name: execute
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile, test]
    retry:
      - exit: 3
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{"add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 { t.Fatal() }\n}\n"}}`)
	gitCommitAll(t, dir, "wf")
	env := envWithStubPath()
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-retry", "--agent-stub"}, env, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["execute.status"] != "pass" {
		t.Fatalf("status: %q", st["execute.status"])
	}
	if st["execute.attempt"] != "2" {
		t.Fatalf("attempt: want 2, got %q", st["execute.attempt"])
	}
	joined := manifestEventTypes(t, dir, runID)
	if !strings.Contains(joined, "retry_triggered") {
		t.Fatalf("expected retry_triggered in %s", joined)
	}
	if got := readPrevField(t, dir, runID, "execute", "gate.compile"); got != "false" {
		t.Fatalf("prev gate.compile after retry pass: want false, got %q", got)
	}
}

func TestE2E_R3_03_RetryExhaustedFatal(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-fatal.yaml", `name: r3-fatal
steps:
  - name: execute
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
    retry:
      - exit: 2
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add( { }\n"}}`)
	gitCommitAll(t, dir, "wf")
	env := envWithStubPath()
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-fatal", "--agent-stub"}, env, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit, stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	if runID == "" {
		t.Fatal("no run id in output")
	}
	st := readRunState(t, dir, runID)
	if st["execute.status"] != "fatal" && st["execute.status"] != "fail" {
		t.Fatalf("status: %q (want fatal or fail after exhausted retries)", st["execute.status"])
	}
}

func TestE2E_R3_05_GateOnlySecondStep(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-gateonly.yaml", `name: r3-gateonly
steps:
  - name: code
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
  - name: check
    gate: [test]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-gateonly", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	b := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if strings.Count(b, "agent_launched") != 1 {
		t.Fatalf("expected exactly one agent_launched, got:\n%s", b)
	}
}

func TestE2E_R3_06_SessionFromReusesResume(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-sess.yaml", `name: r3-sess
steps:
  - name: impl
    type: code
    prompt: "first"
    agent: claude-opus
    gate: [compile]
  - name: next
    type: code
    prompt: "second"
    agent: claude-opus
    session:
      from: impl
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"session_id_by_step":{"impl":"sess-from-impl"},"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-sess", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("run failed: %s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	launched, _, _ := parseLedger(t, filepath.Join(dir, ".gump", "runs", runID))
	if len(launched) < 2 {
		t.Fatalf("want 2 launches, got %v", launched)
	}
	if !strings.Contains(launched[1], "--resume") || !strings.Contains(launched[1], "sess-from-impl") {
		t.Fatalf("second launch should resume session: %q", launched[1])
	}
}

func TestE2E_R3_07_SessionFromProviderMismatchWarning(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-mismatch.yaml", `name: r3-mismatch
steps:
  - name: step1
    type: code
    prompt: "a"
    agent: claude-sonnet
    gate: [compile]
  - name: step2
    type: code
    prompt: "b"
    agent: codex-5
    session:
      from: step1
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-mismatch", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "different agent/provider") {
		t.Fatalf("expected provider mismatch warning in stderr: %s", stderr)
	}
}

func TestE2E_R3_08_WorktreeNoneUsesTempForAgent(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-none.yaml", `name: r3-none
steps:
  - name: v
    type: validate
    worktree: none
    prompt: "review"
    agent: claude-opus
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-none", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	b := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	var agentWT string
	for _, line := range strings.Split(strings.TrimSpace(b), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "agent_launched" {
			agentWT, _ = ev["worktree"].(string)
			break
		}
	}
	if agentWT == "" {
		t.Fatal("no agent_launched worktree")
	}
	mainWT := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	if agentWT == mainWT || strings.HasPrefix(agentWT, mainWT+string(filepath.Separator)) {
		t.Fatalf("agent worktree should not be main run worktree: %q vs %q", agentWT, mainWT)
	}
	st := readRunState(t, dir, runID)
	if !strings.Contains(st["v.output"], "true") {
		t.Fatalf("validate output in state: %q", st["v.output"])
	}
}

// TestE2E_R3_09_ReadOnlyNoWritePostStep : worktree read-only + guard no_write explicite (le cas split implicite sera couvert quand split+each sera implémenté côté moteur, R6).
func TestE2E_R3_09_ReadOnlyNoWritePostStep(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-nowrite.yaml", `name: r3-nowrite
steps:
  - name: split
    type: code
    worktree: read-only
    guard: { no_write: true }
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"evil.go":"package main\n\nfunc Evil() {}\n"}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-nowrite", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected failure, stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "no_write") {
		t.Fatalf("expected no_write in stderr: %s", stderr)
	}
}

func TestE2E_R3_10_GlobalMaxBudgetAfterStep(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-budget.yaml", `name: r3-budget
max_budget: 0.10
steps:
  - name: spend
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"cost_usd": 0.15,"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-budget", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected fatal for global budget, stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "budget") && !strings.Contains(stderr, "max_budget") {
		t.Fatalf("stderr: %s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	if runID != "" {
		b := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
		if !strings.Contains(b, "budget_exceeded") {
			t.Fatalf("expected budget_exceeded in manifest")
		}
	}
}

func TestE2E_R3_11_HITLBeforeGateAutoContinue(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-hitl1.yaml", `name: r3-hitl1
steps:
  - name: g
    type: code
    hitl: before_gate
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	env := envWithStubPath()
	env["GUMP_E2E_AUTO_HITL_CONTINUE"] = "1"
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-hitl1", "--agent-stub"}, env, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	b := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if !strings.Contains(b, "hitl_paused") || !strings.Contains(b, "hitl_resumed") {
		t.Fatalf("manifest: %s", b)
	}
}

func TestE2E_R3_12_HITLAfterGateFailThenRetry(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-hitl2.yaml", `name: r3-hitl2
steps:
  - name: g
    type: code
    hitl: after_gate
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
    retry:
      - exit: 3
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	env := envWithStubPath()
	env["GUMP_E2E_AUTO_HITL_CONTINUE"] = "1"
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-hitl2", "--agent-stub"}, env, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	b := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if !strings.Contains(b, "hitl_paused") || !strings.Contains(b, "retry_triggered") {
		t.Fatalf("manifest: %s", b)
	}
}

func TestE2E_R3_13_GuardMaxTokensKill(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-tok.yaml", `name: r3-tok
steps:
  - name: g
    type: code
    prompt: "{spec}"
    agent: claude-opus
    guard: { max_tokens: 100 }
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"tokens_in":200,"tokens_out":50,"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-tok", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected failure stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "max_tokens") && !strings.Contains(stderr, "guard") {
		t.Fatalf("stderr: %s", stderr)
	}
}

func TestE2E_R3_14_GuardMaxTimeKill(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-time.yaml", `name: r3-time
steps:
  - name: g
    type: code
    prompt: "{spec}"
    agent: claude-opus
    guard: { max_time: 1ns }
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-time", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected failure stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "max_time") && !strings.Contains(stderr, "guard") {
		t.Fatalf("stderr: %s", stderr)
	}
}

func TestE2E_R3_15_DryRun(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-dry.yaml", `name: r3-dry
steps:
  - name: execute
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-dry", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "r3-dry") && !strings.Contains(stdout, "execute") {
		t.Fatalf("stdout should show workflow: %s", stdout)
	}
}

func TestE2E_R3_16_ResumeFatalRun(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-resume.yaml", `name: r3-resume
steps:
  - name: prep
    type: code
    prompt: "x"
    agent: claude-opus
    gate: [compile]
  - name: code
    type: code
    prompt: "x"
    agent: claude-opus
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_attempt": {
    "2": {"files": {"bad.go": "package main\n\nfunc X() { SYNTAXERROR }\n"}},
    "3": {"files": {"bad.go": "package main\n\nfunc X() {}\n"}}
  }
}`)
	gitCommitAll(t, dir, "wf")
	stdout1, stderr1, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-resume", "--agent-stub"}, envWithStubPath(), dir)
	if code1 == 0 {
		t.Fatalf("expected first run fatal, got exit 0 stdout=%s stderr=%s", stdout1, stderr1)
	}
	runID := extractCookID(stdout1 + stderr1)
	if runID == "" {
		t.Fatal("no run id")
	}
	st1 := readRunState(t, dir, runID)
	if st1["prep.status"] != "pass" {
		t.Fatalf("prep should pass before fatal: status=%q", st1["prep.status"])
	}
	stdout2, stderr2, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, envWithStubPath(), dir)
	if code2 != 0 {
		t.Fatalf("resume exit %d stdout=%s stderr=%s", code2, stdout2, stderr2)
	}
	manifest := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if !strings.Contains(manifest, `"type":"run_resumed"`) {
		t.Fatalf("manifest should contain run_resumed: %s", manifest)
	}
	st2 := readRunState(t, dir, runID)
	if st2["prep.status"] != "pass" {
		t.Fatalf("after resume prep should stay pass: %q", st2["prep.status"])
	}
	if st2["code.status"] != "pass" {
		t.Fatalf("after resume code should pass: %q", st2["code.status"])
	}
	if st2["code.attempt"] != "1" {
		t.Fatalf("code attempt after resume should restart at 1, got %q", st2["code.attempt"])
	}
}

func TestE2E_R3_17_SplitWithoutEachRejected(t *testing.T) {
	raw := "name: bad\nsteps:\n  - name: s\n    type: split\n    prompt: p\n    agent: claude-opus\n"
	wf, _, err := workflow.Parse([]byte(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	errs := workflow.Validate(wf)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for split without each")
	}
}

func TestE2E_R3_18_AllGatesRunWhenCompileFails(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-allgates.yaml", `name: r3-allgates
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile, test, lint]
    retry:
      - exit: 1
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add( { }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-allgates", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected failure: %s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	if runID == "" {
		t.Fatal("no run id")
	}
	st := readRunState(t, dir, runID)
	if st["impl.gate.compile"] != "false" {
		t.Fatalf("impl.gate.compile: %q", st["impl.gate.compile"])
	}
	if st["impl.gate.test"] == "" {
		t.Fatal("expected impl.gate.test after compile failure")
	}
	if st["impl.gate.lint"] == "" {
		t.Fatal("expected impl.gate.lint after compile failure")
	}
}

func TestSmoke_R3_FreeformRun(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "add func")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	base := filepath.Join(dir, ".gump", "runs", runID)
	for _, rel := range []string{"manifest.ndjson", "state.json", "workflow-snapshot.yaml"} {
		if _, err := os.Stat(filepath.Join(base, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestSmoke_R3_02_ValidateStep(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "check")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-smoke-val.yaml", `name: r3-smoke-val
steps:
  - name: v
    type: validate
    worktree: none
    prompt: "review"
    agent: claude-opus
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-smoke-val", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	joined := manifestEventTypes(t, dir, runID)
	if !strings.Contains(joined, "step_completed") {
		t.Fatalf("ledger missing step_completed: %s", joined)
	}
	st := readRunState(t, dir, runID)
	if st["v.output"] != "true" && st["v.output"] != "false" {
		t.Fatalf("expected boolean string in v.output, got %q", st["v.output"])
	}
}

func TestSmoke_R3_03_DirectoryLayout(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r3-smoke-layout.yaml", `name: r3-smoke-layout
steps:
  - name: s
    type: code
    prompt: "{spec}"
    agent: claude-opus
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r3-smoke-layout", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	gump := filepath.Join(dir, ".gump")
	for _, p := range []string{
		filepath.Join(gump, "runs", "index.ndjson"),
		filepath.Join(gump, "runs", runID, "manifest.ndjson"),
		filepath.Join(gump, "runs", runID, "workflow-snapshot.yaml"),
		filepath.Join(gump, "runs", runID, "state.json"),
		filepath.Join(gump, "worktrees", "run-"+runID),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing expected path %s: %v", p, err)
		}
	}
}
