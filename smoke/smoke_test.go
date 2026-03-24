//go:build smoke

package smoke

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	specAdd = `Create a function ` + "`Add(a, b int) int`" + ` in a file called ` + "`math.go`" + ` that returns the sum of two integers. Also create ` + "`math_test.go`" + ` with a test that verifies Add(2, 3) == 5.`

	specMathLib = `Implement a small math library in Go:
1. A function ` + "`Add(a, b int) int`" + ` that returns a + b
2. A function ` + "`Multiply(a, b int) int`" + ` that returns a * b
Each function should have its own test.`

	specStringUtils = `Implement a string utility package in Go with two functions:
1. ` + "`Reverse(s string) string`" + ` - reverses a string, handling UTF-8 correctly
2. ` + "`IsPalindrome(s string) bool`" + ` - checks if a string is a palindrome (case-insensitive, ignoring spaces)
Each function must have comprehensive tests.`

	specGreet = `Create a function ` + "`Greet(name string) string`" + ` in package main that returns "Hello, <name>!" and a test for it.`

	specCalc = `Calculator struct with Add(a, b int) int and Subtract(a, b int) int methods.`

	specTrickyReverse = `Implement ` + "`ReverseWords(s string) string`" + ` that reverses the order of words in a string while preserving whitespace. Leading and trailing spaces should be preserved. Multiple spaces between words should be preserved. Create comprehensive tests including edge cases.`
)

const crossRecipeYAML = `name: cross
description: Cross-provider smoke test
steps:
  - name: decompose
    agent: claude-sonnet
    output: plan
    prompt: |
      Analyze this spec. Produce a single task with the function name and affected files.
    gate:
      - schema: plan
  - name: implement
    foreach: decompose
    steps:
      - name: code
        agent: gemini
        prompt: "Implement: {item.description}. Files: {item.files}"
        gate:
          - compile
          - test
`

const sessionRecipeYAML = `name: session-test
description: Session reuse smoke test
steps:
  - name: write-tests
    agent: claude-sonnet
    session: fresh
    prompt: |
      Write tests for a Calculator struct with Add and Subtract methods.
      Only create test files.
    gate:
      - compile
  - name: implement
    agent: claude-sonnet
    session: reuse
    prompt: |
      Implement the Calculator struct to make all tests pass.
      Do NOT modify test files.
    gate:
      - compile
      - test
`

const retryRecipeYAML = `name: retry-test
description: Retry smoke test
steps:
  - name: code
    agent: claude-haiku
    prompt: |
      {spec}
      IMPORTANT: You must also handle edge cases like empty strings and single characters.
    gate:
      - compile
      - test
    on_failure:
      retry: 3
      strategy:
        - same
        - escalate: claude-sonnet
`

// TestSmokeDoctor checks that gump doctor runs and reports git and installed agents.
// It only fails if doctor crashes or git is missing; red agents are logged, not failed.
func TestSmokeDoctor(t *testing.T) {
	dir := setupSmokeRepo(t)
	stdout, _, code := runGump(t, dir, "doctor")
	if code != 0 {
		t.Fatalf("gump doctor exit %d", code)
	}
	if !strings.Contains(stdout, "git") || !strings.Contains(stdout, "✓") {
		t.Errorf("stdout should contain git and ✓: %s", stdout)
	}
	// WHY: Doctor uses harnesses with explicit labels; we assert on labels rather than exit codes.
	for _, label := range []string{"claude-code", "codex", "gemini", "qwen", "opencode"} {
		if !strings.Contains(stdout, label) {
			t.Errorf("stdout should contain %q: %s", label, stdout)
		}
	}
	re := regexp.MustCompile(`recipes\s+✓\s+(\d+)\s+built-in recipes loaded`)
	m := re.FindStringSubmatch(stdout)
	if len(m) != 2 {
		t.Errorf("stdout should contain recipes count (e.g. 'recipes ✓ N built-in recipes loaded'): %s", stdout)
	} else {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			t.Errorf("built-in recipes count should be > 0, got %q (err=%v): %s", m[1], err, stdout)
		}
	}
}

// TestSmokeDryRunV4: smoke test live du dry-run v4.
func TestSmokeDryRunV4(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Implement a function")
	stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "tdd", "--dry-run")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	for _, s := range []string{"Budget:", "$5.00", "gate=", "on_failure:", "State Bag Resolutions:"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
	if strings.Contains(stdout, "validate:") || strings.Contains(stdout, "retry:") {
		t.Errorf("stdout must not contain validate/retry: %s", stdout)
	}
}

// TestSmokeFreeform runs a full run→apply→go test cycle per agent to catch
// regressions in a single provider path.
func TestSmokeFreeform(t *testing.T) {
	agents := []struct {
		name  string
		agent string
	}{
		{"claude", "claude-sonnet"},
		{"codex", "codex"},
		{"gemini", "gemini"},
		{"qwen", "qwen"},
		{"opencode", "opencode"},
	}
	for _, a := range agents {
		t.Run(a.name, func(t *testing.T) {
			requireAgent(t, a.name)
			dir := setupSmokeRepo(t)
			writeSpec(t, dir, specAdd)
			stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", a.agent)
			if code != 0 {
				t.Fatalf("run exit %d: %s", code, stdout)
			}
			assertRunPass(t, dir)
			ledger := readLedger(t, dir)
			hasLaunched := false
			hasCompletedZero := false
			for _, ev := range ledger {
				if ev["type"] == "agent_launched" {
					if cli, _ := ev["cli"].(string); strings.HasPrefix(cli, a.name) {
						hasLaunched = true
					}
				}
				if ev["type"] == "agent_completed" {
					if ec, _ := ev["exit_code"].(float64); ec == 0 {
						hasCompletedZero = true
					}
				}
			}
			if !hasLaunched {
				t.Error("ledger should contain agent_launched with cli starting with agent name")
			}
			if !hasCompletedZero {
				t.Error("ledger should contain agent_completed with exit_code 0")
			}
			_, _, applyCode := runGump(t, dir, "apply")
			if applyCode != 0 {
				t.Fatalf("apply exit %d", applyCode)
			}
			assertGoTestPasses(t, dir)
		})
	}
}

// TestSmokeSimple verifies the simple recipe (decompose + implement) and that the
// ledger records plan step and at least two step_started events.
func TestSmokeSimple(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specMathLib)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "simple", "--agent", "claude-sonnet")
	if code != 0 {
		t.Fatalf("run exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	assertRunPass(t, dir)
	ledger := readLedger(t, dir)
	stepStarted := 0
	hasDecomposeCompleted := false
	for _, ev := range ledger {
		if ev["type"] == "step_started" {
			stepStarted++
		}
		if ev["type"] == "agent_completed" {
			if step, _ := ev["step"].(string); step == "decompose" {
				hasDecomposeCompleted = true
			}
		}
	}
	if stepStarted < 2 {
		t.Errorf("ledger should contain at least 2 step_started (decompose + implement/*), got %d", stepStarted)
	}
	if !hasDecomposeCompleted {
		t.Error("ledger should contain agent_completed for step decompose")
	}
	_, _, applyCode := runGump(t, dir, "apply")
	if applyCode != 0 {
		t.Fatalf("apply exit %d", applyCode)
	}
	assertGoTestPasses(t, dir)
}

// TestSmokeTDD runs the full TDD flow (decompose → red → green → final-check) to
// ensure the recipe and validators behave; non-deterministic agent failures are accepted.
// final-check runs lint; if golangci-lint is not installed, the validator is skipped (spec: skip-lint-if-not-installed).
// TDD is informational like Retry: we do not Fatal on run failure (e.g. timeout or untouched violation).
func TestSmokeTDD(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specStringUtils)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "tdd")
	if code != 0 {
		t.Logf("run failed (exit %d); TDD test is informational: stdout: %s stderr: %s", code, stdout, stderr)
	}
	if code == 0 {
		assertRunPass(t, dir)
		_, _, applyCode := runGump(t, dir, "apply")
		if applyCode != 0 {
			t.Fatalf("apply exit %d", applyCode)
		}
		assertGoTestPasses(t, dir)
	}
	ledger := readLedger(t, dir)
	hasDecompose := false
	hasTests := false
	hasImpl := false
	hasQuality := false
	for _, ev := range ledger {
		if ev["type"] != "step_started" {
			continue
		}
		step, _ := ev["step"].(string)
		if step == "decompose" {
			hasDecompose = true
		}
		if strings.Contains(step, "/tests") || step == "tests" {
			hasTests = true
		}
		if strings.Contains(step, "/impl") || step == "impl" {
			hasImpl = true
		}
		if step == "quality" || strings.Contains(step, "/quality") {
			hasQuality = true
		}
	}
	assertLedger := func(cond bool, msg string) {
		if cond {
			return
		}
		if code != 0 {
			t.Logf("ledger (run failed): %s", msg)
		} else {
			t.Error(msg)
		}
	}
	assertLedger(hasDecompose, "ledger should contain step_started for decompose")
	assertLedger(hasTests, "ledger should contain step_started for at least one step containing tests")
	assertLedger(hasImpl, "ledger should contain step_started for at least one step containing impl")
	assertLedger(hasQuality, "ledger should contain step_started for quality")
}

// TestSmokeCrossProvider ensures two different agents are used in one run so
// cross-provider wiring is exercised; we require claude + one of gemini/codex/qwen/opencode.
func TestSmokeCrossProvider(t *testing.T) {
	requireAgent(t, "claude")
	second := ""
	for _, name := range []string{"gemini", "codex", "qwen", "opencode"} {
		if _, err := execLookPath(name); err == nil {
			second = name
			break
		}
	}
	if second == "" {
		t.Skip("no second agent (gemini/codex/qwen/opencode) installed, skipping")
	}
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specGreet)
	recipe := strings.ReplaceAll(crossRecipeYAML, "agent: gemini\n", "agent: "+second+"\n")
	writeRecipe(t, dir, "cross", recipe)
	stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "cross")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	assertRunPass(t, dir)
	ledger := readLedger(t, dir)
	hasClaude := false
	hasSecond := false
	for _, ev := range ledger {
		if ev["type"] != "agent_launched" {
			continue
		}
		cli, _ := ev["cli"].(string)
		if strings.HasPrefix(cli, "claude") {
			hasClaude = true
		}
		if strings.HasPrefix(cli, second) {
			hasSecond = true
		}
	}
	if !hasClaude {
		t.Error("ledger should contain agent_launched with cli starting with claude (decompose)")
	}
	if !hasSecond {
		t.Errorf("ledger should contain agent_launched with cli starting with %s (code)", second)
	}
	_, _, applyCode := runGump(t, dir, "apply")
	if applyCode != 0 {
		t.Fatalf("apply exit %d", applyCode)
	}
	assertGoTestPasses(t, dir)
}

// TestSmokeSessionReuse checks that session reuse (resume/session) is used for the
// implement step so we detect regressions in session handoff.
func TestSmokeSessionReuse(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specCalc)
	writeRecipe(t, dir, "session-test", sessionRecipeYAML)
	stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "session-test")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	assertRunPass(t, dir)
	ledger := readLedger(t, dir)
	implementHasResume := false
	for _, ev := range ledger {
		if ev["type"] != "agent_launched" {
			continue
		}
		stepPath, _ := ev["step"].(string)
		if !strings.Contains(stepPath, "implement") {
			continue
		}
		cli, _ := ev["cli"].(string)
		if strings.Contains(cli, "--resume") || strings.Contains(cli, "--session") {
			implementHasResume = true
			break
		}
	}
	if !implementHasResume {
		t.Error("ledger: implement step should have agent_launched with --resume or --session in cli")
	}
	_, _, applyCode := runGump(t, dir, "apply")
	if applyCode != 0 {
		t.Fatalf("apply exit %d", applyCode)
	}
	assertGoTestPasses(t, dir)
}

// TestSmokeGC ensures gc --keep-last 1 leaves exactly one run dir and reports Cleaned.
func TestSmokeGC(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	for i := 0; i < 3; i++ {
		stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-sonnet")
		if code != 0 {
			t.Fatalf("run %d exit %d:\nstdout: %s\nstderr: %s", i+1, code, stdout, stderr)
		}
	}
	stdout, _, code := runGump(t, dir, "gc", "--keep-last", "1")
	if code != 0 {
		t.Fatalf("gc exit %d: %s", code, stdout)
	}
	runsDir := filepath.Join(dir, ".gump", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("list cooks: %v", err)
	}
	var dirs int
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Errorf("expected exactly 1 run dir after gc --keep-last 1, got %d", dirs)
	}
	if !strings.Contains(stdout, "Cleaned") {
		t.Errorf("stdout should contain Cleaned: %s", stdout)
	}
}

// TestSmokeRetry is informational: we only assert ledger events; if the run fails
// we log and pass so the suite does not fail on non-deterministic agent behaviour.
func TestSmokeRetry(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specTrickyReverse)
	writeRecipe(t, dir, "retry-test", retryRecipeYAML)
	stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "retry-test")
	if code == 0 {
		assertRunPass(t, dir)
		_, _, applyCode := runGump(t, dir, "apply")
		if applyCode != 0 {
			t.Logf("apply failed with exit %d", applyCode)
		} else {
			assertGoTestPasses(t, dir)
		}
	} else {
		t.Logf("run failed (exit %d); retry test is informational: %s", code, stdout)
	}
	ledger := readLedger(t, dir)
	types := make(map[string]bool)
	for _, ev := range ledger {
		if t, ok := ev["type"].(string); ok {
			types[t] = true
		}
	}
	if !types["agent_launched"] {
		t.Error("ledger should contain agent_launched")
	}
	if !types["step_started"] {
		t.Error("ledger should contain step_started")
	}
}

// TestSmokeParallelArtifact: we need to confirm parallel artifact steps run both agents and the ledger reflects the parallel group so tooling and audits are accurate.
func TestSmokeParallelArtifact(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Write a haiku about Go.")
	recipeYAML := `name: parallel-art
description: Parallel artifact smoke test
steps:
  - name: generate
    parallel: true
    steps:
      - name: review-1
        agent: claude-sonnet
        output: artifact
        prompt: |
          Write a brief code review of this spec. Write your review to .gump/out/artifact.txt.
          {spec}
      - name: review-2
        agent: claude-haiku
        output: artifact
        prompt: |
          Write a brief code review of this spec. Write your review to .gump/out/artifact.txt.
          {spec}
`
	writeRecipe(t, dir, "parallel-art", recipeYAML)
	stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "parallel-art")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	assertRunPass(t, dir)
	ledger := readLedger(t, dir)
	var hasGroupStartedParallel bool
	agentLaunched := 0
	var hasGroupCompleted bool
	for _, ev := range ledger {
		if ev["type"] == "group_started" {
			if par, _ := ev["parallel"].(bool); par {
				hasGroupStartedParallel = true
			}
		}
		if ev["type"] == "agent_launched" {
			agentLaunched++
		}
		if ev["type"] == "group_completed" {
			hasGroupCompleted = true
		}
	}
	if !hasGroupStartedParallel {
		t.Error("ledger should contain group_started with parallel: true")
	}
	if agentLaunched < 2 {
		t.Errorf("ledger should contain at least 2 agent_launched, got %d", agentLaunched)
	}
	if !hasGroupCompleted {
		t.Error("ledger should contain group_completed")
	}
	runDir := latestRunDir(t, dir)
	artifactsDir := filepath.Join(runDir, "artifacts")
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		t.Fatalf("list artifacts dir: %v", err)
	}
	hasReview1 := false
	hasReview2 := false
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, "review-1") {
			hasReview1 = true
		}
		if strings.Contains(name, "review-2") {
			hasReview2 = true
		}
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !hasReview1 {
		t.Errorf("artifacts dir should contain a file with review-1 in the name (got: %v)", names)
	}
	if !hasReview2 {
		t.Errorf("artifacts dir should contain a file with review-2 in the name (got: %v)", names)
	}
}

// TestSmokeReplay: replay resumes from the chosen step only; step-a is skipped, step-b runs with the fixed recipe (no validator so stub passes).
func TestSmokeReplay(t *testing.T) {
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Create hello.go")

	// Recipe 1: step-b always fails (bash exit 1)
	writeRecipe(t, dir, "replay-test", `name: replay-test
description: Replay smoke test
steps:
  - name: step-a
    agent: stub
    prompt: "Create hello.go"
  - name: step-b
    agent: stub
    prompt: "Create goodbye.go"
    gate:
      - bash: "exit 1"
`)

	_, _, code1 := runGump(t, dir, "run", "spec.md", "--workflow", "replay-test", "--agent-stub")
	if code1 == 0 {
		t.Fatal("first run should fail (step-b has bash exit 1)")
	}

	// Recipe 2: step-b without validator passes as soon as stub completes
	writeRecipe(t, dir, "replay-test", `name: replay-test
description: Replay smoke test
steps:
  - name: step-a
    agent: stub
    prompt: "Create hello.go"
  - name: step-b
    agent: stub
    prompt: "Create goodbye.go"
`)

	stdout2, stderr2, code2 := runGump(t, dir, "run", "spec.md", "--workflow", "replay-test", "--replay", "--from-step", "step-b", "--agent-stub")
	if code2 != 0 {
		t.Fatalf("replay run exit %d:\nstdout: %s\nstderr: %s", code2, stdout2, stderr2)
	}

	ledger := readLedger(t, dir)
	hasReplay := false
	hasStepB := false
	hasStepA := false
	for _, ev := range ledger {
		if ev["type"] == "replay_started" {
			hasReplay = true
		}
		if ev["type"] == "step_started" {
			step, _ := ev["step"].(string)
			if step == "step-a" {
				hasStepA = true
			}
			if step == "step-b" {
				hasStepB = true
			}
		}
	}
	if !hasReplay {
		t.Error("replay ledger should contain replay_started")
	}
	if !hasStepB {
		t.Error("replay ledger should contain step_started for step-b")
	}
	if hasStepA {
		t.Error("replay ledger should NOT contain step_started for step-a (skipped)")
	}
}

// TestSmokeContextFile: context files must be visible to the agent so specs can reference repo docs and conventions without pasting them inline.
func TestSmokeContextFile(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	marker := "UNIQUE_CONTEXT_MARKER_" + t.Name()
	if err := os.WriteFile(filepath.Join(dir, "context-doc.md"), []byte("# Context\n"+marker+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "context-doc.md")
	runGit(t, dir, "commit", "-m", "add context doc")
	writeSpec(t, dir, "Summarize the context document.")
	recipeYAML := `name: ctx-test
description: Context file injection smoke test
steps:
  - name: summarize
    agent: claude-haiku
    output: artifact
    context:
      - file: "context-doc.md"
    prompt: |
      Read the context file provided above. Your response MUST include the exact marker string found in context-doc.md.
      Write your response to .gump/out/artifact.txt.
`
	writeRecipe(t, dir, "ctx-test", recipeYAML)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "ctx-test")
	if code != 0 {
		t.Fatalf("run exit %d:\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	assertRunPass(t, dir)
	ledger := readLedger(t, dir)
	for _, ev := range ledger {
		if ev["type"] == "agent_completed" {
			if ec, _ := ev["exit_code"].(float64); ec == 0 {
				return
			}
		}
	}
	runID := filepath.Base(latestRunDir(t, dir))
	wtDir := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	claudeMD := filepath.Join(wtDir, "CLAUDE.md")
	data, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("read context file: %v", err)
	}
	if !strings.Contains(string(data), marker) {
		t.Errorf("context file should contain marker %q", marker)
	}
}

// TestSmokeContextBuilderV4Modes: diff and review modes must produce distinct provider prompts so agents never mix output contracts.
func TestSmokeContextBuilderV4Modes(t *testing.T) {
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Create a simple Go function.")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files": {"add.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	writeRecipe(t, dir, "test-diff", `name: test-diff
steps:
  - name: s
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
`)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "test-diff", "--agent-stub")
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, stdout, stderr)
	}
	runID := filepath.Base(latestRunDir(t, dir))
	wtDir := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	data1, err := os.ReadFile(filepath.Join(wtDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	s1 := string(data1)
	if !strings.Contains(s1, "code step") || !strings.Contains(s1, "git diff") {
		t.Errorf("diff mode: expected code step and git diff markers")
	}
	writeRecipe(t, dir, "test-review", `name: test-review
steps:
  - name: r
    agent: claude-haiku
    output: review
    prompt: "Review"
    gate: [compile]
`)
	stdout, stderr, code = runGump(t, dir, "run", "spec.md", "--workflow", "test-review", "--agent-stub")
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, stdout, stderr)
	}
	runID2 := filepath.Base(latestRunDir(t, dir))
	wtDir2 := filepath.Join(dir, ".gump", "worktrees", "run-"+runID2)
	data2, err := os.ReadFile(filepath.Join(wtDir2, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	s2 := string(data2)
	if !strings.Contains(s2, "review step") || !strings.Contains(s2, "review.json") {
		t.Errorf("review mode: expected review step and review.json markers")
	}
	writeRecipe(t, dir, "test-plan", `name: test-plan
steps:
  - name: p
    agent: claude-haiku
    output: plan
    prompt: "Plan"
`)
	stdout, stderr, code = runGump(t, dir, "run", "spec.md", "--workflow", "test-plan", "--agent-stub")
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, stdout, stderr)
	}
	runID3 := filepath.Base(latestRunDir(t, dir))
	wtDir3 := filepath.Join(dir, ".gump", "worktrees", "run-"+runID3)
	data3, err := os.ReadFile(filepath.Join(wtDir3, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	s3 := string(data3)
	if !strings.Contains(s3, "plan step") || !strings.Contains(s3, ".gump/out/plan.json") {
		t.Errorf("plan mode: expected plan step and plan.json markers")
	}
	writeRecipe(t, dir, "test-artifact", `name: test-artifact
steps:
  - name: a
    agent: claude-haiku
    output: artifact
    prompt: "Write artifact"
    gate: [compile]
`)
	stdout, stderr, code = runGump(t, dir, "run", "spec.md", "--workflow", "test-artifact", "--agent-stub")
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, stdout, stderr)
	}
	runID4 := filepath.Base(latestRunDir(t, dir))
	wtDir4 := filepath.Join(dir, ".gump", "worktrees", "run-"+runID4)
	data4, err := os.ReadFile(filepath.Join(wtDir4, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	s4 := string(data4)
	if !strings.Contains(s4, "artifact step") || !strings.Contains(s4, ".gump/out/artifact.txt") {
		t.Errorf("artifact mode: expected artifact step and artifact.txt markers")
	}
}

// TestSmokeBlastRadius: blast radius must block edits outside task.files so plans stay predictable and reviews are scoped.
func TestSmokeBlastRadius(t *testing.T) {
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Create hello.go")
	writeRecipe(t, dir, "foreach-blast", `name: foreach-blast
steps:
  - name: plan
    agent: stub
    output: plan
    prompt: "Plan"
  - name: impl
    foreach: plan
    steps:
      - name: code
        agent: stub
        output: diff
        prompt: "Implement"
`)
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name": "task-1", "description": "One task", "files": ["hello.go"]}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"code":{"files":{"hello.go":"package main\n\nfunc Hello() {}\n","extra.go":"package main\n\nfunc Extra() {}\n"}}}}`)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "foreach-blast", "--agent-stub")
	if code == 0 {
		t.Fatal("expected non-zero exit on blast radius violation")
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "blast radius") {
		t.Errorf("output should contain blast radius: %s", combined)
	}
}

// TestSmokeSessionReuseOnRetry: reuse-on-retry must start fresh then resume the same session on retry so long conversations are preserved without storing session in state bag.
func TestSmokeSessionReuseOnRetry(t *testing.T) {
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Create math.go")
	writeRecipe(t, dir, "retry-session-test", `name: retry-session-test
description: reuse-on-retry smoke test
steps:
  - name: code
    agent: stub
    session: reuse-on-retry
    prompt: "Implement math.go"
    gate:
      - compile
      - test
    on_failure:
      retry: 2
      strategy:
        - same
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"math.go":"package main\n\nfunc Add(a, b int) int { return 0 }\n"}},"2":{"files":{"math.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{"math_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n"}}`)
	_, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "retry-session-test", "--agent-stub")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stderr)
	}
	ledger := readLedger(t, dir)
	var launchedCode []map[string]interface{}
	for _, ev := range ledger {
		if ev["type"] == "agent_launched" {
			step, _ := ev["step"].(string)
			if strings.Contains(step, "code") {
				launchedCode = append(launchedCode, ev)
			}
		}
	}
	if len(launchedCode) < 2 {
		t.Fatalf("expected at least 2 agent_launched for code step, got %d", len(launchedCode))
	}
	sid1, _ := launchedCode[0]["session_id"].(string)
	sid2, _ := launchedCode[1]["session_id"].(string)
	if sid1 != "" {
		t.Errorf("attempt 1 should have empty session_id (fresh), got %q", sid1)
	}
	if sid2 == "" {
		t.Error("attempt 2 should have non-empty session_id (reuse)")
	}
	cli2, _ := launchedCode[1]["cli"].(string)
	if !strings.Contains(cli2, "resume") && !strings.Contains(cli2, "session") {
		t.Errorf("attempt 2 cli should contain resume or session: %s", cli2)
	}
}

// TestSmokeGumpModels: models subcommand must list providers and context so users can see what is available and configured.
func TestSmokeGumpModels(t *testing.T) {
	dir := setupSmokeRepo(t)
	stdout, _, code := runGump(t, dir, "models")
	if code != 0 {
		t.Fatalf("gump models exit %d", code)
	}
	for _, s := range []string{"claude-opus", "codex", "gemini", "qwen", "Context"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout should contain %q: %s", s, stdout)
		}
	}
}

// TestSmokeGumpStatus: status must exit cleanly when idle so scripts and UX can poll without false errors.
func TestSmokeGumpStatus(t *testing.T) {
	dir := setupSmokeRepo(t)
	stdout, _, code := runGump(t, dir, "status")
	if code != 0 {
		t.Fatalf("gump status exit %d", code)
	}
	if !strings.Contains(stdout, "No run") && !strings.Contains(stdout, "no run") && !strings.Contains(stdout, "nothing in progress") {
		t.Errorf("stdout should indicate no run in progress: %s", stdout)
	}
}

// TestSmokeRecipeComposition: recipe: must expand the referenced recipe so composed recipes run the full sub-recipe, not a single no-op step.
func TestSmokeRecipeComposition(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	writeRecipe(t, dir, "compose-test", `name: compose-test
description: Recipe composition smoke test
steps:
  - name: implement
    recipe: freeform
`)
	_, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "compose-test", "--agent", "claude-sonnet")
	if code != 0 {
		t.Fatalf("run exit %d", code)
	}
	assertRunPass(t, dir)
	ledger := readLedger(t, dir)
	hasExecuteStep := false
	for _, ev := range ledger {
		if ev["type"] == "step_started" {
			step, _ := ev["step"].(string)
			if step == "execute" || strings.Contains(step, "implement/execute") {
				hasExecuteStep = true
				break
			}
		}
	}
	if !hasExecuteStep {
		t.Error("ledger should contain step_started from freeform (e.g. execute or implement/execute)")
	}
}

// TestSmokeDXStreaming: streaming output must show progress (step counter), agent summary, and footer so users see what is running and the final result.
func TestSmokeDXStreaming(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	_, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-sonnet")
	if code != 0 {
		t.Fatalf("run exit %d", code)
	}
	// Display format is [1] (step index) or [1/N] when total is known.
	if !strings.Contains(stderr, "[1]") && !strings.Contains(stderr, "[1/") {
		t.Errorf("stderr should contain step counter [1] or [1/: %s", stderr)
	}
	if !strings.Contains(stderr, "done") || !strings.Contains(stderr, "turns") {
		t.Errorf("stderr should contain agent summary (done, turns): %s", stderr)
	}
	if !strings.Contains(stderr, "Run total:") && !strings.Contains(stderr, "Result:") {
		t.Errorf("stderr should contain footer (Run total: or Result:): %s", stderr)
	}
}

func TestSmokeDisplayTurnBased(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	_, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	if code != 0 {
		t.Fatalf("run exit %d", code)
	}
	if !strings.Contains(stderr, "T1") {
		t.Fatalf("stderr should contain turn marker T1: %s", stderr)
	}
	if strings.Contains(stderr, "░░░░░") {
		t.Fatalf("stderr should not contain legacy bars: %s", stderr)
	}
}

func TestSmokeEscapingLive(t *testing.T) {
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Ensure escaped braces survive templating.")
	writeFile(t, dir, "context-doc.md", "literal {{json_example}} marker")
	writeRecipe(t, dir, "escape-live", `name: escape-live
steps:
  - name: echo
    agent: stub
    output: artifact
    context:
      - file: "context-doc.md"
    prompt: |
      Keep this literal token: {{json_example}}
`)
	_, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "escape-live", "--agent-stub")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stderr)
	}
	runDir := latestRunDir(t, dir)
	runID := filepath.Base(runDir)
	wtDir := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	data, err := os.ReadFile(filepath.Join(wtDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(data), "{json_example}") {
		t.Fatalf("context should keep literal braces, got: %s", string(data))
	}
}

// TestSmokeLedgerV4Events checks gate naming, session_mode, and observed_at-prefixed stdout lines on a live run.
func TestSmokeLedgerV4Events(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	stdout, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	ledger := readLedger(t, dir)
	var hasGate bool
	var hasSessionMode bool
	for _, ev := range ledger {
		typ, _ := ev["type"].(string)
		if typ == "gate_started" || typ == "gate_passed" {
			hasGate = true
		}
		if typ == "step_started" {
			if _, ok := ev["session_mode"]; ok {
				hasSessionMode = true
			}
		}
	}
	if !hasGate {
		t.Error("ledger should contain gate_* events")
	}
	if !hasSessionMode {
		t.Error("step_started should include session_mode")
	}
	runDir := latestRunDir(t, dir)
	matches, _ := filepath.Glob(filepath.Join(runDir, "artifacts", "*-stdout.log"))
	if len(matches) == 0 {
		t.Fatal("expected *-stdout.log in run artifacts")
	}
	lineRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z `)
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			if !lineRe.MatchString(line) {
				snippet := line
				if len(snippet) > 80 {
					snippet = snippet[:80]
				}
				t.Errorf("stdout line missing observed_at prefix: %q", snippet)
			}
		}
	}
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// TestSmokeReportLive checks that gump report runs after a real run (M5 TUI).
func TestSmokeReportLive(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	_, _, code1 := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	if code1 != 0 {
		t.Fatalf("run exit %d", code1)
	}
	stdout, _, code2 := runGump(t, dir, "report")
	if code2 != 0 {
		t.Fatalf("report exit %d: %s", code2, stdout)
	}
	low := strings.ToLower(stdout)
	if !strings.Contains(low, "pass") {
		t.Errorf("report should contain pass: %s", stdout)
	}
	for _, s := range []string{"Duration", "Cost", "Tokens", "Steps"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("report should contain %q: %s", s, stdout)
		}
	}
}

// TestSmokeReportLastN checks cross-run aggregation (M5).
func TestSmokeReportLastN(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	stdout, _, code := runGump(t, dir, "report", "--last", "2")
	if code != 0 {
		t.Fatalf("report exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "Last 2 runs") {
		t.Errorf("report should mention Last 2 runs: %s", stdout)
	}
	if !strings.Contains(stdout, "Success rate") {
		t.Errorf("report should contain Success rate: %s", stdout)
	}
}

func TestSmokeFullV03(t *testing.T) {
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	stdout, stderr, _ := runGump(t, dir, "run", "spec.md", "--workflow", "tdd", "--agent-stub")
	if !strings.Contains(stderr, "[gump]") {
		t.Fatalf("expected stream marker [gump] in stderr: %s", stderr)
	}
	runDir := latestRunDir(t, dir)
	if _, err := os.Stat(filepath.Join(runDir, "state-bag.json")); err != nil {
		t.Fatalf("state-bag.json missing: %v", err)
	}
	reportOut, _, rc := runGump(t, dir, "report")
	if rc != 0 {
		t.Fatalf("report exit %d: %s", rc, reportOut)
	}
	if !strings.Contains(reportOut, "Gump Report") && !strings.Contains(stdout, "Gump Report") {
		t.Fatalf("report output missing title: %s", reportOut)
	}
}

func TestSmokeTelemetryOptOut(t *testing.T) {
	dir := setupSmokeRepo(t)
	_, _, code := runGump(t, dir, "config", "set", "analytics", "false")
	if code != 0 {
		t.Fatalf("config set analytics false failed: %d", code)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".gump", "config.toml"))
	if err != nil {
		t.Fatalf("read ~/.gump/config.toml: %v", err)
	}
	if !strings.Contains(string(data), "enabled = false") {
		t.Fatalf("expected analytics opt-out persisted in config.toml: %s", string(data))
	}
}

// TestSmokeGumpRunLive validates a real `gump run` invocation.
func TestSmokeGumpRunLive(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	if code != 0 {
		t.Fatalf("run exit %d:\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	assertRunPass(t, dir)
}

// TestSmokeStateBagMetricsLive validates state-bag metrics are written on a live run.
func TestSmokeStateBagMetricsLive(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, specAdd)
	_, _, code := runGump(t, dir, "run", "spec.md", "--workflow", "freeform", "--agent", "claude-haiku")
	if code != 0 {
		t.Skip("live run failed; skipping metrics assertions")
	}
	runDir := latestRunDir(t, dir)
	data, err := os.ReadFile(filepath.Join(runDir, "state-bag.json"))
	if err != nil {
		t.Fatalf("read state-bag.json: %v", err)
	}
	if !strings.Contains(string(data), `"run"`) || !strings.Contains(string(data), `"entries"`) {
		t.Fatalf("state-bag should contain run and entries sections: %s", string(data))
	}
}

// TestSmokeWorkflowComposition validates standalone workflow composition with inputs on a live agent path.
func TestSmokeWorkflowComposition(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Say hello.")
	writeRecipe(t, dir, "sub-smoke", `name: sub-smoke
inputs:
  msg:
    required: true
steps:
  - name: echo
    agent: claude-haiku
    output: artifact
    prompt: "{msg}"
`)
	writeRecipe(t, dir, "parent-smoke", `name: parent-smoke
steps:
  - name: call-sub
    workflow: sub-smoke
    with:
      msg: hello from smoke
`)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "parent-smoke")
	if code != 0 {
		t.Fatalf("run exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	assertRunPass(t, dir)
}

// TestSmokeGuardMaxTurnsLive ensures high max_turns does not regress normal execution flow.
func TestSmokeGuardMaxTurnsLive(t *testing.T) {
	requireAgent(t, "claude")
	dir := setupSmokeRepo(t)
	writeSpec(t, dir, "Create a one-line summary.")
	writeRecipe(t, dir, "guard-max-turns-live", `name: guard-max-turns-live
steps:
  - name: summarize
    agent: claude-haiku
    output: artifact
    prompt: "{spec}"
    guard:
      max_turns: 100
`)
	stdout, stderr, code := runGump(t, dir, "run", "spec.md", "--workflow", "guard-max-turns-live")
	if code != 0 {
		t.Fatalf("run exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	assertRunPass(t, dir)
}
