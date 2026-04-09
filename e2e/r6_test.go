package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_R6_01_SplitEachSequentialTwoTasks (spec R6-01): split + each runs two tasks; state keys are qualified.
func TestE2E_R6_01_SplitEachSequentialTwoTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "feature")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"auth","description":"Add auth"},{"name":"api","description":"Add API"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"impl":{"files":{"auth.go":"package main\n","api.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-01.yaml", `name: r6-01
steps:
  - name: decompose
    type: split
    prompt: "Plan tasks for {spec}"
    agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        prompt: "Implement: {task.description}\nFiles: {task.files}"
        agent: claude-sonnet
        gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-01", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/auth/impl.output"] == "" || st["decompose/api/impl.output"] == "" {
		t.Fatalf("expected qualified outputs, got auth=%q api=%q", st["decompose/auth/impl.output"], st["decompose/api/impl.output"])
	}
	man := readFile(t, filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if !strings.Contains(man, "decompose/auth/impl") || !strings.Contains(man, "decompose/api/impl") {
		t.Fatalf("ledger should list qualified step paths")
	}
}

// TestE2E_R6_02_TaskVarsInPrompt (spec R6-02): resolved prompts carry task.description and task.files (checked via agent stdout artefacts).
func TestE2E_R6_02_TaskVarsInPrompt(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"auth","description":"Add auth"},{"name":"api","description":"Add API"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"impl":{"files":{"auth.go":"package main\n","api.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-02.yaml", `name: r6-02
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        prompt: "Implement: {task.description}\nFiles: {task.files}"
        agent: claude-sonnet
        gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-02", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	cookDir := filepath.Join(dir, ".gump", "runs", runID)
	var sawAuth, sawAPI bool
	_ = filepath.Walk(filepath.Join(cookDir, "artifacts"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(path)
		s := string(b)
		if strings.Contains(s, "Implement: Add auth") && strings.Contains(s, "Files:") {
			sawAuth = true
		}
		if strings.Contains(s, "Implement: Add API") && strings.Contains(s, "Files:") {
			sawAPI = true
		}
		return nil
	})
	if !sawAuth || !sawAPI {
		t.Fatalf("ledger stdout artefacts should contain resolved {task.*} per task (auth=%v api=%v)", sawAuth, sawAPI)
	}
}

// TestE2E_R6_08_ParallelGroup (spec R6-08): parallel: true + steps: runs children; qualified state keys.
func TestE2E_R6_08_ParallelGroup(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "review")
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-08.yaml", `name: r6-08
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
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-08", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["reviews/arch-review.output"] == "" || st["reviews/security-review.output"] == "" {
		t.Fatalf("expected parallel group outputs in state: %+v", map[string]string{
			"arch": st["reviews/arch-review.output"], "sec": st["reviews/security-review.output"],
		})
	}
}

// TestE2E_R6_10_SplitZeroTasks (spec R6-10): empty plan warns; run still passes.
func TestE2E_R6_10_SplitZeroTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[]`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-10.yaml", `name: r6-10
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        prompt: "x"
        agent: claude-sonnet
        gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-10", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "0 tasks") {
		t.Fatalf("expected 0 tasks warning in output: %s", combined)
	}
}

// TestE2E_R6_12_ReplayQualifiedTask (spec R6-12): replay from split/task/step clears only that task's subtree.
func TestE2E_R6_12_ReplayQualifiedTask(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "bad_test.go", "package main\n\nimport \"testing\"\n\nfunc TestAlwaysFail(t *testing.T) { t.Fatal(\"fail\") }\n")
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"auth","description":"A"},{"name":"api","description":"B"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"impl":{"files":{"a.go":"package main\n","b.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-12.yaml", `name: r6-12
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        prompt: "{task.description}"
        agent: claude-sonnet
        gate: [compile]
  - name: quality
    gate: [compile, test]
`)
	gitCommitAll(t, dir, "wf")
	stdout1, stderr1, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-12", "--agent-stub"}, envWithStubPath(), dir)
	if code1 == 0 {
		t.Fatalf("expected fatal at quality, got pass stderr=%s", stderr1)
	}
	origID := extractCookID(stdout1)
	if origID == "" {
		t.Fatal("no run id")
	}
	st1 := readRunState(t, dir, origID)
	authOut := st1["decompose/auth/impl.output"]
	if authOut == "" {
		t.Fatalf("first run should record auth impl: %+v", st1)
	}
	_, _, _ = runPudding(t, []string{"run", "spec.md", "--workflow", "r6-12", "--replay", "--from-step", "decompose/api/impl", "--agent-stub"}, envWithStubPath(), dir)
	cooksDir := filepath.Join(dir, ".gump", "runs")
	entries, _ := os.ReadDir(cooksDir)
	var replayID string
	var replayMtime int64
	for _, e := range entries {
		if !e.IsDir() || e.Name() == origID {
			continue
		}
		info, err := os.Stat(filepath.Join(cooksDir, e.Name(), "manifest.ndjson"))
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > replayMtime {
			replayMtime = info.ModTime().UnixNano()
			replayID = e.Name()
		}
	}
	if replayID == "" {
		t.Fatal("replay should create a new run dir")
	}
	st2 := readRunState(t, dir, replayID)
	if st2["decompose/auth/impl.output"] != authOut {
		t.Fatalf("auth task state should be preserved from fatal run; was %q now %q", authOut, st2["decompose/auth/impl.output"])
	}
}

// TestE2E_R6_07_ParallelMergeConflictMessage (spec R6-07): two parallel tasks touch the same file → fatal mentions parallel merge conflict.
func TestE2E_R6_07_ParallelMergeConflictMessage(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"t1","description":"one","files":["shared.go"]},{"name":"t2","description":"two","files":["shared.go"]}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"impl":{"files":{"shared.go":"package main\n\nvar X = 1\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-07.yaml", `name: r6-07
steps:
  - name: decompose
    type: split
    parallel: true
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: impl
        type: code
        prompt: "edit"
        agent: claude-sonnet
        gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-07", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatal("expected merge conflict fatal")
	}
	if !strings.Contains(stderr, "parallel merge conflict") {
		t.Fatalf("stderr should mention parallel merge conflict: %s", stderr)
	}
}
