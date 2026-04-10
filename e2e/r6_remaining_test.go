package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isomorphx/gump/internal/state"
)

func countManifestStepStarted(t *testing.T, cookDir, stepPath string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cookDir, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
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
		if s, _ := ev["step"].(string); s == stepPath {
			n++
		}
	}
	return n
}

// TestE2E_R6_03_EachScopeSessionAndAgent (spec R6-03): session: from: + {converge.agent} stay on the task; top-level same short name must not hijack.
func TestE2E_R6_03_EachScopeSessionAndAgent(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"auth","description":"A"},{"name":"api","description":"B"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"unique_session_each":true,"by_step":{"converge":{"files":{"x.go":"package main\n"}},"smoke":{"files":{"y.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-03.yaml", `name: r6-03
steps:
  - name: topconv
    type: code
    prompt: "warmup"
    agent: claude-haiku
    gate: [compile]
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: converge
        type: code
        prompt: "c {task.name}"
        agent: claude-opus
        gate: [compile]
      - name: smoke
        type: code
        prompt: "s {task.name} agent={converge.agent}"
        agent: "{converge.agent}"
        session:
          from: converge
        gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-03", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/auth/smoke.agent"] != "claude-opus" || st["decompose/api/smoke.agent"] != "claude-opus" {
		t.Fatalf("smoke should inherit each-task converge agent, got auth=%q api=%q", st["decompose/auth/smoke.agent"], st["decompose/api/smoke.agent"])
	}
}

// TestE2E_R6_04_RetryInsideEach (spec R6-04): converge retries; attempt 2 and gate vars scoped to the task.
func TestE2E_R6_04_RetryInsideEach(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"auth","description":"A"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"z.go":"package main\n\nfunc X() { !!! }\n"}},"2":{"files":{"z.go":"package main\n\nfunc X() {}\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-04.yaml", `name: r6-04
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: converge
        type: code
        prompt: "impl"
        agent: claude-sonnet
        gate: [compile]
        retry:
          - exit: 3
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-04", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/auth/converge.attempt"] != "2" && st["decompose/auth/converge.attempt"] != "3" {
		t.Fatalf("expected retry on converge, got attempt %q", st["decompose/auth/converge.attempt"])
	}
	_ = stdout
}

// TestE2E_R6_05_TaskFatalStopsLaterTasks (spec R6-05): first task exhausts retry; second task never runs.
func TestE2E_R6_05_TaskFatalStopsLaterTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"auth","description":"A"},{"name":"api","description":"B"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"impl":{"files":{"x.go":"package main\n\nfunc X() { !!! }\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-05.yaml", `name: r6-05
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
        retry:
          - exit: 3
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-05", "--agent-stub"}, envWithStubPath(), dir)
	if code == 0 {
		t.Fatal("expected fatal")
	}
	combined := stdout + stderr
	if strings.Contains(combined, "decompose/api/impl") {
		t.Fatalf("second task should not run: %s", combined)
	}
}

// TestE2E_R6_06_SplitParallelOverlap (spec R6-06): parallel split tasks start close together in the ledger (validate steps avoid diff merge collisions).
func TestE2E_R6_06_SplitParallelOverlap(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"t1","description":"a"},{"name":"t2","description":"b"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-06.yaml", `name: r6-06
steps:
  - name: decompose
    type: split
    parallel: true
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: check
        type: validate
        prompt: "v"
        agent: claude-sonnet
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-06", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	b, err := os.ReadFile(filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var t1, t2 time.Time
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
		case "decompose/t1/check":
			t1 = tm
		case "decompose/t2/check":
			t2 = tm
		}
	}
	if t1.IsZero() || t2.IsZero() {
		t.Fatal("missing step_started timestamps for parallel check steps")
	}
	d := t1.Sub(t2)
	if d < 0 {
		d = -d
	}
	if d > 5*time.Second {
		t.Fatalf("parallel impl starts too far apart: %v vs %v", t1, t2)
	}
}

// TestE2E_R6_09_SchemaRetryOnSplit (spec R6-09): invalid plan then valid plan on split; each runs on final tasks.
func TestE2E_R6_09_SchemaRetryOnSplit(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"only","description":"x"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{".gump/out/plan.json":"not-json"}},"2":{"files":{".gump/out/plan.json":"[{\"name\":\"z\",\"description\":\"z\"}]"}}},"by_step":{"impl":{"files":{"z.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-09.yaml", `name: r6-09
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    retry:
      - exit: 2
    each:
      - name: impl
        type: code
        prompt: "x"
        agent: claude-sonnet
        gate: [compile]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-09", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["decompose/z/impl.status"] != "pass" {
		t.Fatalf("expected impl on retried plan task z, state=%+v", st)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "retry") && !strings.Contains(combined, "attempt 2") {
		t.Log("note: retry banner optional; continuing")
	}
	_ = combined
}

// TestE2E_R6_11_ResumeSkipsCompletedSplitTasks (spec R6-11): tasks 1–2 pass; task3 fails compile; resume after fixing file completes without re-running 1–2 impl starts.
func TestE2E_R6_11_ResumeSkipsCompletedSplitTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "ok1.go", "package main\n")
	writeFile(t, dir, "ok2.go", "package main\n")
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"a","description":"a"},{"name":"b","description":"b"},{"name":"c","description":"c"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"3":{"files":{"badc.go":"package main\n\nfunc Y() { !!! }\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-11.yaml", `name: r6-11
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
	stdout1, stderr1, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-11", "--agent-stub"}, envWithStubPath(), dir)
	if code1 == 0 {
		t.Fatalf("expected fatal on task c compile stderr=%s", stderr1)
	}
	runID := extractCookID(stdout1 + stderr1)
	if runID == "" {
		t.Fatal("no run id")
	}
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	writeFile(t, wt, "badc.go", "package main\n\nfunc Y() {}\n")
	// The stub reads .pudding-test-scenario.json from the worktree (copied from the repo). Clear by_attempt so a resumed prompt like "attempt 4/4" cannot re-inject the broken badc.go.
	writeFile(t, wt, ".pudding-test-scenario.json", `{}`)
	_ = os.Remove(filepath.Join(wt, ".gump", "stub-launch-seq"))

	stdout2, stderr2, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, envWithStubPath(), dir)
	if code2 != 0 {
		t.Fatalf("resume exit %d stdout=%s stderr=%s", code2, stdout2, stderr2)
	}
	cookDir := filepath.Join(dir, ".gump", "runs", runID)
	if n := countManifestStepStarted(t, cookDir, "decompose/a/impl"); n != 1 {
		t.Fatalf("task a impl should start once across runs, got %d", n)
	}
	if n := countManifestStepStarted(t, cookDir, "decompose/b/impl"); n != 1 {
		t.Fatalf("task b impl should start once, got %d", n)
	}
	st := readRunState(t, dir, runID)
	if st["decompose/c/impl.status"] != "pass" {
		t.Fatalf("task c should pass after resume: %+v", st["decompose/c/impl.status"])
	}
}

// TestE2E_R6_13_ParallelSplitManyStateWrites (spec R6-13): stress concurrent state writes under split parallel; run CI with -race ./e2e -run R6_13.
func TestE2E_R6_13_ParallelSplitManyStateWrites(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	tasks := `[{"name":"t1","description":"1"},{"name":"t2","description":"2"},{"name":"t3","description":"3"}]`
	writeFile(t, dir, ".pudding-test-plan.json", tasks)
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-13.yaml", `name: r6-13
steps:
  - name: decompose
    type: split
    parallel: true
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: check
        type: validate
        prompt: "x"
        agent: claude-sonnet
        gate: [compile, test]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-13", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	for _, suf := range []string{"t1", "t2", "t3"} {
		base := "decompose/" + suf + "/check"
		if st[base+".status"] != "pass" {
			t.Fatalf("missing pass for %s: %q", base, st[base+".status"])
		}
		if st[base+".gate.test"] != "true" {
			t.Fatalf("expected test gate pass for %s, got %q", base, st[base+".gate.test"])
		}
	}
	_ = stderr
}

// TestE2E_R6_14_PrevIsolatedPerTask (spec R6-14): persisted prev snapshots use full step paths so tasks under a split never share {prev.*} namespaces.
func TestE2E_R6_14_PrevIsolatedPerTask(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"a","description":"a"},{"name":"b","description":"b"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"z.go":"package main\n\nfunc Z() { !!! }\n"}},"2":{"files":{"z.go":"package main\n\nfunc Z() {}\n"}},"3":{"files":{"w.go":"package main\n\nfunc W() { !!! }\n"}},"4":{"files":{"w.go":"package main\n\nfunc W() {}\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-14.yaml", `name: r6-14
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: work
        type: code
        prompt: "try"
        agent: claude-sonnet
        gate: [compile]
        retry:
          - exit: 3
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-14", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	raw, err := os.ReadFile(filepath.Join(dir, ".gump", "runs", runID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bag map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bag); err != nil {
		t.Fatal(err)
	}
	pk, ok := bag[state.PrevSnapshotJSONKey]
	if !ok {
		t.Fatal("expected prev snapshot blob in state.json")
	}
	var prev map[string]map[string]string
	if err := json.Unmarshal(pk, &prev); err != nil {
		t.Fatal(err)
	}
	if prev["decompose/a/work"] == nil || prev["decompose/b/work"] == nil {
		t.Fatalf("prev namespaces should be per qualified step path, got keys: %v", keysOfPrev(prev))
	}
	_ = stdout
	_ = stderr
}

func keysOfPrev(m map[string]map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestE2E_R6_15_QualityAfterSplitEach (spec R6-15): gate-only quality after split+each sees combined worktree.
func TestE2E_R6_15_QualityAfterSplitEach(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"a","description":"a"},{"name":"b","description":"b"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"impl":{"files":{"a.go":"package main\n","b.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r6-15.yaml", `name: r6-15
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
  - name: quality
    gate: [compile, test]
`)
	gitCommitAll(t, dir, "wf")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "r6-15", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	runID := extractCookID(stdout)
	st := readRunState(t, dir, runID)
	if st["quality.status"] != "pass" {
		t.Fatalf("quality should pass after split+each: %+v", st["quality.status"])
	}
}

// TestSmoke_R6_01_ImplementSpecSplitEachQuality (Smoke-R6-01): split, each converge+smoke, quality.
func TestSmoke_R6_01_ImplementSpecSplitEachQuality(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "feat")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"t1","description":"d1"},{"name":"t2","description":"d2"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_step":{"converge":{"files":{"u.go":"package main\n"}},"smoke":{"files":{"v.go":"package main\n"}}}}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/smoke-r6-01.yaml", `name: smoke-r6-01
steps:
  - name: decompose
    type: split
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: converge
        type: code
        prompt: "c"
        agent: claude-sonnet
        gate: [compile]
      - name: smoke
        type: code
        prompt: "s"
        agent: "{converge.agent}"
        session:
          from: converge
        gate: [compile]
  - name: quality
    gate: [compile, test]
`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "smoke-r6-01", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
}

// TestSmoke_R6_02_ParallelSplitTasks (Smoke-R6-02).
func TestSmoke_R6_02_ParallelSplitTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"x","description":"x"},{"name":"y","description":"y"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/smoke-r6-02.yaml", `name: smoke-r6-02
steps:
  - name: decompose
    type: split
    parallel: true
    prompt: "Plan"
    agent: claude-opus
    gate: [schema]
    each:
      - name: check
        type: validate
        prompt: "i"
        agent: claude-sonnet
`)
	gitCommitAll(t, dir, "wf")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "smoke-r6-02", "--agent-stub"}, envWithStubPath(), dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
}

// TestSmoke_R6_03_ResumeAfterEachFailure (Smoke-R6-03).
func TestSmoke_R6_03_ResumeAfterEachFailure(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "needb.go", "package main\n\nfunc NeedB() { !!! }\n")
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"a","description":"a"},{"name":"b","description":"b"}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{}`)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/smoke-r6-03.yaml", `name: smoke-r6-03
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
	stdout1, _, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "smoke-r6-03", "--agent-stub"}, envWithStubPath(), dir)
	if code1 == 0 {
		t.Fatal("expected failure while needb.go does not compile")
	}
	runID := extractCookID(stdout1)
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	writeFile(t, wt, "needb.go", "package main\n\nfunc NeedB() {}\n")
	stdout2, stderr2, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, envWithStubPath(), dir)
	if code2 != 0 {
		t.Fatalf("resume exit %d stdout=%s stderr=%s", code2, stdout2, stderr2)
	}
}
