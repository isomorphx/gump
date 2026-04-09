package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nthLaunchedWorktree(launched []map[string]interface{}, stepPath string, n int) string {
	count := 0
	for _, ev := range launched {
		if s, _ := ev["step"].(string); s != stepPath {
			continue
		}
		count++
		if count == n {
			if w, _ := ev["worktree"].(string); w != "" {
				return w
			}
			return ""
		}
	}
	return ""
}

func agentsLaunchedForStep(t *testing.T, repoDir, runID, stepPath string) []string {
	t.Helper()
	launched := parseLedgerLaunchedByStep(t, filepath.Join(repoDir, ".gump", "runs", runID))
	var out []string
	for _, ev := range launched {
		if s, _ := ev["step"].(string); s != stepPath {
			continue
		}
		if a, _ := ev["agent"].(string); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func retryTriggeredEventsForStep(t *testing.T, repoDir, runID, stepPath string) []map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoDir, ".gump", "runs", runID, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
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
		if s, _ := ev["step"].(string); s == stepPath {
			out = append(out, ev)
		}
	}
	return out
}

func lastRetryTriggered(t *testing.T, repoDir, runID, stepPath string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoDir, ".gump", "runs", runID, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var last map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
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
		if s, _ := ev["step"].(string); s == stepPath {
			last = ev
		}
	}
	return last
}

func TestE2E_R4_01_AttemptConditionalAgent(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "fix")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-01.yaml", `name: r4-01
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - attempt: 3
        agent: claude-opus
      - exit: 5
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"3":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{"add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 { t.Fatal() }\n}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-01", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) < 3 {
		t.Fatalf("want >=3 launches, got %v", agents)
	}
	if agents[0] != "claude-sonnet" || agents[1] != "claude-sonnet" {
		t.Fatalf("attempts 1-2 agent: %v", agents)
	}
	if agents[2] != "claude-opus" {
		t.Fatalf("attempt 3 agent: %v", agents)
	}
	rt := lastRetryTriggered(t, dir, runID, "impl")
	if rt == nil {
		t.Fatal("no retry_triggered")
	}
	ov, _ := rt["overrides"].(map[string]interface{})
	if ov == nil || ov["agent"] != "claude-opus" {
		t.Fatalf("overrides: %v", ov)
	}
	if !strings.Contains(stderr, "forcing new session") {
		t.Fatalf("expected session force stderr, got: %s", stderr)
	}
}

func TestE2E_R4_02_StickyAgentEscalation(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-02.yaml", `name: r4-02
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - attempt: 2
        agent: claude-sonnet-thinking
      - attempt: 4
        agent: claude-opus
      - exit: 6
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add( { }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-02", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected fatal stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	// WHY: exit:6 allows attempts 1–6; the 7th scheduling call is fatal without a 7th launch.
	want := []string{"claude-sonnet", "claude-sonnet-thinking", "claude-sonnet-thinking", "claude-opus", "claude-opus", "claude-opus"}
	if len(agents) != len(want) {
		t.Fatalf("agents len %d vs %d: %v", len(agents), len(want), agents)
	}
	for i := range want {
		if agents[i] != want[i] {
			t.Fatalf("idx %d: want %q got %q", i, want[i], agents[i])
		}
	}
}

func TestE2E_R4_03_NotGateTestFail(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-03.yaml", `name: r4-03
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - not: gate.test
        agent: claude-opus
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"fail\") }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2)!=3 { t.Fatal() } }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-03", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) < 2 || agents[1] != "claude-opus" {
		t.Fatalf("agents: %v", agents)
	}
}

func TestE2E_R4_04_NotGateCompilePassNoOverride(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-04.yaml", `name: r4-04
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test, lint]
    retry:
      - not: gate.compile
        agent: claude-opus
      - exit: 3
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"fail\") }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-04", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected fail stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) < 2 || agents[1] != "claude-sonnet" {
		t.Fatalf("second launch should stay sonnet: %v", agents)
	}
}

func TestE2E_R4_05_PromptOverrideResolvesGateMeta(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "check")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-gate-meta.yaml", `name: r4-gate-meta
steps:
  - name: impl
    type: code
    prompt: "Implement {spec}"
    agent: claude-sonnet
    gate:
      - compile
      - bash: "test -f .gump/e2e-r4-05-ok || (echo 'missing error handling in auth' >&2; exit 1)"
    retry:
      - attempt: 2
        prompt: |
          Gate errors: {gate.bash_1.error}
          Compile: {gate.compile}
          Fix these issues only.
      - exit: 3
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2)!=3 { t.Fatal() } }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2)!=3 { t.Fatal() } }\n",".gump/e2e-r4-05-ok":"ok"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-gate-meta", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	launched := parseLedgerLaunchedByStep(t, filepath.Join(dir, ".gump", "runs", runID))
	wt2 := nthLaunchedWorktree(launched, "impl", 2)
	if wt2 == "" {
		t.Fatal("no worktree for second launch of impl")
	}
	body := readFile(t, filepath.Join(wt2, "CLAUDE.md"))
	if strings.Contains(body, "Previous Attempt Failed") {
		t.Fatalf("should not inject retry section: %s", body)
	}
	if !strings.Contains(body, "missing error handling in auth") {
		t.Fatalf("expected bash stderr in context: %s", body)
	}
	if !strings.Contains(body, "true") {
		t.Fatalf("expected compile pass marker in context: %s", body)
	}
}

func TestE2E_R4_06_NoPromptOverrideKeepsRetrySection(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "fix")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-06.yaml", `name: r4-06
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - attempt: 3
        agent: claude-opus
      - exit: 5
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{"add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 { t.Fatal() }\n}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-06", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	launched := parseLedgerLaunchedByStep(t, filepath.Join(dir, ".gump", "runs", runID))
	wt2 := nthLaunchedWorktree(launched, "impl", 2)
	if wt2 == "" {
		t.Fatal("no worktree for second launch of impl")
	}
	body := readFile(t, filepath.Join(wt2, "CLAUDE.md"))
	if !strings.Contains(body, "Previous Attempt Failed") {
		t.Fatalf("expected retry section: %s", body)
	}
}

func TestE2E_R4_07_LedgerSessionNewOnAgentChange(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-07.yaml", `name: r4-07
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - attempt: 2
        agent: claude-opus
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-07", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	rt := lastRetryTriggered(t, dir, runID, "impl")
	ov, _ := rt["overrides"].(map[string]interface{})
	if ov == nil || ov["session"] != "new" {
		t.Fatalf("overrides: %v", ov)
	}
	if !strings.Contains(stderr, "forcing new session") {
		t.Fatalf("stderr: %s", stderr)
	}
}

func TestE2E_R4_08_ExplicitSessionNoExtraWarning(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-08.yaml", `name: r4-08
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - attempt: 2
        agent: claude-opus
        session: new
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-08", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "forcing new session") {
		t.Fatalf("unexpected implicit warning: %s", stderr)
	}
}

func TestE2E_R4_09_WorktreeReset(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-09.yaml", `name: r4-09
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - attempt: 2
        worktree: reset
      - exit: 3
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"foo.go":"package main\n\nfunc Foo() {}\n","add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-09", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	if _, err := os.Stat(filepath.Join(wt, "foo.go")); err == nil {
		t.Fatal("foo.go should be gone after reset")
	}
	_ = stderr
}

func TestE2E_R4_10_WorktreeResetSessionWarning(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-10.yaml", `name: r4-10
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - attempt: 2
        worktree: reset
      - exit: 3
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-10", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "worktree reset without new session") {
		t.Fatalf("stderr: %s", stderr)
	}
}

func TestE2E_R4_11_ValidatePlaceholderError(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-11.yaml", `name: r4-11
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
          agent: claude-haiku
        agent: claude-opus
      - exit: 4
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add( { }\n"}}`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-11", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(stderr, "workflow validators") && !strings.Contains(stderr, "validators") {
		t.Fatalf("stderr: %s", stderr)
	}
}

func retryTriggeredAttempt(ev map[string]interface{}) int {
	switch v := ev["attempt"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func TestE2E_R4_12_MultiEntriesOrderedOverrides(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "spec body")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-multi-entries.yaml", `name: r4-multi-entries
steps:
  - name: impl
    type: code
    prompt: "Implement {spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - attempt: 2
        prompt: "Fix: {error}"
      - attempt: 4
        agent: claude-opus
        session: new
        worktree: reset
      - exit: 5
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add( { }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-multi-entries", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatalf("expected fatal exit, stderr=%s", stderr)
	}
	runID := extractCookID(stdout + stderr)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	wantAgents := []string{"claude-sonnet", "claude-sonnet", "claude-sonnet", "claude-opus", "claude-opus"}
	if len(agents) != len(wantAgents) {
		t.Fatalf("want %d agent launches, got %d: %v", len(wantAgents), len(agents), agents)
	}
	for i := range wantAgents {
		if agents[i] != wantAgents[i] {
			t.Fatalf("launch %d: want %q got %q", i+1, wantAgents[i], agents[i])
		}
	}
	launched := parseLedgerLaunchedByStep(t, filepath.Join(dir, ".gump", "runs", runID))
	for n := 2; n <= 5; n++ {
		wt := nthLaunchedWorktree(launched, "impl", n)
		if wt == "" {
			t.Fatalf("no worktree for impl launch %d", n)
		}
		body := readFile(t, filepath.Join(wt, "CLAUDE.md"))
		if strings.Contains(body, "Previous Attempt Failed") {
			t.Fatalf("attempt %d: must not inject retry section when prompt overridden", n)
		}
	}
	rts := retryTriggeredEventsForStep(t, dir, runID, "impl")
	if len(rts) != 4 {
		t.Fatalf("want 4 retry_triggered before attempts 2–5, got %d", len(rts))
	}
	for _, ev := range rts {
		ov, ok := ev["overrides"].(map[string]interface{})
		if !ok {
			t.Fatalf("missing overrides: %v", ev)
		}
		if p, _ := ov["prompt"].(string); p != "overridden" {
			t.Fatalf("want prompt overridden marker, got %v (event attempt %v)", ov["prompt"], ev["attempt"])
		}
	}
	for _, ev := range rts {
		a := retryTriggeredAttempt(ev)
		if a != 2 && a != 3 {
			continue
		}
		ov := ev["overrides"].(map[string]interface{})
		if ag, _ := ov["agent"].(string); ag != "" {
			t.Fatalf("attempt %d: expected no agent override in ledger, got %q", a, ag)
		}
	}
	for _, ev := range rts {
		a := retryTriggeredAttempt(ev)
		if a != 4 && a != 5 {
			continue
		}
		ov := ev["overrides"].(map[string]interface{})
		if ov["agent"] != "claude-opus" || ov["session"] != "new" || ov["worktree"] != "reset" {
			t.Fatalf("attempt %d: want opus/new/reset in overrides, got %v", a, ov)
		}
	}
	if !strings.Contains(stderr, "all attempts exhausted") && !strings.Contains(stderr, "FATAL") {
		t.Fatalf("expected fatal message in stderr: %s", stderr)
	}
	// NOTE: exit:N caps total attempts at N (same as R4-02). Five launches require exit:5, not exit:6.
}

func TestSmoke_R4_01_ImplSimplePromptOverrideEscalation(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "do add")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/impl-simple.yaml", `name: impl-simple
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - attempt: 2
        prompt: "Address compile failure: {error}"
      - attempt: 3
        agent: claude-opus
        session: new
      - exit: 5
  - name: quality
    gate: [compile, lint, test]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add( { }\n"}},"3":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{"add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 { t.Fatal() }\n}\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "impl-simple", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) != 3 || agents[0] != "claude-sonnet" || agents[1] != "claude-sonnet" || agents[2] != "claude-opus" {
		t.Fatalf("impl agents: %v", agents)
	}
	launched := parseLedgerLaunchedByStep(t, filepath.Join(dir, ".gump", "runs", runID))
	var implHashes []string
	for _, ev := range launched {
		if s, _ := ev["step"].(string); s != "impl" {
			continue
		}
		h, _ := ev["prompt_hash"].(string)
		implHashes = append(implHashes, h)
	}
	if len(implHashes) != 3 {
		t.Fatalf("want 3 impl prompt hashes, got %d", len(implHashes))
	}
	if implHashes[0] == implHashes[1] {
		t.Fatal("attempt 1 vs 2: prompt hash should differ (original prompt vs override)")
	}
	// Worktree is reused across attempts; CLAUDE.md only reflects the last GET.
	lastWT := nthLaunchedWorktree(launched, "impl", 3)
	if lastWT == "" {
		t.Fatal("no worktree for last impl launch")
	}
	bodyLast := readFile(t, filepath.Join(lastWT, "CLAUDE.md"))
	if strings.Contains(bodyLast, "Previous Attempt Failed") {
		t.Fatal("last impl attempt: must not inject retry section when prompt is overridden")
	}
	if !strings.Contains(bodyLast, "Address compile failure:") {
		t.Fatal("last impl attempt: expected sticky retry override in context")
	}
	st := readRunState(t, dir, runID)
	if st["impl.status"] != "pass" {
		t.Fatalf("impl.status: %q", st["impl.status"])
	}
	if st["impl.attempt"] != "3" {
		t.Fatalf("impl.attempt: %q", st["impl.attempt"])
	}
	if st["impl.agent"] != "claude-opus" {
		t.Fatalf("impl.agent: %q", st["impl.agent"])
	}
	_ = stderr
	_ = stdout
}

func TestE2E_R4_13_NotAndAttemptCombined(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-13.yaml", `name: r4-13
steps:
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile, test]
    retry:
      - not: gate.test
        agent: claude-opus
      - attempt: 3
        worktree: reset
      - exit: 5
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"fail\") }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"fail\") }\n"}},"3":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2)!=3 { t.Fatal() } }\n"}}}}`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-13", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	agents := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents) < 2 || agents[1] != "claude-opus" {
		t.Fatalf("agents: %v", agents)
	}
	_ = stderr
}

func TestE2E_R4_14_ExitOnlyLikeR3(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "fix Add")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-14.yaml", `name: r4-14
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
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-14", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	rts := retryTriggeredEventsForStep(t, dir, runID, "execute")
	if len(rts) == 0 {
		t.Fatal("no retry_triggered for execute")
	}
	for _, ev := range rts {
		ov, ok := ev["overrides"].(map[string]interface{})
		if !ok {
			t.Fatalf("retry_triggered missing overrides object: %v", ev)
		}
		if len(ov) != 0 {
			t.Fatalf("want empty overrides, got %v", ov)
		}
	}
}

func TestE2E_R4_15_ResumeFreshRetryEvaluator(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r4-15.yaml", `name: r4-15
steps:
  - name: prep
    type: code
    prompt: "x"
    agent: claude-sonnet
    gate: [compile]
  - name: impl
    type: code
    prompt: "{spec}"
    agent: claude-sonnet
    gate: [compile]
    retry:
      - attempt: 2
        agent: claude-opus
      - exit: 2
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"2":{"files":{"bad.go":"package main\n\nfunc X() { SYNTAXERROR }\n"}}},"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "wf")
	stdout1, stderr1, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "r4-15", "--agent-stub"}, envWithStubPath(), dir)
	if code1 == 0 {
		t.Fatalf("expected fatal: %s", stderr1)
	}
	runID := extractCookID(stdout1 + stderr1)
	agents1 := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents1) < 2 {
		t.Fatalf("first run: %v", agents1)
	}
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n","bad.go":"package main\n\nfunc X() {}\n"}}`)
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	writeFile(t, wt, "bad.go", "package main\n\nfunc X() {}\n")
	stdout2, stderr2, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, envWithStubPath(), dir)
	if code2 != 0 {
		t.Fatalf("resume exit %d: %s", code2, stderr2)
	}
	agents2 := agentsLaunchedForStep(t, dir, runID, "impl")
	if len(agents2) < 3 {
		t.Fatalf("resume launches: %v", agents2)
	}
	// WHY: resume must not inherit sticky opus from the exhausted run; the step’s declared agent applies again.
	if agents2[2] != "claude-sonnet" {
		t.Fatalf("post-resume first impl agent want sonnet, sequence=%v", agents2)
	}
	_ = stdout2
}
